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
// ValidateTestConfig — exactly one of TotalRequests / Duration
// ============================================================

func TestValidateTestConfig_TableDriven(t *testing.T) {
	cases := []struct {
		name    string
		cfg     model.TestConfig
		wantErr bool
	}{
		{
			name:    "count only passes",
			cfg:     model.TestConfig{Mode: model.ModeLoad, Concurrency: 2, TotalRequests: 10},
			wantErr: false,
		},
		{
			name:    "duration only passes",
			cfg:     model.TestConfig{Mode: model.ModeSoak, Concurrency: 2, Duration: time.Second},
			wantErr: false,
		},
		{
			name:    "both set rejected",
			cfg:     model.TestConfig{Mode: model.ModeLoad, Concurrency: 2, TotalRequests: 10, Duration: time.Second},
			wantErr: true,
		},
		{
			name:    "neither set rejected",
			cfg:     model.TestConfig{Mode: model.ModeLoad, Concurrency: 2},
			wantErr: true,
		},
		{
			name:    "zero concurrency rejected",
			cfg:     model.TestConfig{Mode: model.ModeLoad, TotalRequests: 10},
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange — tc.cfg above

			// Act
			err := engine.ValidateTestConfig(tc.cfg)

			// Assert
			if (err != nil) != tc.wantErr {
				t.Errorf("err = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}

// ============================================================
// CheckAssertions — one entry per configured assertion
// ============================================================

func TestCheckAssertions_TableDriven(t *testing.T) {
	okSummary := model.RunSummary{
		StatusCounts: map[int]int{200: 100},
		ErrorRate:    0.01,
		P95Millis:    120,
	}
	cases := []struct {
		name       string
		assertions model.Assertions
		summary    model.RunSummary
		want       []model.AssertionResult
	}{
		{
			name:       "all met",
			assertions: model.Assertions{ExpectedStatus: []int{200}, MaxP95Millis: 500, MaxErrorRate: 0.05},
			summary:    okSummary,
			want: []model.AssertionResult{
				{Name: "Status code: 200", Passed: true},
				{Name: "Latency (p95) < 500ms", Passed: true},
				{Name: "Error rate < 5%", Passed: true},
			},
		},
		{
			name:       "unexpected status fails its entry only",
			assertions: model.Assertions{ExpectedStatus: []int{200}, MaxP95Millis: 500},
			summary:    model.RunSummary{StatusCounts: map[int]int{200: 90, 500: 10}, ErrorRate: 0.1, P95Millis: 120},
			want: []model.AssertionResult{
				{Name: "Status code: 200", Passed: false},
				{Name: "Latency (p95) < 500ms", Passed: true},
			},
		},
		{
			name:       "p95 and error rate fail",
			assertions: model.Assertions{MaxP95Millis: 200, MaxErrorRate: 0.05},
			summary:    model.RunSummary{StatusCounts: map[int]int{200: 90}, ErrorRate: 0.1, P95Millis: 400},
			want: []model.AssertionResult{
				{Name: "Latency (p95) < 200ms", Passed: false},
				{Name: "Error rate < 5%", Passed: false},
			},
		},
		{
			name:       "no assertions configured, no entries",
			assertions: model.Assertions{},
			summary:    okSummary,
			want:       nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange — tc fields above

			// Act
			got := engine.CheckAssertions(tc.assertions, tc.summary)

			// Assert
			if len(got) != len(tc.want) {
				t.Fatalf("len = %d, want %d (%v)", len(got), len(tc.want), got)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("[%d] = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// ============================================================
// Bucketize — fixed edges, counts sum to input size
// ============================================================

func TestBucketize_KnownDistribution(t *testing.T) {
	// Arrange — one sample per interesting region
	samples := []int{10, 49, 50, 99, 150, 199, 200, 299, 350, 599, 600, 5000}

	// Act
	buckets := engine.Bucketize(samples)

	// Assert — every sample lands in exactly one bucket
	if len(buckets) != len(model.LatencyBucketEdges)+1 {
		t.Fatalf("len = %d, want %d", len(buckets), len(model.LatencyBucketEdges)+1)
	}
	total := 0
	for _, c := range buckets {
		total += c
	}
	if total != len(samples) {
		t.Errorf("sum = %d, want %d", total, len(samples))
	}
	// Spot checks: 10,49 → bucket 0 (<50); 50,99 → bucket 1; 600,5000 → last
	if buckets[0] != 2 {
		t.Errorf("buckets[0] = %d, want 2", buckets[0])
	}
	if buckets[1] != 2 {
		t.Errorf("buckets[1] = %d, want 2", buckets[1])
	}
	if buckets[len(buckets)-1] != 2 {
		t.Errorf("overflow = %d, want 2", buckets[len(buckets)-1])
	}
}

// ============================================================
// Unified Run — duration mode stops at the deadline
// ============================================================

func TestRun_DurationMode_StopsAtDeadline(t *testing.T) {
	// Arrange — fast target, short deadline
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	def := model.RequestDefinition{
		ID: "u1", Name: "u1", Method: "GET", URLTemplate: srv.URL + "/x",
	}
	cfg := model.TestConfig{
		ID: "cfg-u1", RequestID: "u1", Mode: model.ModeSoak,
		Concurrency: 2, Duration: 2 * time.Second,
		Assertions: model.Assertions{ExpectedStatus: []int{200}},
	}

	// Act
	start := time.Now()
	run, err := engine.Run(context.Background(), def, cfg, model.AuthConfig{})
	elapsed := time.Since(start)

	// Assert
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if elapsed < 1500*time.Millisecond || elapsed > 15*time.Second {
		t.Errorf("elapsed = %v, want ≈2s deadline", elapsed)
	}
	if len(run.Summary.StatusCounts) == 0 {
		t.Error("no requests recorded")
	}
	if !run.Passed {
		t.Errorf("Outcome = %q Passed = %v, want pass/true", run.Outcome, run.Passed)
	}
}

// ============================================================
// Unified Run — count mode behaves like the original RunLoad
// ============================================================

func TestRun_CountMode_CompletesFixedCount(t *testing.T) {
	// Arrange
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	def := model.RequestDefinition{
		ID: "u2", Name: "u2", Method: "GET", URLTemplate: srv.URL + "/x",
	}
	cfg := model.TestConfig{
		ID: "cfg-u2", RequestID: "u2", Mode: model.ModeLoad,
		Concurrency: 2, TotalRequests: 10,
		Assertions: model.Assertions{ExpectedStatus: []int{200}},
	}

	// Act
	run, err := engine.Run(context.Background(), def, cfg, model.AuthConfig{})

	// Assert — same behavior as the original count-based RunLoad
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.Summary.StatusCounts[200] != 10 {
		t.Errorf("status 200 count = %d, want 10", run.Summary.StatusCounts[200])
	}
	if len(run.AssertionResults) != 1 || !run.AssertionResults[0].Passed {
		t.Errorf("AssertionResults = %+v, want one passing entry", run.AssertionResults)
	}
	if run.Summary.TotalRequests != 10 {
		t.Errorf("Summary.TotalRequests = %d, want 10", run.Summary.TotalRequests)
	}
	total := 0
	for _, c := range run.LatencyBuckets {
		total += c
	}
	if total != 10 {
		t.Errorf("bucket sum = %d, want 10", total)
	}
	if !run.Passed {
		t.Error("Passed = false, want true")
	}
}

func TestRun_RecordsUnexpectedStatusCause(t *testing.T) {
	// Arrange
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	def := model.RequestDefinition{ID: "err-status", Name: "err-status", Method: "GET", URLTemplate: srv.URL}
	cfg := model.TestConfig{
		ID: "cfg-err-status", RequestID: def.ID, Mode: model.ModeLoad,
		Concurrency: 2, TotalRequests: 3,
		Assertions: model.Assertions{ExpectedStatus: []int{http.StatusOK}},
	}

	// Act
	run, err := engine.Run(context.Background(), def, cfg, model.AuthConfig{})

	// Assert
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(run.Summary.ErrorDetails) != 1 {
		t.Fatalf("ErrorDetails = %+v, want one grouped cause", run.Summary.ErrorDetails)
	}
	detail := run.Summary.ErrorDetails[0]
	if detail.Cause != "Unexpected HTTP status 503 (expected: 200)" || detail.Count != 3 {
		t.Errorf("ErrorDetails[0] = %+v, want status 503 counted 3 times", detail)
	}
}

func TestRun_RecordsTransportFailureCause(t *testing.T) {
	// Arrange
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	url := srv.URL
	srv.Close()
	def := model.RequestDefinition{ID: "err-transport", Name: "err-transport", Method: "GET", URLTemplate: url}
	cfg := model.TestConfig{ID: "cfg-err-transport", RequestID: def.ID, Mode: model.ModeLoad, Concurrency: 1, TotalRequests: 2}

	// Act
	run, err := engine.Run(context.Background(), def, cfg, model.AuthConfig{})

	// Assert
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(run.Summary.ErrorDetails) != 1 || run.Summary.ErrorDetails[0].Count != 2 || run.Summary.ErrorDetails[0].Cause == "" {
		t.Errorf("ErrorDetails = %+v, want one non-empty transport cause counted twice", run.Summary.ErrorDetails)
	}
}

func TestRun_InvalidConfig_ReturnsError(t *testing.T) {
	// Arrange — both stop conditions set
	def := model.RequestDefinition{ID: "u3", Name: "u3", Method: "GET", URLTemplate: "http://x.invalid/"}
	cfg := model.TestConfig{
		ID: "cfg-u3", RequestID: "u3", Mode: model.ModeLoad,
		Concurrency: 1, TotalRequests: 5, Duration: time.Second,
	}

	// Act
	_, err := engine.Run(context.Background(), def, cfg, model.AuthConfig{})

	// Assert
	if err == nil {
		t.Error("expected validation error, got nil")
	}
}
