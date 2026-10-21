package engine

import (
	"context"
	"encoding/json"
	"net/http"
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
	// cancelled: the request failed because the run's own context ended
	// (Stop or the Duration deadline). Neither a success nor an error.
	cancelled bool
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
// Count with Idle ms between them; when MaxRate is set, the n-th job is
// released at least 1/MaxRate after the previous one was taken, so a
// stall never turns into a catch-up burst; otherwise as fast as workers
// drain. Respects ctx cancellation.
func feed(ctx context.Context, jobs chan<- struct{}, cfg model.TestConfig, start time.Time) {
	defer close(jobs)
	sent := 0
	var next time.Time // earliest release of the next job under MaxRate
	for !stopPredicate(cfg, start, sent) {
		if cfg.MaxRate > 0 && sent > 0 {
			if wait := time.Until(next); wait > 0 {
				select {
				case <-ctx.Done():
					return
				case <-time.After(wait):
				}
				if stopPredicate(cfg, start, sent) {
					return
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case jobs <- struct{}{}:
			sent++
			if cfg.MaxRate > 0 {
				next = time.Now().Add(time.Duration(float64(time.Second) / cfg.MaxRate))
			}
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

// workerAuth is one worker's identity. The worker goroutine is its single
// owner: auth is only read and replaced (on re-login) by that goroutine.
type workerAuth struct {
	auth model.AuthConfig
	// relogin, when set, fetches a fresh auth after a 401. It is called
	// at most once per worker.
	relogin func(ctx context.Context) (model.AuthConfig, bool)
}

// runWorkers launches one worker per entry in workers, each pulling from
// jobs and sending exactly one result per request it fires. Closes results
// when all finish.
func runWorkers(
	ctx context.Context,
	req model.RequestDefinition,
	workers []workerAuth,
	payloadPool []json.RawMessage,
	jobs <-chan struct{},
	results chan<- result,
	client *http.Client,
) {
	startWorkers(ctx, req, workers, payloadPool, func() (time.Time, bool) {
		_, ok := <-jobs
		return time.Time{}, ok
	}, func() {}, results, client)
}

// runScheduledWorkers is runWorkers for rate mode: each job is the
// request's intended send time, and its latency is measured from it. A
// worker frees one of the scheduler's in-flight slots once it has
// delivered that request's result.
func runScheduledWorkers(
	ctx context.Context,
	req model.RequestDefinition,
	workers []workerAuth,
	payloadPool []json.RawMessage,
	jobs <-chan time.Time,
	slots <-chan struct{},
	results chan<- result,
	client *http.Client,
) {
	startWorkers(ctx, req, workers, payloadPool, func() (time.Time, bool) {
		due, ok := <-jobs
		return due, ok
	}, func() { <-slots }, results, client)
}

// startWorkers runs the worker loop shared by both job kinds. next blocks
// for the next job and returns its intended send time (zero when the job
// has none) and false once the jobs channel is closed; done runs after
// each fired request's result is delivered.
func startWorkers(
	ctx context.Context,
	req model.RequestDefinition,
	workers []workerAuth,
	payloadPool []json.RawMessage,
	next func() (time.Time, bool),
	done func(),
	results chan<- result,
	client *http.Client,
) {
	var wg sync.WaitGroup
	var nextPayload atomic.Uint64
	for _, w := range workers {
		wg.Add(1)
		go func(w workerAuth) {
			defer wg.Done()
			for {
				due, ok := next()
				if !ok {
					return
				}
				if ctx.Err() != nil {
					return
				}
				request := req
				if len(payloadPool) > 0 {
					index := (nextPayload.Add(1) - 1) % uint64(len(payloadPool))
					request.Body = &model.BodyTemplate{Raw: append([]byte(nil), payloadPool[index]...)}
				}
				r := execRequest(ctx, client, request, w.auth)
				if !due.IsZero() {
					r.latency = time.Since(due)
				}
				// Judge by the run context, not the error type: a request's
				// own timeout also surfaces as a context error, and that one
				// is the target's fault.
				r.cancelled = r.err != nil && ctx.Err() != nil
				// Always deliver: aggregate drains results until close, so
				// every dispatched request is counted exactly once.
				results <- r
				done()
				if r.statusCode == http.StatusUnauthorized && w.relogin != nil {
					relogin := w.relogin
					w.relogin = nil // re-login once per worker
					if fresh, ok := relogin(ctx); ok {
						w.auth = fresh
					}
				}
			}
		}(w)
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
