package engine_test

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Horac3/Primus/internal/engine"
	"github.com/Horac3/Primus/internal/model"
)

// ============================================================
// Rate mode: open-loop fixed req/s (FR-RATE-01..04)
// ============================================================

func rateConfig(rate float64, inFlight int, d time.Duration) model.TestConfig {
	return model.TestConfig{
		ID:          "cfg-rate",
		RequestID:   "req-rate",
		Mode:        model.ModeRate,
		Concurrency: inFlight,
		Duration:    d,
		TargetRate:  rate,
	}
}

func TestValidateTestConfig_TargetRate(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*model.TestConfig)
		wantErr string
	}{
		{"rate run", func(c *model.TestConfig) {}, ""},
		{"missing target", func(c *model.TestConfig) { c.TargetRate = 0 }, "target_rate"},
		{"negative target", func(c *model.TestConfig) { c.TargetRate = -5 }, "target_rate"},
		{"NaN target", func(c *model.TestConfig) { c.TargetRate = math.NaN() }, "target_rate"},
		{"infinite target", func(c *model.TestConfig) { c.TargetRate = math.Inf(1) }, "target_rate"},
		{"below minimum", func(c *model.TestConfig) { c.TargetRate = 1e-12 }, "target_rate"},
		{"smallest target", func(c *model.TestConfig) { c.TargetRate = engine.MinTargetRate }, ""},
		{"count instead of duration", func(c *model.TestConfig) { c.Duration = 0; c.TotalRequests = 100 }, "duration"},
		{"target in load mode", func(c *model.TestConfig) { c.Mode = model.ModeLoad }, "target_rate"},
		{"with max_rate", func(c *model.TestConfig) { c.MaxRate = 5 }, "max_rate"},
		{"with burst pattern", func(c *model.TestConfig) { c.BurstPattern = &model.BurstPattern{Count: 2, Idle: 10} }, "burst"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := rateConfig(100, 10, time.Second)
			tc.mutate(&cfg)
			err := engine.ValidateTestConfig(cfg)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want one mentioning %q", err, tc.wantErr)
			}
		})
	}
}

func TestDetermineOutcome_Dropped(t *testing.T) {
	ok := map[int]int{200: 10}
	pass := model.Assertions{MaxErrorRate: 0.5}
	cases := []struct {
		name       string
		assertions model.Assertions
		summary    model.RunSummary
		want       model.RunOutcome
	}{
		{"dropped beats passing assertions", pass, model.RunSummary{StatusCounts: ok, TotalRequests: 10, Dropped: 1}, model.OutcomeFailRateNotSustained},
		{"dropped beats no assertions", model.Assertions{}, model.RunSummary{StatusCounts: ok, TotalRequests: 10, Dropped: 1}, model.OutcomeFailRateNotSustained},
		{"no requests beats dropped", pass, model.RunSummary{Dropped: 50}, model.OutcomeFailNoRequests},
		{"nothing dropped keeps pass", pass, model.RunSummary{StatusCounts: ok, TotalRequests: 10}, model.OutcomePass},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := engine.DetermineOutcome(tc.assertions, tc.summary); got != tc.want {
				t.Fatalf("outcome = %s, want %s", got, tc.want)
			}
		})
	}
}

// slowTarget answers every request after delay and records the highest
// number of requests it held at once.
type slowTarget struct {
	srv         *httptest.Server
	inFlight    atomic.Int64
	maxInFlight atomic.Int64
	arrivals    atomic.Int64
}

