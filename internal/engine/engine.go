package engine

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/Horac3/Primus/internal/model"
)

// tuned http.Client scaled to the given concurrency level.
func newClient(concurrency int) *http.Client {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.MaxIdleConns = concurrency * 2
	t.MaxIdleConnsPerHost = concurrency
	t.IdleConnTimeout = 30 * time.Second
	return &http.Client{Transport: t}
}

// ProbeResult is the outcome of a single test-connection request.
type ProbeResult struct {
	StatusCode  int    `json:"status_code"`
	LatencyMs   int    `json:"latency_ms"`
	BodyPreview string `json:"body_preview"`
	OK          bool   `json:"ok"`
	ErrMsg      string `json:"err_msg,omitempty"`
}

// applyAuth injects the run's auth credentials — global auth, or one
// worker's credential-pool token — unless the request already carries
// that header (explicit per-endpoint override wins).
func applyAuth(req *http.Request, auth model.AuthConfig) {
	switch auth.Type {
	case model.AuthTypeBearer:
		if auth.Token != "" && req.Header.Get("Authorization") == "" {
			req.Header.Set("Authorization", "Bearer "+auth.Token)
		}
	case model.AuthTypeAPIKey, model.AuthTypeCredentialPool:
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

func buildRun(cfg model.TestConfig, summary model.RunSummary, checks []model.AssertionResult, buckets []int, startedAt, finishedAt time.Time, series []model.Point) model.Run {
	outcome := DetermineOutcome(cfg.Assertions, summary)
	return model.Run{
		ID:               fmt.Sprintf("run-%d", startedAt.UnixNano()),
		TestConfigID:     cfg.ID,
		Mode:             cfg.Mode,
		Concurrency:      cfg.Concurrency,
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
	return RunWithPool(ctx, req, cfg, auth, nil, snapshot)
}

// RunWithPool is RunWithSnapshot with an optional credential pool. When
// pool is non-nil, a login phase runs before measurement starts: each
// worker logs in as its own credential and sends that token instead of
// global auth. Workers whose login failed are excluded; when no login
// succeeds the run returns OutcomeFailNoLogins without dispatching.
func RunWithPool(ctx context.Context, req model.RequestDefinition, cfg model.TestConfig, auth model.AuthConfig, pool *model.CredentialPool, snapshot *atomic.Pointer[[]model.Point]) (model.Run, error) {
	if err := ValidateTestConfig(cfg); err != nil {
		return model.Run{}, err
	}
	if pool != nil {
		if err := ValidatePool(cfg, *pool); err != nil {
			return model.Run{}, err
		}
	}

	client := newClient(cfg.Concurrency)
	defer client.CloseIdleConnections()

	workers := make([]workerAuth, cfg.Concurrency)
	for i := range workers {
		workers[i] = workerAuth{auth: auth}
	}
	var logins *poolLogins
	if pool != nil {
		logins = newPoolLogins(client, *pool)
		workers = logins.loginWorkers(ctx, cfg.Concurrency)
		if len(workers) == 0 || ctx.Err() != nil {
			run := loginAbortedRun(cfg, logins.summary(), len(workers))
			run.Stopped = ctx.Err() != nil
			return run, nil
		}
	}

	// Measurement starts only now, after every login has finished.
	runCtx := ctx
	if cfg.Duration > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithDeadline(ctx, time.Now().Add(cfg.Duration))
		defer cancel()
	}

	buf := cfg.Concurrency
	if cfg.MaxRate > 0 {
		// Unbuffered: a release is the moment a worker takes the job, so
		// MaxRate bounds dispatch, not just queueing.
		buf = 0
	}
	if cfg.BurstPattern != nil && cfg.BurstPattern.Count > buf {
		buf = cfg.BurstPattern.Count
	}
	jobs := make(chan struct{}, buf)
	results := make(chan result, cfg.Concurrency)

	startedAt := time.Now()
	feedDone := make(chan struct{})
	go func() {
		defer close(feedDone)
		feed(runCtx, jobs, cfg, startedAt)
	}()
	runWorkers(runCtx, req, workers, cfg.PayloadPool, jobs, results, client)

	summary, buckets, series := aggregate(results, cfg.Assertions, startedAt, snapshot)
	finishedAt := time.Now()
	// Workers exit on cancel without draining jobs, so feed may still be
	// in a send or burst-idle select; it returns on runCtx.Done.
	<-feedDone
	run := buildRun(cfg, summary, CheckAssertions(cfg.Assertions, summary), buckets, startedAt, finishedAt, series)
	// Stopped: the caller cancelled before the run's own stop condition. A
	// count run that already fired every request finished, even if a Stop
	// raced in afterwards; a Duration deadline lives on runCtx, not ctx.
	run.Stopped = ctx.Err() != nil && !(cfg.TotalRequests > 0 && summary.TotalRequests >= cfg.TotalRequests)
	if logins != nil {
		run.Login = logins.summary()
	}
	return run, nil
}

func Run(ctx context.Context, req model.RequestDefinition, cfg model.TestConfig, auth model.AuthConfig) (model.Run, error) {
	return RunWithSnapshot(ctx, req, cfg, auth, nil)
}
