package engine

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Horac3/Primus/internal/model"
)

// result is a single HTTP call outcome, emitted by each worker.
type result struct {
	statusCode int
	latency    time.Duration
	err        error
}

// tuned http.Client scaled to the given concurrency level.
func newClient(concurrency int) *http.Client {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.MaxIdleConns = concurrency * 2
	t.MaxIdleConnsPerHost = concurrency
	t.IdleConnTimeout = 30 * time.Second
	return &http.Client{Transport: t}
}

// ============================================================
// Public helper: Percentiles
// ============================================================

// Percentiles returns the p50, p95, and p99 of a slice of millisecond samples.
// The slice is sorted in place. Returns 0 for all if the slice is empty.
func Percentiles(samples []int) (p50, p95, p99 int) {
	if len(samples) == 0 {
		return 0, 0, 0
	}
	sort.Ints(samples)
	p50 = samples[percentileIdx(len(samples), 50)]
	p95 = samples[percentileIdx(len(samples), 95)]
	p99 = samples[percentileIdx(len(samples), 99)]
	return
}

// percentileIdx returns the 0-based index for the given percentile.
func percentileIdx(n, pct int) int {
	idx := (n*pct+99)/100 - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= n {
		idx = n - 1
	}
	return idx
}

// ============================================================
// Public helper: ValidateTestConfig
// ============================================================

// ValidateTestConfig rejects configs that set both or neither of the two
// mutually exclusive stop conditions (TotalRequests vs Duration), and
// non-positive concurrency. Callers must validate before running.
func ValidateTestConfig(cfg model.TestConfig) error {
	if cfg.Concurrency < 1 {
		return fmt.Errorf("concurrency must be >= 1 (got %d)", cfg.Concurrency)
	}
	countSet := cfg.TotalRequests > 0
	durSet := cfg.Duration > 0
	if countSet == durSet {
		return fmt.Errorf("set exactly one of total_requests (%d) or duration (%s), not both/neither",
			cfg.TotalRequests, cfg.Duration)
	}
	return nil
}

// ============================================================
// Public helper: CheckAssertions
// ============================================================

// CheckAssertions returns one AssertionResult per configured assertion.
// Entries mirror EvaluateAssertions item-by-item; the overall pass/fail
// (including the 100%-failure baseline) stays with DetermineOutcome.
func CheckAssertions(a model.Assertions, s model.RunSummary) []model.AssertionResult {
	var out []model.AssertionResult
	if len(a.ExpectedStatus) > 0 {
		allowed := make(map[int]bool, len(a.ExpectedStatus))
		for _, code := range a.ExpectedStatus {
			allowed[code] = true
		}
		passed := len(s.StatusCounts) > 0
		for code := range s.StatusCounts {
			if !allowed[code] {
				passed = false
				break
			}
		}
		names := make([]string, 0, len(a.ExpectedStatus))
		for _, code := range a.ExpectedStatus {
			names = append(names, strconv.Itoa(code))
		}
		out = append(out, model.AssertionResult{
			Name:   "Status code: " + strings.Join(names, ", "),
			Passed: passed,
		})
	}
	if a.MaxP95Millis > 0 {
		out = append(out, model.AssertionResult{
			Name:   fmt.Sprintf("Latency (p95) < %dms", a.MaxP95Millis),
			Passed: !(s.P95Millis > a.MaxP95Millis),
		})
	}
	if a.MaxErrorRate > 0 {
		out = append(out, model.AssertionResult{
			Name:   "Error rate < " + strconv.FormatFloat(a.MaxErrorRate*100, 'f', -1, 64) + "%",
			Passed: !(s.ErrorRate > a.MaxErrorRate),
		})
	}
	return out
}

// ============================================================
// Public helper: Bucketize
// ============================================================

// Bucketize counts samples into the fixed model.LatencyBucketEdges buckets
// plus a final >= last-edge overflow bucket. Counts sum to len(samples).
func Bucketize(samples []int) []int {
	edges := model.LatencyBucketEdges
	buckets := make([]int, len(edges)+1)
	for _, s := range samples {
		placed := false
		for i, e := range edges {
			if s < e {
				buckets[i]++
				placed = true
				break
			}
		}
		if !placed {
			buckets[len(buckets)-1]++
		}
	}
	return buckets
}