func newSlowTarget(t *testing.T, delay time.Duration) *slowTarget {
	t.Helper()
	s := &slowTarget{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.arrivals.Add(1)
		n := s.inFlight.Add(1)
		defer s.inFlight.Add(-1)
		for {
			m := s.maxInFlight.Load()
			if n <= m || s.maxInFlight.CompareAndSwap(m, n) {
				break
			}
		}
		select {
		case <-r.Context().Done():
		case <-time.After(delay):
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *slowTarget) request() model.RequestDefinition {
	return model.RequestDefinition{ID: "slow", Method: http.MethodPost, URLTemplate: s.srv.URL}
}

// released is every request handed to a worker: finished or cut off by
// the deadline.
func released(s model.RunSummary) int { return s.TotalRequests + s.Cancelled }

func TestRunRate_ReleasesOnScheduleWhateverTheResponseTime(t *testing.T) {
	// Arrange: 300ms responses. Closed loop with 50 workers would send
	// ~166/s; open loop must hold 100/s and need only ~30 in flight.
	target := newSlowTarget(t, 300*time.Millisecond)
	cfg := rateConfig(100, 50, time.Second)

	// Act
	run, err := engine.Run(context.Background(), target.request(), cfg, model.AuthConfig{})

	// Assert
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := released(run.Summary); got < 90 || got > 110 {
		t.Errorf("released = %d, want 100 ±10 (the schedule, not concurrency/latency)", got)
	}
	if run.Summary.Dropped != 0 {
		t.Errorf("Dropped = %d, want 0: the cap was never reached", run.Summary.Dropped)
	}
	if run.Summary.TargetRate != 100 {
		t.Errorf("TargetRate = %g, want 100", run.Summary.TargetRate)
	}
	if got := run.Summary.AchievedRate; got < 85 || got > 115 {
		t.Errorf("AchievedRate = %g, want ~100", got)
	}
}

func TestRunRate_CapReached_CountsDroppedAndNeverExceedsCap(t *testing.T) {
	// Arrange: 200/s for 500ms is 100 due requests; 5 slots of 200ms
	// serve at most ~15 of them.
	target := newSlowTarget(t, 200*time.Millisecond)
	cfg := rateConfig(200, 5, 500*time.Millisecond)

	// Act
	run, err := engine.Run(context.Background(), target.request(), cfg, model.AuthConfig{})

	// Assert
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := target.maxInFlight.Load(); got > 5 {
		t.Errorf("max in flight = %d, want <= 5", got)
	}
	if run.Summary.Dropped < 50 {
		t.Errorf("Dropped = %d, want most of the 100 due requests", run.Summary.Dropped)
	}
	if got := released(run.Summary) + run.Summary.Dropped; got < 95 || got > 100 {
		t.Errorf("released + dropped = %d, want every due slot accounted for (95..100)", got)
	}
	if int64(released(run.Summary)) != target.arrivals.Load() {
		t.Errorf("released = %d, target saw %d", released(run.Summary), target.arrivals.Load())
	}
	if run.Outcome != model.OutcomeFailRateNotSustained {
		t.Errorf("Outcome = %s, want %s", run.Outcome, model.OutcomeFailRateNotSustained)
	}
}

func TestRunRate_Stop_ReturnsPromptlyAndCancelsInFlight(t *testing.T) {
	// Arrange: responses never come back within the run.
	target := newSlowTarget(t, 10*time.Second)
	cfg := rateConfig(50, 100, 10*time.Second)
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
	waitFor(t, func() bool { return target.arrivals.Load() >= 5 })

	// Act
	stoppedAt := time.Now()
	cancel()

	// Assert
	select {
	case run := <-done:
		if waited := time.Since(stoppedAt); waited > 500*time.Millisecond {
			t.Errorf("Run returned %s after Stop, want < 500ms", waited)
		}
		if run.Summary.Cancelled < 5 || run.Summary.ErrorCount != 0 {
			t.Errorf("cancelled = %d, errors = %d; want in-flight cancelled, no errors",
				run.Summary.Cancelled, run.Summary.ErrorCount)
		}
		if !run.Stopped {
			t.Error("Stopped = false, want true")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return within 3s of Stop")
	}
}

// With a pool, workers whose login failed are left out, so the in-flight
// cap is the workers that remain: extra due requests are dropped, never
// queued behind a busy worker.
func TestRunRate_PoolWithFailedLogins_CapIsTheWorkersLeft(t *testing.T) {
	// Arrange: 4 slots asked for, 1 login succeeds, 200ms responses.
	f := newAuthFixture(t, 4)
	f.meDelay = 200 * time.Millisecond
	pool := f.pool(4, 1, 2, 3)
	cfg := rateConfig(50, 4, 500*time.Millisecond)

	// Act
	run, err := engine.RunWithPool(context.Background(), f.meRequest(), cfg, model.AuthConfig{}, &pool, nil)

	// Assert
	if err != nil {
		t.Fatalf("RunWithPool: %v", err)
	}
	if run.Summary.Dropped == 0 {
		t.Error("Dropped = 0, want the requests one worker could not take")
	}
	if p99 := run.Summary.P99Millis; p99 > 350 {
		t.Errorf("p99 = %dms, want ~200ms: due requests were queued instead of dropped", p99)
	}
}

// Achieved rate is measured over the whole schedule window, not up to the
// last due time: 2/s for 1s releases at 0 and 500ms and is 2/s.
func TestRunRate_AchievedRateCoversTheWholeWindow(t *testing.T) {
	// Arrange
	target := newSlowTarget(t, 0)
	cfg := rateConfig(2, 5, time.Second)

	// Act
	run, err := engine.Run(context.Background(), target.request(), cfg, model.AuthConfig{})

	// Assert
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := run.Summary.AchievedRate; got < 1.8 || got > 2.2 {
		t.Errorf("AchievedRate = %g, want ~2", got)
	}
}
