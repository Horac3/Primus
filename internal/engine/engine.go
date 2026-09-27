package engine

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net/http"
	"sort"
	"strings"
	"sync"
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

// applyAuth sets the Authorization headers on req from the global auth
// config. Called at dispatch time so every mode uses current credentials.
func applyAuth(req *http.Request, auth model.AuthConfig) {
	switch auth.Type {
	case model.AuthTypeBearer:
		if auth.Token != "" {
			req.Header.Set("Authorization", "Bearer "+auth.Token)
		}
	case model.AuthTypeAPIKey:
		if auth.Header != "" && auth.Value != "" {
			req.Header.Set(auth.Header, auth.Value)
		}
	case model.AuthTypeBasic:
		if auth.Username != "" || auth.Password != "" {
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

// feedJobs sends totalRequests job tokens onto jobs, then closes it.
// Respects ctx cancellation.
func feedJobs(ctx context.Context, jobs chan<- struct{}, total int) {
	defer close(jobs)
	for i := 0; i < total; i++ {
		select {
		case <-ctx.Done():
			return
		case jobs <- struct{}{}:
		}
	}
}

// feedBurst sends jobs in pulses of burstCount with idleMs sleep between them.
func feedBurst(ctx context.Context, jobs chan<- struct{}, total, burstCount, idleMs int) {
	defer close(jobs)
	sent := 0
	for sent < total {
		// Send one burst
		batchSize := burstCount
		if sent+batchSize > total {
			batchSize = total - sent
		}
		for i := 0; i < batchSize; i++ {
			select {
			case <-ctx.Done():
				return
			case jobs <- struct{}{}:
				sent++
			}
		}
		if sent >= total {
			break
		}
		// Idle between bursts
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(idleMs) * time.Millisecond):
		}
	}
}

// feedSoak sends job tokens continuously until ctx expires, then closes
// the channel. Used by RunSoak for deadline-based dispatch.
func feedSoak(ctx context.Context, jobs chan<- struct{}) {
	defer close(jobs)
	for {
		select {
		case <-ctx.Done():
			return
		case jobs <- struct{}{}:
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

	// Global auth, applied at dispatch time.
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

// aggregate consumes all results and builds a RunSummary.
func aggregate(results <-chan result, assertions model.Assertions, start time.Time) model.RunSummary {
	statusCounts := make(map[int]int)
	var latencies []int
	var errorCount int
	total := 0

	allowed := make(map[int]bool, len(assertions.ExpectedStatus))
	for _, code := range assertions.ExpectedStatus {
		allowed[code] = true
	}

	for r := range results {
		total++
		if r.err != nil {
			errorCount++
			continue
		}
		statusCounts[r.statusCode]++
		latencies = append(latencies, int(r.latency.Milliseconds()))

		// Count unexpected status codes as errors for error-rate purposes
		if len(allowed) > 0 && !allowed[r.statusCode] {
			errorCount++
		}
	}

	p50, p95, p99 := Percentiles(latencies)
	var errorRate float64
	if total > 0 {
		errorRate = float64(errorCount) / float64(total)
	}

	return model.RunSummary{
		StatusCounts: statusCounts,
		ErrorCount:   errorCount,
		P50Millis:    p50,
		P95Millis:    p95,
		P99Millis:    p99,
		ErrorRate:    errorRate,
	}
}

// buildRun assembles the final Run from a summary.
func buildRun(cfg model.TestConfig, summary model.RunSummary, startedAt, finishedAt time.Time) model.Run {
	outcome := DetermineOutcome(cfg.Assertions, summary)
	return model.Run{
		ID:           fmt.Sprintf("run-%d", startedAt.UnixNano()),
		TestConfigID: cfg.ID,
		StartedAt:    startedAt,
		FinishedAt:   finishedAt,
		Summary:      summary,
		Outcome:      outcome,
		Passed:       outcome == model.OutcomePass,
	}
}

// ============================================================
// RunLoad
// ============================================================

// RunLoad executes a load test: cfg.Concurrency workers drain cfg.TotalRequests
// jobs as fast as possible, then returns the aggregated Run.
func RunLoad(ctx context.Context, req model.RequestDefinition, cfg model.TestConfig, auth model.AuthConfig) (model.Run, error) {
	client := newClient(cfg.Concurrency)
	jobs := make(chan struct{}, cfg.Concurrency)
	results := make(chan result, cfg.Concurrency)

	startedAt := time.Now()
	go feedJobs(ctx, jobs, cfg.TotalRequests)
	runWorkers(ctx, req, auth, jobs, results, cfg.Concurrency, client)

	summary := aggregate(results, cfg.Assertions, startedAt)
	finishedAt := time.Now()
	return buildRun(cfg, summary, startedAt, finishedAt), nil
}

// ============================================================
// RunBurst
// ============================================================

// RunBurst executes a burst test: jobs are released in pulses per
// cfg.BurstPattern.Count with cfg.BurstPattern.Idle ms between bursts.
func RunBurst(ctx context.Context, req model.RequestDefinition, cfg model.TestConfig, auth model.AuthConfig) (model.Run, error) {
	if cfg.BurstPattern == nil {
		return RunLoad(ctx, req, cfg, auth)
	}
	client := newClient(cfg.Concurrency)
	jobs := make(chan struct{}, cfg.BurstPattern.Count)
	results := make(chan result, cfg.Concurrency)

	startedAt := time.Now()
	go feedBurst(ctx, jobs, cfg.TotalRequests, cfg.BurstPattern.Count, cfg.BurstPattern.Idle)
	runWorkers(ctx, req, auth, jobs, results, cfg.Concurrency, client)

	summary := aggregate(results, cfg.Assertions, startedAt)
	finishedAt := time.Now()
	return buildRun(cfg, summary, startedAt, finishedAt), nil
}

// ============================================================
// RunSoak
// ============================================================

// RunSoak executes a soak test: cfg.Concurrency workers fire requests
// continuously until cfg.Duration elapses, then returns the aggregated Run.
// Unlike RunLoad/RunBurst it is bounded by a deadline, not a fixed count.
func RunSoak(ctx context.Context, req model.RequestDefinition, cfg model.TestConfig, auth model.AuthConfig) (model.Run, error) {
	deadlineCtx, cancel := context.WithDeadline(ctx, time.Now().Add(cfg.Duration))
	defer cancel()

	client := newClient(cfg.Concurrency)
	jobs := make(chan struct{}, cfg.Concurrency)
	results := make(chan result, cfg.Concurrency)

	startedAt := time.Now()
	go feedSoak(deadlineCtx, jobs)
	runWorkers(deadlineCtx, req, auth, jobs, results, cfg.Concurrency, client)

	summary := aggregate(results, cfg.Assertions, startedAt)
	finishedAt := time.Now()
	return buildRun(cfg, summary, startedAt, finishedAt), nil
}
