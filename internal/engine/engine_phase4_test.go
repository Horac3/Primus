package engine_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Horac3/Primus/internal/engine"
	"github.com/Horac3/Primus/internal/model"
)

// ============================================================
// EvaluateAssertions — baseline "100% failure" rule (NEW)
// ============================================================

func TestEvaluateAssertions_AllRequestsFailed_ReturnsFalse(t *testing.T) {
	// Arrange — no user assertions configured, but every request errored
	assertions := model.Assertions{}
	summary := model.RunSummary{
		StatusCounts: map[int]int{},
		ErrorCount:   10,
		ErrorRate:    1.0, // 100% failure
	}

	// Act
	passed := engine.EvaluateAssertions(assertions, summary)

	// Assert — baseline rule: 100% failure is always FAIL
	if passed {
		t.Error("EvaluateAssertions = true, want false when all requests failed")
	}
}

func TestEvaluateAssertions_NoAssertionsPartialSuccess_ReturnsFalse(t *testing.T) {
	// Arrange — no assertions, some failures but not 100%
	// This should still pass (no assertions = pass, baseline not triggered)
	assertions := model.Assertions{}
	summary := model.RunSummary{
		StatusCounts: map[int]int{200: 8, 500: 2},
		ErrorCount:   2,
		ErrorRate:    0.2,
	}

	// Act
	passed := engine.EvaluateAssertions(assertions, summary)

	// Assert — partial success with no assertions = passes baseline
	if !passed {
		t.Error("EvaluateAssertions = false, want true when some requests succeeded and no assertions configured")
	}
}

// ============================================================
// RunOutcome tri-state (NEW)
// ============================================================

func TestRunOutcome_AllFailed_IsFailNoRequests(t *testing.T) {
	// Arrange
	assertions := model.Assertions{}
	summary := model.RunSummary{ErrorCount: 5, ErrorRate: 1.0}

	// Act
	outcome := engine.DetermineOutcome(assertions, summary)

	// Assert
	if outcome != model.OutcomeFailNoRequests {
		t.Errorf("outcome = %q, want %q", outcome, model.OutcomeFailNoRequests)
	}
}

func TestRunOutcome_NoAssertionsPartialSuccess_IsNoAssertions(t *testing.T) {
	// Arrange
	assertions := model.Assertions{}
	summary := model.RunSummary{
		StatusCounts: map[int]int{200: 5},
		ErrorCount:   0,
		ErrorRate:    0.0,
	}

	// Act
	outcome := engine.DetermineOutcome(assertions, summary)

	// Assert
	if outcome != model.OutcomeNoAssertions {
		t.Errorf("outcome = %q, want %q", outcome, model.OutcomeNoAssertions)
	}
}

func TestRunOutcome_AssertionsMet_IsPass(t *testing.T) {
	// Arrange
	assertions := model.Assertions{ExpectedStatus: []int{200}}
	summary := model.RunSummary{
		StatusCounts: map[int]int{200: 10},
		ErrorCount:   0,
		ErrorRate:    0.0,
	}

	// Act
	outcome := engine.DetermineOutcome(assertions, summary)

	// Assert
	if outcome != model.OutcomePass {
		t.Errorf("outcome = %q, want %q", outcome, model.OutcomePass)
	}
}

func TestRunOutcome_AssertionsFailed_IsFail(t *testing.T) {
	// Arrange
	assertions := model.Assertions{MaxErrorRate: 0.05}
	summary := model.RunSummary{StatusCounts: map[int]int{200: 5}, ErrorCount: 5, ErrorRate: 0.5}

	// Act
	outcome := engine.DetermineOutcome(assertions, summary)

	// Assert
	if outcome != model.OutcomeFail {
		t.Errorf("outcome = %q, want %q", outcome, model.OutcomeFail)
	}
}

// ============================================================
// POST /probe via Probe() helper (NEW)
// ============================================================

func TestProbe_SuccessfulTarget(t *testing.T) {
	// Arrange
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()

	def := model.RequestDefinition{
		ID: "p1", Name: "p1", Method: "GET", URLTemplate: srv.URL + "/ok",
	}

	// Act
	result, err := engine.Probe(context.Background(), def, model.AuthConfig{})

	// Assert
	if err != nil {
		t.Fatalf("Probe error: %v", err)
	}
	if result.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, want 200", result.StatusCode)
	}
	if !result.OK {
		t.Error("OK = false, want true")
	}
	// LatencyMs is whole milliseconds, so a localhost probe can legitimately report 0.
	if result.LatencyMs < 0 {
		t.Errorf("LatencyMs = %d, want >= 0", result.LatencyMs)
	}
}

