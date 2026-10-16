package insight

import (
	"math"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Horac3/Primus/internal/model"
)

const (
	SeverityCritical = "critical"
	SeverityWarning  = "warning"
	SeverityInfo     = "info"

	timeBucketWidth = int64(time.Second / time.Millisecond)
)

var urlPattern = regexp.MustCompile(`(?i)https?://[^\s"'<>]+`)

type FailureCause struct {
	Cause string `json:"cause"`
	Count int    `json:"count"`
}

type LatencyBucket struct {
	LowerBoundMs int  `json:"lower_bound_ms"`
	UpperBoundMs *int `json:"upper_bound_ms,omitempty"`
	Count        int  `json:"count"`
}

type Summary struct {
	P50Millis            int             `json:"p50_millis"`
	P95Millis            int             `json:"p95_millis"`
	P99Millis            int             `json:"p99_millis"`
	RequestCount         int             `json:"request_count"`
	RequestsPerSecond    float64         `json:"requests_per_second"`
	ErrorCount           int             `json:"error_count"`
	ErrorRate            float64         `json:"error_rate"`
	StatusCounts         map[int]int     `json:"status_counts"`
	FailureCauses        []FailureCause  `json:"failure_causes"`
	LatencyBuckets       []LatencyBucket `json:"latency_buckets"`
	AverageLatencyMs     float64         `json:"average_latency_ms"`
	Mode                 model.Mode      `json:"mode"`
	AssertionsConfigured bool            `json:"assertions_configured"`
	Concurrency          int             `json:"concurrency"`
	Logins               *LoginCounts    `json:"logins,omitempty"`

	runDuration time.Duration
	timeSeries  []model.Point
}

// LoginCounts is all of a credential-pool login phase that may reach an
// analyzer: counts only, never pool names, credentials, tokens or reasons.
type LoginCounts struct {
	Attempted int `json:"attempted"`
	Succeeded int `json:"succeeded"`
	Failed    int `json:"failed"`
}

type Finding struct {
	Code     string         `json:"code"`
	Severity string         `json:"severity"`
	Message  string         `json:"message"`
	Values   map[string]any `json:"values"`
}

// BuildSummary projects a completed run into metrics suitable for diagnosis.
func BuildSummary(run model.Run) Summary {
	summary := Summary{
		P50Millis:            run.Summary.P50Millis,
		P95Millis:            run.Summary.P95Millis,
		P99Millis:            run.Summary.P99Millis,
		RequestCount:         run.Summary.TotalRequests,
		ErrorCount:           run.Summary.ErrorCount,
		ErrorRate:            run.Summary.ErrorRate,
		StatusCounts:         cloneStatusCounts(run.Summary.StatusCounts),
		Mode:                 run.Mode,
		AssertionsConfigured: len(run.AssertionResults) > 0,
		Concurrency:          run.Concurrency,
		runDuration:          run.FinishedAt.Sub(run.StartedAt),
		timeSeries:           append([]model.Point(nil), run.TimeSeries...),
	}
	if summary.runDuration > 0 {
		summary.RequestsPerSecond = float64(summary.RequestCount) / summary.runDuration.Seconds()
	}
	summary.FailureCauses = normalizeFailureCauses(run.Summary.ErrorDetails)
	summary.LatencyBuckets = describeLatencyBuckets(run.LatencyBuckets)
	summary.AverageLatencyMs = averageLatency(run.TimeSeries)
	if run.Login != nil {
		summary.Logins = &LoginCounts{
			Attempted: run.Login.Attempted,
			Succeeded: run.Login.Succeeded,
			Failed:    run.Login.Failed,
		}
	}
	return summary
}