// ============================================================
// Public helper: EvaluateAssertions
// ============================================================

// EvaluateAssertions returns true if all configured assertions hold.
// Baseline rule: if 100% of requests failed (ErrorRate==1.0 and no successful
// status codes recorded), it always returns false regardless of configured assertions.
func EvaluateAssertions(a model.Assertions, s model.RunSummary) bool {
	// Baseline: 100% failure is always a fail.
	if s.ErrorRate >= 1.0 && len(s.StatusCounts) == 0 {
		return false
	}
	// Error rate
	if a.MaxErrorRate > 0 && s.ErrorRate > a.MaxErrorRate {
		return false
	}
	// P95
	if a.MaxP95Millis > 0 && s.P95Millis > a.MaxP95Millis {
		return false
	}
	// Expected status codes
	if len(a.ExpectedStatus) > 0 {
		allowed := make(map[int]bool, len(a.ExpectedStatus))
		for _, code := range a.ExpectedStatus {
			allowed[code] = true
		}
		for code := range s.StatusCounts {
			if !allowed[code] {
				return false
			}
		}
	}
	return true
}

// DetermineOutcome returns the tri-state RunOutcome for a completed run.
func DetermineOutcome(a model.Assertions, s model.RunSummary) model.RunOutcome {
	// Baseline: 100% failure overrides everything.
	if s.ErrorRate >= 1.0 && len(s.StatusCounts) == 0 {
		return model.OutcomeFailNoRequests
	}
	// No assertions configured and at least one success → neutral.
	hasAssertions := a.MaxErrorRate > 0 || a.MaxP95Millis > 0 || len(a.ExpectedStatus) > 0
	if !hasAssertions {
		return model.OutcomeNoAssertions
	}
	if EvaluateAssertions(a, s) {
		return model.OutcomePass
	}
	return model.OutcomeFail
}

// ProbeResult is the outcome of a single test-connection request.
type ProbeResult struct {
	StatusCode  int    `json:"status_code"`
	LatencyMs   int    `json:"latency_ms"`
	BodyPreview string `json:"body_preview"`
	OK          bool   `json:"ok"`
	ErrMsg      string `json:"err_msg,omitempty"`
}

// applyAuth injects the global auth credentials — unless the request
// already carries that header (explicit per-endpoint override wins).
func applyAuth(req *http.Request, auth model.AuthConfig) {
	switch auth.Type {
	case model.AuthTypeBearer:
		if auth.Token != "" && req.Header.Get("Authorization") == "" {
			req.Header.Set("Authorization", "Bearer "+auth.Token)
		}
	case model.AuthTypeAPIKey:
		if auth.Header != "" && auth.Value != "" && req.Header.Get(auth.Header) == "" {
			req.Header.Set(auth.Header, auth.Value)
		}
	case model.AuthTypeBasic:
		if (auth.Username != "" || auth.Password != "") && req.Header.Get("Authorization") == "" {
			req.SetBasicAuth(auth.Username, auth.Password)
		}
	}
}

// Probe fires a single HTTP request and returns its result. It never returns
// a Go error — transport failures are encoded in ProbeResult.OK/ErrMsg.
func Probe(ctx context.Context, def model.RequestDefinition, auth model.AuthConfig) (ProbeResult, error) {
	client := newClient(1)
	r := execRequest(ctx, client, def, auth)
	pr := ProbeResult{
		StatusCode: r.statusCode,
		LatencyMs:  int(r.latency.Milliseconds()),
		OK:         r.err == nil && r.statusCode > 0,
	}
	if r.err != nil {
		pr.ErrMsg = r.err.Error()
	}
	return pr, nil
}

// ============================================================
// Public helper: ResolveParams
// ============================================================

