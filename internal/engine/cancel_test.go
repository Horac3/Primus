package engine_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Horac3/Primus/internal/engine"
	"github.com/Horac3/Primus/internal/model"
)

// ============================================================
// Cancelled in-flight requests — neither success nor error
// ============================================================

// scriptedTarget answers the first len(script) requests with the matching
// handler, then holds every later request open until the client gives up.
// arrivals counts every request the server received.
type scriptedTarget struct {
	srv      *httptest.Server
	arrivals atomic.Int64
}

func newScriptedTarget(t *testing.T, script ...http.HandlerFunc) *scriptedTarget {
	t.Helper()
	target := &scriptedTarget{}
	release := make(chan struct{})
	target.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := target.arrivals.Add(1)
		if int(n) <= len(script) {
			script[n-1](w, r)
			return
		}
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(func() {
		close(release)
		target.srv.Close()
	})
	return target
}

func respond(code int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code) }
}

// resetConn drops the connection without a response: a transport error.
func resetConn(w http.ResponseWriter, r *http.Request) {
	conn, _, err := w.(http.Hijacker).Hijack()
	if err == nil {
		conn.Close()
	}
}

// POST, so net/http never silently retries a request on a dropped
// connection and each dispatch reaches the target exactly once.
func (s *scriptedTarget) request() model.RequestDefinition {
	return model.RequestDefinition{ID: "scripted", Method: http.MethodPost, URLTemplate: s.srv.URL}
}

// stopWhen starts a count run that cannot finish on its own, cancels it once
// the target has received inFlightAt requests, and returns the final Run.
func stopWhen(t *testing.T, target *scriptedTarget, cfg model.TestConfig, inFlightAt int64) model.Run {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan model.Run, 1)
	go func() {
		run, err := engine.Run(ctx, target.request(), cfg, model.AuthConfig{})
		if err != nil {
			t.Errorf("Run: %v", err)
		}
		done <- run
	}()
	waitFor(t, func() bool { return target.arrivals.Load() == inFlightAt })
	cancel()
	select {
	case run := <-done:
		return run
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return within 3s of Stop")
		return model.Run{}
	}
}

func stoppableCfg(concurrency int, assertions model.Assertions) model.TestConfig {
	return model.TestConfig{
		ID: "cfg-stop", Mode: model.ModeLoad,
		Concurrency: concurrency, TotalRequests: 1000,
		Assertions: assertions,
	}
}

func TestRun_StopMidRun_InFlightRequestsAreCancelledNotErrors(t *testing.T) {
	// Arrange: 6 quick 200s, then all 3 workers hang in flight.
	target := newScriptedTarget(t,
		respond(200), respond(200), respond(200), respond(200), respond(200), respond(200))
	cfg := stoppableCfg(3, model.Assertions{MaxErrorRate: 0.05})

	// Act
	run := stopWhen(t, target, cfg, 9)

	// Assert
	s := run.Summary
	if s.ErrorCount != 0 || s.ErrorRate != 0 {
		t.Errorf("errors = %d (rate %v), want 0: the stop is not the target's fault; details %+v",
			s.ErrorCount, s.ErrorRate, s.ErrorDetails)
	}
	if s.Cancelled != 3 {
		t.Errorf("Cancelled = %d, want 3 (the requests in flight at Stop)", s.Cancelled)
	}
	if s.TotalRequests != 6 || s.StatusCounts[200] != 6 {
		t.Errorf("TotalRequests = %d, status counts = %v; want 6 completed 200s", s.TotalRequests, s.StatusCounts)
	}
	if run.Outcome != model.OutcomePass {
		t.Errorf("outcome = %q, want %q: cancelled requests must not fail the verdict", run.Outcome, model.OutcomePass)
	}
	if !run.Stopped {
		t.Error("Stopped = false, want true")
	}
}