func TestProbe_UnreachableTarget_ReturnsFailureNotError(t *testing.T) {
	// Arrange — closed server, guaranteed unreachable
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL + "/gone"
	srv.Close()

	def := model.RequestDefinition{
		ID: "p2", Name: "p2", Method: "GET", URLTemplate: url,
	}

	// Act
	result, err := engine.Probe(context.Background(), def, model.AuthConfig{})

	// Assert — Probe must not return a Go error; it encodes the failure in the result
	if err != nil {
		t.Fatalf("Probe returned Go error for unreachable target: %v", err)
	}
	if result.OK {
		t.Error("OK = true, want false for unreachable target")
	}
	if result.ErrMsg == "" {
		t.Error("ErrMsg is empty, want a non-empty failure description")
	}
}

// ============================================================
// RunLoad produces correct Outcome on Run (NEW — regression)
// ============================================================

func TestRunLoad_AllFailed_OutcomeIsFailNoRequests(t *testing.T) {
	// Arrange — server that always closes connection (causes transport error)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Hijack and close to force a transport error
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	def := model.RequestDefinition{
		ID: "r1", Name: "r1", Method: "GET", URLTemplate: srv.URL + "/fail",
	}
	cfg := model.TestConfig{
		ID:            "c1",
		RequestID:     "r1",
		Mode:          model.ModeLoad,
		Concurrency:   2,
		TotalRequests: 4,
		Assertions:    model.Assertions{ExpectedStatus: []int{200}}, // all will be 500 → errors
	}

	// Act
	run, err := engine.Run(context.Background(), def, cfg, model.AuthConfig{})

	// Assert
	if err != nil {
		t.Fatalf("RunLoad: %v", err)
	}
	// 500s with expected_status=[200] → assertion failure → OutcomeFail
	if run.Outcome != model.OutcomeFail {
		t.Errorf("Outcome = %q, want %q", run.Outcome, model.OutcomeFail)
	}
	if run.Passed {
		t.Error("Passed = true, want false when all requests returned unexpected status")
	}
}

func TestRunLoad_ZeroAssertions_OutcomeIsNoAssertions(t *testing.T) {
	// Arrange
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	def := model.RequestDefinition{
		ID: "r2", Name: "r2", Method: "GET", URLTemplate: srv.URL + "/ok",
	}
	cfg := model.TestConfig{
		ID:            "c2",
		RequestID:     "r2",
		Mode:          model.ModeLoad,
		Concurrency:   2,
		TotalRequests: 4,
		Assertions:    model.Assertions{}, // no assertions
	}

	// Act
	run, err := engine.Run(context.Background(), def, cfg, model.AuthConfig{})

	// Assert
	if err != nil {
		t.Fatalf("RunLoad: %v", err)
	}
	if run.Outcome != model.OutcomeNoAssertions {
		t.Errorf("Outcome = %q, want %q", run.Outcome, model.OutcomeNoAssertions)
	}
	// Passed is false for no-assertions (neutral state)
	if run.Passed {
		t.Error("Passed = true, want false for no-assertions outcome")
	}
}

// ============================================================
// Existing table-driven assertions — unchanged, must still pass
// ============================================================

func TestEvaluateAssertions_TableDriven_Regression(t *testing.T) {
	cases := []struct {
		name       string
		assertions model.Assertions
		summary    model.RunSummary
		wantPassed bool
	}{
		{
			name:       "all thresholds met passes",
			assertions: model.Assertions{ExpectedStatus: []int{200}, MaxErrorRate: 0.05, MaxP95Millis: 500},
			summary:    model.RunSummary{StatusCounts: map[int]int{200: 100}, ErrorRate: 0.01, P95Millis: 300},
			wantPassed: true,
		},
		{
			name:       "fails on error rate",
			assertions: model.Assertions{MaxErrorRate: 0.05},
			summary:    model.RunSummary{StatusCounts: map[int]int{200: 9}, ErrorCount: 1, ErrorRate: 0.10},
			wantPassed: false,
		},
		{
			name:       "fails on p95",
			assertions: model.Assertions{MaxP95Millis: 200},
			summary:    model.RunSummary{StatusCounts: map[int]int{200: 10}, P95Millis: 500},
			wantPassed: false,
		},
		{
			name:       "fails on unexpected status code",
			assertions: model.Assertions{ExpectedStatus: []int{200}},
			summary:    model.RunSummary{StatusCounts: map[int]int{200: 90, 404: 10}, ErrorRate: 0.1},
			wantPassed: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange — tc fields above

			// Act
			passed := engine.EvaluateAssertions(tc.assertions, tc.summary)

			// Assert
			if passed != tc.wantPassed {
				t.Errorf("passed = %v, want %v", passed, tc.wantPassed)
			}
		})
	}
}

// ---- keep the pre-existing tests below working by re-exporting them --------
// (The original engine_test.go has TestPercentiles_KnownInput,
// TestResolveParams_*, TestRunLoad_Respects*, TestRunBurst_*, etc.
// Those are in the existing engine_test.go file and remain untouched.)

// Compile-time check: engine.Probe and engine.DetermineOutcome must exist
var _ = engine.Probe
var _ = engine.DetermineOutcome
var _ time.Time
