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
// Verdict — zero completed requests never passes
// ============================================================

func TestDetermineOutcome_ZeroCompletedRequests_IsFailNoRequests(t *testing.T) {
	cases := []struct {
		name       string
		assertions model.Assertions
	}{
		{"only max error rate", model.Assertions{MaxErrorRate: 0.05}},
		{"only max p95", model.Assertions{MaxP95Millis: 500}},
		{"no assertions configured", model.Assertions{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange — nothing completed: 0 errors out of 0 requests
			summary := model.RunSummary{StatusCounts: map[int]int{}}

			// Act
			outcome := engine.DetermineOutcome(tc.assertions, summary)
			passed := engine.EvaluateAssertions(tc.assertions, summary)

			// Assert
			if outcome != model.OutcomeFailNoRequests {
				t.Errorf("outcome = %q, want %q", outcome, model.OutcomeFailNoRequests)
			}
			if passed {
				t.Error("EvaluateAssertions = true, want false with zero completed requests")
			}
		})
	}
}

func TestRun_StoppedBeforeAnyRequest_FailsAndIsMarkedStopped(t *testing.T) {
	// Arrange — the run is stopped before a single request completes
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	def := model.RequestDefinition{Method: "GET", URLTemplate: srv.URL}
	cfg := model.TestConfig{
		Mode:        model.ModeLoad,
		Concurrency: 2,
		Duration:    10 * time.Second,
		Assertions:  model.Assertions{MaxErrorRate: 0.05},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Act
	run, err := engine.Run(ctx, def, cfg, model.AuthConfig{})

	// Assert
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.Outcome != model.OutcomeFailNoRequests || run.Passed {
		t.Errorf("outcome = %q passed = %v, want %q and not passed", run.Outcome, run.Passed, model.OutcomeFailNoRequests)
	}
	if !run.Stopped {
		t.Error("Stopped = false, want true for a cancelled run")
	}
}

func TestRun_StoppedAfterSuccesses_KeepsVerdictAndIsMarkedStopped(t *testing.T) {
	// Arrange — a long soak that is stopped once a few requests succeeded
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	def := model.RequestDefinition{Method: "GET", URLTemplate: srv.URL}
	// Expected-status rather than max-error-rate: requests in flight at
	// cancel may surface as transport errors, which is not under test here.
	cfg := model.TestConfig{
		Mode:        model.ModeSoak,
		Concurrency: 2,
		Duration:    30 * time.Second,
		Assertions:  model.Assertions{ExpectedStatus: []int{200}},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		for hits.Load() < 5 {
			time.Sleep(time.Millisecond)
		}
		cancel()
	}()

	// Act
	run, err := engine.Run(ctx, def, cfg, model.AuthConfig{})

	// Assert
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.Outcome != model.OutcomePass || !run.Passed {
		t.Errorf("outcome = %q passed = %v, want %q; summary %+v", run.Outcome, run.Passed, model.OutcomePass, run.Summary)
	}
	if !run.Stopped {
		t.Error("Stopped = false, want true for a run stopped early")
	}
}

func TestRun_NaturalCompletion_IsNotMarkedStopped(t *testing.T) {
	// Arrange
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	def := model.RequestDefinition{Method: "GET", URLTemplate: srv.URL}
	cfg := model.TestConfig{Mode: model.ModeLoad, Concurrency: 2, TotalRequests: 4}

	// Act
	run, err := engine.Run(context.Background(), def, cfg, model.AuthConfig{})

	// Assert
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.Stopped {
		t.Error("Stopped = true, want false for a run that reached its stop condition")
	}
}