func TestRun_StopMidRun_Real500BeforeStopIsStillAnError(t *testing.T) {
	// Arrange
	target := newScriptedTarget(t, respond(200), respond(500), respond(200), respond(500))
	cfg := stoppableCfg(3, model.Assertions{ExpectedStatus: []int{200}})

	// Act
	run := stopWhen(t, target, cfg, 7)

	// Assert
	s := run.Summary
	if s.ErrorCount != 2 || s.ErrorRate != 0.5 {
		t.Errorf("errors = %d (rate %v), want 2 (rate 0.5): the 500s came from the target", s.ErrorCount, s.ErrorRate)
	}
	if s.StatusCounts[200] != 2 || s.StatusCounts[500] != 2 {
		t.Errorf("status counts = %v, want 2×200 and 2×500", s.StatusCounts)
	}
	if s.Cancelled != 3 {
		t.Errorf("Cancelled = %d, want 3", s.Cancelled)
	}
}

func TestRun_StopMidRun_EveryDispatchedRequestCountedOnce(t *testing.T) {
	// Arrange: responses, unexpected statuses and dropped connections, then 3 in flight.
	target := newScriptedTarget(t,
		respond(200), respond(500), resetConn, respond(200), resetConn, respond(500), respond(200))
	cfg := stoppableCfg(3, model.Assertions{ExpectedStatus: []int{200}})

	// Act
	run := stopWhen(t, target, cfg, 10)

	// Assert
	s := run.Summary
	dispatched := int(target.arrivals.Load())
	completed := 0
	for _, n := range s.StatusCounts {
		completed += n
	}
	transportErrors := s.TotalRequests - completed
	if completed+transportErrors+s.Cancelled != dispatched {
		t.Errorf("completed %d + transport errors %d + cancelled %d != dispatched %d",
			completed, transportErrors, s.Cancelled, dispatched)
	}
	if completed != 5 || transportErrors != 2 || s.Cancelled != 3 {
		t.Errorf("completed = %d, transport errors = %d, cancelled = %d; want 5, 2, 3",
			completed, transportErrors, s.Cancelled)
	}
	if s.ErrorCount != 4 {
		t.Errorf("ErrorCount = %d, want 4 (2 unexpected 500s + 2 dropped connections)", s.ErrorCount)
	}
}

func TestRun_DurationDeadline_InFlightRequestsAreCancelledNotErrors(t *testing.T) {
	// Arrange: 3 quick 200s, then every worker hangs until the deadline.
	target := newScriptedTarget(t, respond(200), respond(200), respond(200))
	cfg := model.TestConfig{
		ID: "cfg-deadline", Mode: model.ModeSoak, Concurrency: 3,
		Duration:   300 * time.Millisecond,
		Assertions: model.Assertions{MaxErrorRate: 0.05},
	}

	// Act
	run, err := engine.Run(context.Background(), target.request(), cfg, model.AuthConfig{})

	// Assert
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	s := run.Summary
	if s.ErrorCount != 0 || s.Cancelled != 3 || s.TotalRequests != 3 {
		t.Errorf("errors = %d, cancelled = %d, total = %d; want 0, 3, 3", s.ErrorCount, s.Cancelled, s.TotalRequests)
	}
	if run.Stopped || run.Outcome != model.OutcomePass {
		t.Errorf("stopped = %v, outcome = %q; want a natural, passing finish", run.Stopped, run.Outcome)
	}
}

func TestRun_NaturalCompletion_NothingCancelled(t *testing.T) {
	// Arrange
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	req := model.RequestDefinition{ID: "ok", Method: http.MethodGet, URLTemplate: srv.URL}
	cfg := model.TestConfig{ID: "cfg-natural", Mode: model.ModeLoad, Concurrency: 3, TotalRequests: 20}

	// Act
	run, err := engine.Run(context.Background(), req, cfg, model.AuthConfig{})

	// Assert
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	s := run.Summary
	if s.Cancelled != 0 || s.TotalRequests != 20 || s.StatusCounts[200] != 20 || s.ErrorCount != 0 {
		t.Errorf("summary = %+v, want 20 completed 200s, no errors, nothing cancelled", s)
	}
	if run.Stopped {
		t.Error("Stopped = true, want false")
	}
}