// ResolveParams resolves all ParamSpec entries into concrete string values.
func ResolveParams(specs map[string]model.ParamSpec) map[string]string {
	out := make(map[string]string, len(specs))
	for name, spec := range specs {
		out[name] = resolveOne(spec)
	}
	return out
}

func resolveOne(spec model.ParamSpec) string {
	if spec.Static != nil {
		return *spec.Static
	}
	if spec.Dynamic == nil {
		return ""
	}
	d := spec.Dynamic
	switch d.Kind {
	case model.DynamicKindRandomInt:
		spread := d.IntMax - d.IntMin + 1
		if spread <= 0 {
			spread = 1
		}
		n := d.IntMin + rand.IntN(spread)
		return fmt.Sprintf("%d", n)
	case model.DynamicKindRandomString:
		length := d.StringLen
		if length <= 0 {
			length = 8
		}
		return randomString(length)
	case model.DynamicKindSequential:
		// Sequential without shared state returns a random int in range;
		// proper sequential counters are wired per-run in the worker pool.
		return fmt.Sprintf("%d", d.SeqStart)
	default:
		return ""
	}
}

const charset = "abcdefghijklmnopqrstuvwxyz0123456789"

func randomString(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = charset[rand.IntN(len(charset))]
	}
	return string(b)
}

// ============================================================
// Internal: job feeder and worker pool
// ============================================================

// stopPredicate reports whether the run is complete: duration-based when
// cfg.Duration > 0, count-based otherwise (exactly one is always set;
// see ValidateTestConfig).
func stopPredicate(cfg model.TestConfig, start time.Time, sent int) bool {
	if cfg.Duration > 0 {
		return time.Since(start) >= cfg.Duration
	}
	return sent >= cfg.TotalRequests
}

// feed sends job tokens until the stop predicate holds, then closes the
// channel. When a BurstPattern is set, jobs are released in pulses of
// Count with Idle ms between them; otherwise as fast as workers drain.
// Respects ctx cancellation.
func feed(ctx context.Context, jobs chan<- struct{}, cfg model.TestConfig, start time.Time) {
	defer close(jobs)
	sent := 0
	for !stopPredicate(cfg, start, sent) {
		select {
		case <-ctx.Done():
			return
		case jobs <- struct{}{}:
			sent++
		}
		// Burst pacing: idle between pulses, never after the final one.
		if cfg.BurstPattern != nil && cfg.BurstPattern.Count > 0 &&
			sent%cfg.BurstPattern.Count == 0 && !stopPredicate(cfg, start, sent) {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Duration(cfg.BurstPattern.Idle) * time.Millisecond):
			}
		}
	}
}