// Evaluate emits stable diagnostic codes in rule order.
func Evaluate(summary Summary) []Finding {
	findings := make([]Finding, 0, 5)
	if summary.Logins != nil && summary.Logins.Failed > 0 {
		severity, message := SeverityWarning, "Some credential-pool logins failed; those workers were excluded from the run."
		if summary.Logins.Succeeded == 0 {
			severity, message = SeverityCritical, "No credential-pool login succeeded, so no requests were sent."
		}
		findings = append(findings, Finding{
			Code: "LOGIN_FAILURES", Severity: severity, Message: message,
			Values: map[string]any{
				"logins_attempted": summary.Logins.Attempted,
				"logins_succeeded": summary.Logins.Succeeded,
				"logins_failed":    summary.Logins.Failed,
			},
		})
	}
	if allFailuresAreTimeouts(summary) {
		findings = append(findings, Finding{
			Code: "CLIENT_TIMEOUT", Severity: SeverityCritical,
			Message: "All failed requests ended with context deadline exceeded.",
			Values:  map[string]any{"error_count": summary.ErrorCount, "failure_causes": summary.FailureCauses},
		})
	}
	if flatLatencyFromStart(summary) {
		findings = append(findings, Finding{
			Code: "SATURATION_FROM_START", Severity: SeverityWarning,
			Message: "Latency was already elevated in the first time bucket and stayed nearly flat.",
			Values:  map[string]any{"first_bucket_latency_ms": summary.timeSeries[0].AvgLatencyMs, "p50_ms": summary.P50Millis},
		})
	}
	inFlight := summary.RequestsPerSecond * summary.AverageLatencyMs / 1000
	if summary.Concurrency > 0 && inFlight > float64(summary.Concurrency) {
		findings = append(findings, Finding{
			Code: "CONCURRENCY_EXCEEDS_CAPACITY", Severity: SeverityWarning,
			Message: "Estimated in-flight requests exceed the configured worker concurrency.",
			Values: map[string]any{
				"estimated_in_flight": inFlight,
				"concurrency":         summary.Concurrency,
				"requests_per_second": summary.RequestsPerSecond,
				"average_latency_ms":  summary.AverageLatencyMs,
			},
		})
	}
	if !summary.AssertionsConfigured {
		findings = append(findings, Finding{
			Code: "NO_ASSERTIONS", Severity: SeverityInfo,
			Message: "No assertions were configured, so the run has no pass/fail criteria.",
			Values:  map[string]any{"assertions_configured": false},
		})
	}
	if hasPartialLastBucket(summary.timeSeries) {
		last := summary.timeSeries[len(summary.timeSeries)-1]
		previousElapsed := int64(0)
		if len(summary.timeSeries) > 1 {
			previousElapsed = summary.timeSeries[len(summary.timeSeries)-2].ElapsedMs
		}
		findings = append(findings, Finding{
			Code: "PARTIAL_LAST_BUCKET", Severity: SeverityInfo,
			Message: "The final time-series bucket covers less than one second.",
			Values:  map[string]any{"bucket_duration_ms": last.ElapsedMs - previousElapsed, "bucket_width_ms": timeBucketWidth},
		})
	}
	return findings
}

func cloneStatusCounts(counts map[int]int) map[int]int {
	if counts == nil {
		return nil
	}
	clone := make(map[int]int, len(counts))
	for code, count := range counts {
		clone[code] = count
	}
	return clone
}

func normalizeFailureCauses(details []model.ErrorDetail) []FailureCause {
	counts := make(map[string]int, len(details))
	for _, detail := range details {
		cause := strings.Join(strings.Fields(urlPattern.ReplaceAllString(detail.Cause, "<url>")), " ")
		counts[cause] += detail.Count
	}
	causes := make([]FailureCause, 0, len(counts))
	for cause, count := range counts {
		causes = append(causes, FailureCause{Cause: cause, Count: count})
	}
	sort.Slice(causes, func(i, j int) bool { return causes[i].Cause < causes[j].Cause })
	return causes
}

func describeLatencyBuckets(counts []int) []LatencyBucket {
	buckets := make([]LatencyBucket, len(counts))
	lower := 0
	for i, count := range counts {
		bucket := LatencyBucket{LowerBoundMs: lower, Count: count}
		if i < len(model.LatencyBucketEdges) {
			upper := model.LatencyBucketEdges[i]
			bucket.UpperBoundMs = &upper
			lower = upper
		}
		buckets[i] = bucket
	}
	return buckets
}

func averageLatency(points []model.Point) float64 {
	var weightedLatency, weight float64
	for _, point := range points {
		successRPS := point.RPS * (1 - point.ErrorRate)
		if point.AvgLatencyMs <= 0 || successRPS <= 0 {
			continue
		}
		weightedLatency += point.AvgLatencyMs * successRPS
		weight += successRPS
	}
	if weight == 0 {
		return 0
	}
	return weightedLatency / weight
}

func allFailuresAreTimeouts(summary Summary) bool {
	if summary.ErrorCount == 0 || len(summary.FailureCauses) == 0 {
		return false
	}
	count := 0
	for _, cause := range summary.FailureCauses {
		if !strings.Contains(strings.ToLower(cause.Cause), "context deadline exceeded") {
			return false
		}
		count += cause.Count
	}
	return count == summary.ErrorCount
}

func flatLatencyFromStart(summary Summary) bool {
	if summary.P50Millis <= 0 || len(summary.timeSeries) < 2 {
		return false
	}
	first := summary.timeSeries[0].AvgLatencyMs
	if first < float64(summary.P50Millis) || first <= 0 {
		return false
	}
	for _, point := range summary.timeSeries[1:] {
		if point.RPS <= 0 || point.AvgLatencyMs <= 0 || math.Abs(point.AvgLatencyMs-first)/first > 0.10 {
			return false
		}
	}
	return summary.timeSeries[0].RPS > 0
}

func hasPartialLastBucket(points []model.Point) bool {
	if len(points) == 0 {
		return false
	}
	previousElapsed := int64(0)
	if len(points) > 1 {
		previousElapsed = points[len(points)-2].ElapsedMs
	}
	lastDuration := points[len(points)-1].ElapsedMs - previousElapsed
	return lastDuration > 0 && lastDuration < timeBucketWidth
}