// runWorkers launches concurrency workers, each pulling from jobs and sending
// results to results. Closes results when all workers finish.
func runWorkers(
	ctx context.Context,
	req model.RequestDefinition,
	auth model.AuthConfig,
	jobs <-chan struct{},
	results chan<- result,
	concurrency int,
	client *http.Client,
) {
	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range jobs {
				if ctx.Err() != nil {
					return
				}
				r := execRequest(ctx, client, req, auth)
				select {
				case results <- r:
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	go func() {
		wg.Wait()
		close(results)
	}()
}

// execRequest builds and fires one HTTP request, returning a result.
func execRequest(ctx context.Context, client *http.Client, def model.RequestDefinition, auth model.AuthConfig) result {
	url := def.URLTemplate
	// Resolve dynamic query params into URL query string
	if len(def.QueryParams) > 0 {
		resolved := ResolveParams(def.QueryParams)
		params := make([]string, 0, len(resolved))
		for k, v := range resolved {
			// Path params: substitute {name} placeholders
			placeholder := "{" + k + "}"
			if strings.Contains(url, placeholder) {
				url = strings.ReplaceAll(url, placeholder, v)
			} else {
				params = append(params, k+"="+v)
			}
		}
		if len(params) > 0 {
			url += "?" + strings.Join(params, "&")
		}
	}

	var bodyReader *strings.Reader
	method := def.Method
	if method == "" {
		method = "GET"
	}
	if def.Body != nil && len(def.Body.Raw) > 0 {
		bodyReader = strings.NewReader(string(def.Body.Raw))
	}

	var req *http.Request
	var err error
	if bodyReader != nil {
		req, err = http.NewRequestWithContext(ctx, method, url, bodyReader)
	} else {
		req, err = http.NewRequestWithContext(ctx, method, url, nil)
	}
	if err != nil {
		return result{err: err}
	}

	// Per-endpoint header overrides ride along verbatim — except the
	// [GLOBAL] sentinel, which explicitly defers to global auth below.
	for k, v := range def.Headers {
		if v == model.HeaderGlobalSentinel {
			continue
		}
		req.Header.Set(k, v)
	}

	// Global auth, applied at dispatch time (skips headers already set above).
	applyAuth(req, auth)

	start := time.Now()
	resp, err := client.Do(req)
	latency := time.Since(start)
	if err != nil {
		return result{latency: latency, err: err}
	}
	resp.Body.Close()
	return result{statusCode: resp.StatusCode, latency: latency}
}

func storeSeriesSnapshot(snapshot *atomic.Pointer[[]model.Point], series []model.Point) {
	if snapshot == nil {
		return
	}
	clone := append([]model.Point(nil), series...)
	snapshot.Store(&clone)
}

func emitSeriesPoint(
	start time.Time,
	windowRequests int,
	windowSuccesses int,
	windowLatencyMS float64,
	windowErrors int,
	series *[]model.Point,
	snapshot *atomic.Pointer[[]model.Point],
) {
	elapsedMs := time.Since(start).Milliseconds()
	point := model.Point{ElapsedMs: elapsedMs}
	if windowRequests > 0 {
		point.RPS = float64(windowRequests) / 1.0
		if windowErrors > 0 {
			point.ErrorRate = float64(windowErrors) / float64(windowRequests)
		}
	}
	if windowSuccesses > 0 {
		point.AvgLatencyMs = windowLatencyMS / float64(windowSuccesses)
	}
	*series = append(*series, point)
	storeSeriesSnapshot(snapshot, *series)
}

// aggregate consumes all results and builds a RunSummary plus the latency
// bucket histogram. Buckets cover completed responses; transport errors
// have no latency sample.
func aggregate(results <-chan result, assertions model.Assertions, start time.Time, snapshot *atomic.Pointer[[]model.Point]) (model.RunSummary, []int, []model.Point) {
	statusCounts := make(map[int]int)
	errorCauses := make(map[string]int)
	var latencies []int
	var errorCount int
	total := 0
	windowRequests := 0
	windowSuccesses := 0
	windowLatencyTotalMs := 0.0
	windowErrors := 0
	series := make([]model.Point, 0)
	allowed := make(map[int]bool, len(assertions.ExpectedStatus))
	for _, code := range assertions.ExpectedStatus {
		allowed[code] = true
	}

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case r, ok := <-results:
			if !ok {
				if total > 0 || len(series) > 0 {
					emitSeriesPoint(start, windowRequests, windowSuccesses, windowLatencyTotalMs, windowErrors, &series, snapshot)
				}
				goto finalize
			}
			total++
			windowRequests++
			if r.err != nil {
				errorCount++
				windowErrors++
				errorCauses["Request failed: "+r.err.Error()]++
				continue
			}
			statusCounts[r.statusCode]++
			latencies = append(latencies, int(r.latency.Milliseconds()))
			windowSuccesses++
			windowLatencyTotalMs += float64(r.latency.Milliseconds())
			if len(allowed) > 0 && !allowed[r.statusCode] {
				errorCount++
				windowErrors++
				expected := make([]string, len(assertions.ExpectedStatus))
				for i, code := range assertions.ExpectedStatus {
					expected[i] = strconv.Itoa(code)
				}
				cause := fmt.Sprintf("Unexpected HTTP status %d (expected: %s)", r.statusCode, strings.Join(expected, ", "))
				errorCauses[cause]++
			}
		case <-ticker.C:
			if windowRequests == 0 {
				continue
			}
			point := model.Point{
				ElapsedMs:    time.Since(start).Milliseconds(),
				RPS:          float64(windowRequests) / 1.0,
				AvgLatencyMs: 0,
				ErrorRate:    0,
			}
			if windowSuccesses > 0 {
				point.AvgLatencyMs = windowLatencyTotalMs / float64(windowSuccesses)
			}
			if windowRequests > 0 {
				point.ErrorRate = float64(windowErrors) / float64(windowRequests)
			}
			series = append(series, point)
			storeSeriesSnapshot(snapshot, series)
			windowRequests = 0
			windowSuccesses = 0
			windowLatencyTotalMs = 0
			windowErrors = 0
		}
	}

finalize:
	p50, p95, p99 := Percentiles(latencies)
	var errorRate float64
	if total > 0 {
		errorRate = float64(errorCount) / float64(total)
	}
	errorDetails := make([]model.ErrorDetail, 0, len(errorCauses))
	for cause, count := range errorCauses {
		errorDetails = append(errorDetails, model.ErrorDetail{Cause: cause, Count: count})
	}
	sort.Slice(errorDetails, func(i, j int) bool {
		return errorDetails[i].Cause < errorDetails[j].Cause
	})

	return model.RunSummary{
		StatusCounts:  statusCounts,
		ErrorCount:    errorCount,
		ErrorDetails:  errorDetails,
		P50Millis:     p50,
		P95Millis:     p95,
		P99Millis:     p99,
		ErrorRate:     errorRate,
		TotalRequests: total,
	}, Bucketize(latencies), series
}

// buildRun assembles the final Run from a summary.
func buildRun(cfg model.TestConfig, summary model.RunSummary, checks []model.AssertionResult, buckets []int, startedAt, finishedAt time.Time, series []model.Point) model.Run {
	outcome := DetermineOutcome(cfg.Assertions, summary)
	return model.Run{
		ID:               fmt.Sprintf("run-%d", startedAt.UnixNano()),
		TestConfigID:     cfg.ID,
		StartedAt:        startedAt,
		FinishedAt:       finishedAt,
		Summary:          summary,
		Outcome:          outcome,
		Passed:           outcome == model.OutcomePass,
		AssertionResults: checks,
		LatencyBuckets:   buckets,
		TimeSeries:       series,
	}
}

// ============================================================
// Run — unified dispatch for every mode
// ============================================================

// Run executes a test: cfg.Concurrency workers drain jobs until the stop
// predicate holds — a fixed TotalRequests count, or a Duration deadline
// (exactly one must be set; see ValidateTestConfig). Burst pacing applies
// when cfg.BurstPattern is set. Returns the aggregated Run.
func RunWithSnapshot(ctx context.Context, req model.RequestDefinition, cfg model.TestConfig, auth model.AuthConfig, snapshot *atomic.Pointer[[]model.Point]) (model.Run, error) {
	if err := ValidateTestConfig(cfg); err != nil {
		return model.Run{}, err
	}

	runCtx := ctx
	if cfg.Duration > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithDeadline(ctx, time.Now().Add(cfg.Duration))
		defer cancel()
	}

	client := newClient(cfg.Concurrency)
	buf := cfg.Concurrency
	if cfg.BurstPattern != nil && cfg.BurstPattern.Count > buf {
		buf = cfg.BurstPattern.Count
	}
	jobs := make(chan struct{}, buf)
	results := make(chan result, cfg.Concurrency)

	startedAt := time.Now()
	go feed(runCtx, jobs, cfg, startedAt)
	runWorkers(runCtx, req, auth, jobs, results, cfg.Concurrency, client)

	summary, buckets, series := aggregate(results, cfg.Assertions, startedAt, snapshot)
	finishedAt := time.Now()
	return buildRun(cfg, summary, CheckAssertions(cfg.Assertions, summary), buckets, startedAt, finishedAt, series), nil
}

func Run(ctx context.Context, req model.RequestDefinition, cfg model.TestConfig, auth model.AuthConfig) (model.Run, error) {
	return RunWithSnapshot(ctx, req, cfg, auth, nil)
}
