package model_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Horac3/Primus/internal/model"
)

func TestRequestDefinition_JSONRoundTrip(t *testing.T) {
	// Arrange
	orig := model.RequestDefinition{
		ID:          "req-1",
		Name:        "Login",
		Method:      "POST",
		URLTemplate: "https://example.com/login",
		Headers:     map[string]string{"Content-Type": "application/json"},
		QueryParams: map[string]model.ParamSpec{
			"trace": {Static: strPtr("abc")},
		},
		Body: &model.BodyTemplate{Raw: []byte(`{"user":"test"}`)},
	}

	// Act
	b, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got model.RequestDefinition
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Assert
	if got.ID != orig.ID {
		t.Errorf("ID: got %q, want %q", got.ID, orig.ID)
	}
	if got.Name != orig.Name {
		t.Errorf("Name: got %q, want %q", got.Name, orig.Name)
	}
	if got.Method != orig.Method {
		t.Errorf("Method: got %q, want %q", got.Method, orig.Method)
	}
	if got.URLTemplate != orig.URLTemplate {
		t.Errorf("URLTemplate: got %q, want %q", got.URLTemplate, orig.URLTemplate)
	}
	if got.Headers["Content-Type"] != orig.Headers["Content-Type"] {
		t.Errorf("Headers: got %v, want %v", got.Headers, orig.Headers)
	}
}

func TestParamSpec_JSONRoundTrip(t *testing.T) {
	// Arrange
	orig := model.ParamSpec{
		Dynamic: &model.DynamicValue{
			Kind:   model.DynamicKindRandomInt,
			IntMin: 1,
			IntMax: 100,
		},
	}

	// Act
	b, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got model.ParamSpec
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Assert
	if got.Dynamic == nil {
		t.Fatal("Dynamic is nil after round-trip")
	}
	if got.Dynamic.Kind != orig.Dynamic.Kind {
		t.Errorf("Dynamic.Kind: got %q, want %q", got.Dynamic.Kind, orig.Dynamic.Kind)
	}
	if got.Dynamic.IntMin != orig.Dynamic.IntMin || got.Dynamic.IntMax != orig.Dynamic.IntMax {
		t.Errorf("Dynamic range: got [%d,%d], want [%d,%d]",
			got.Dynamic.IntMin, got.Dynamic.IntMax,
			orig.Dynamic.IntMin, orig.Dynamic.IntMax)
	}
}

func TestTestConfig_JSONRoundTrip(t *testing.T) {
	// Arrange
	orig := model.TestConfig{
		ID:            "cfg-1",
		RequestID:     "req-1",
		Mode:          model.ModeLoad,
		Concurrency:   10,
		TotalRequests: 1000,
		BurstPattern:  nil,
		Assertions: model.Assertions{
			ExpectedStatus: []int{200, 201},
			MaxP95Millis:   500,
			MaxErrorRate:   0.01,
		},
	}

	// Act
	b, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got model.TestConfig
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Assert
	if got.Mode != orig.Mode {
		t.Errorf("Mode: got %q, want %q", got.Mode, orig.Mode)
	}
	if got.Concurrency != orig.Concurrency {
		t.Errorf("Concurrency: got %d, want %d", got.Concurrency, orig.Concurrency)
	}
	if len(got.Assertions.ExpectedStatus) != 2 {
		t.Errorf("ExpectedStatus len: got %d, want 2", len(got.Assertions.ExpectedStatus))
	}
}

func TestTestConfig_SoakMode_DurationRoundTrip(t *testing.T) {
	// Arrange
	orig := model.TestConfig{
		ID:          "cfg-soak",
		RequestID:   "req-1",
		Mode:        model.ModeSoak,
		Concurrency: 3,
		Duration:    90 * time.Second,
		Assertions:  model.Assertions{ExpectedStatus: []int{200}},
	}

	// Act
	b, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got model.TestConfig
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Assert
	if got.Mode != model.ModeSoak {
		t.Errorf("Mode: got %q, want %q", got.Mode, model.ModeSoak)
	}
	if got.Duration != orig.Duration {
		t.Errorf("Duration: got %v, want %v", got.Duration, orig.Duration)
	}
}

func TestAuthConfig_BasicRoundTrip(t *testing.T) {
	// Arrange
	orig := model.AuthConfig{Type: model.AuthTypeBasic, Username: "u", Password: "p"}

	// Act
	b, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got model.AuthConfig
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Assert
	if got.Type != model.AuthTypeBasic || got.Username != "u" || got.Password != "p" {
		t.Errorf("got %+v, want basic u/p", got)
	}
}

func TestRun_JSONRoundTrip(t *testing.T) {
	// Arrange
	now := time.Now().UTC().Truncate(time.Second)
	orig := model.Run{
		ID:           "run-1",
		TestConfigID: "cfg-1",
		StartedAt:    now,
		FinishedAt:   now.Add(5 * time.Second),
		Summary: model.RunSummary{
			StatusCounts: map[int]int{200: 950, 500: 50},
			P50Millis:    120,
			P95Millis:    480,
			P99Millis:    900,
			ErrorRate:    0.05,
		},
		Passed: true,
	}

	// Act
	b, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got model.Run
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Assert
	if got.ID != orig.ID {
		t.Errorf("ID: got %q, want %q", got.ID, orig.ID)
	}
	if !got.StartedAt.Equal(orig.StartedAt) {
		t.Errorf("StartedAt: got %v, want %v", got.StartedAt, orig.StartedAt)
	}
	if got.Summary.P95Millis != orig.Summary.P95Millis {
		t.Errorf("P95: got %d, want %d", got.Summary.P95Millis, orig.Summary.P95Millis)
	}
	if got.Passed != orig.Passed {
		t.Errorf("Passed: got %v, want %v", got.Passed, orig.Passed)
	}
}

func TestRun_AssertionResultsAndBucketsRoundTrip(t *testing.T) {
	// Arrange
	orig := model.Run{
		ID:           "run-x",
		TestConfigID: "cfg-x",
		StartedAt:    time.Now().UTC().Truncate(time.Second),
		FinishedAt:   time.Now().UTC().Truncate(time.Second),
		AssertionResults: []model.AssertionResult{
			{Name: "Status code: 200", Passed: true},
			{Name: "Error rate < 5%", Passed: false},
		},
		LatencyBuckets: []int{10, 5, 0, 0, 0, 0, 0, 0, 0},
		Passed:         false,
	}

	// Act
	b, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got model.Run
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Assert
	if len(got.AssertionResults) != 2 {
		t.Fatalf("AssertionResults len = %d, want 2", len(got.AssertionResults))
	}
	if got.AssertionResults[0].Name != "Status code: 200" || !got.AssertionResults[0].Passed {
		t.Errorf("AssertionResults[0] = %+v", got.AssertionResults[0])
	}
	if got.AssertionResults[1].Passed {
		t.Errorf("AssertionResults[1] should have Passed=false")
	}
	if len(got.LatencyBuckets) != len(model.LatencyBucketEdges)+1 {
		t.Errorf("LatencyBuckets len = %d, want %d", len(got.LatencyBuckets), len(model.LatencyBucketEdges)+1)
	}
}

func TestRun_TimeSeriesRoundTrip(t *testing.T) {
	// Arrange
	orig := model.Run{
		ID:           "run-timeseries",
		TestConfigID: "cfg-timeseries",
		StartedAt:    time.Now().UTC().Truncate(time.Second),
		FinishedAt:   time.Now().UTC().Truncate(time.Second).Add(2 * time.Second),
		Summary: model.RunSummary{ErrorDetails: []model.ErrorDetail{
			{Cause: "connection refused", Count: 2},
		}},
		TimeSeries: []model.Point{
			{ElapsedMs: 1000, AvgLatencyMs: 120.5, RPS: 8.5, ErrorRate: 0.2},
			{ElapsedMs: 2000, AvgLatencyMs: 150.0, RPS: 10.0, ErrorRate: 0.1},
		},
	}

	// Act
	b, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got model.Run
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Assert
	if len(got.TimeSeries) != 2 {
		t.Fatalf("TimeSeries len = %d, want 2", len(got.TimeSeries))
	}
	if got.TimeSeries[1].ElapsedMs <= got.TimeSeries[0].ElapsedMs {
		t.Fatalf("TimeSeries elapsed times should be monotonic; got %+v", got.TimeSeries)
	}
	if got.TimeSeries[0].AvgLatencyMs != 120.5 {
		t.Fatalf("AvgLatencyMs[0] = %v, want 120.5", got.TimeSeries[0].AvgLatencyMs)
	}
	if len(got.Summary.ErrorDetails) != 1 || got.Summary.ErrorDetails[0] != (model.ErrorDetail{Cause: "connection refused", Count: 2}) {
		t.Fatalf("ErrorDetails = %+v, want persisted transport failure detail", got.Summary.ErrorDetails)
	}
}

func strPtr(s string) *string { return &s }

func TestRateFields_JSONRoundTrip(t *testing.T) {
	cfg := model.TestConfig{Mode: model.ModeRate, Concurrency: 20, Duration: 15 * time.Second, TargetRate: 500}
	run := model.Run{Outcome: model.OutcomeFailRateNotSustained, Summary: model.RunSummary{Dropped: 7, TargetRate: 500, AchievedRate: 493.5}}

	var gotCfg model.TestConfig
	var gotRun model.Run
	for _, rt := range []struct{ in, out any }{{cfg, &gotCfg}, {run, &gotRun}} {
		b, err := json.Marshal(rt.in)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if err := json.Unmarshal(b, rt.out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
	}

	if gotCfg.Mode != model.ModeRate || gotCfg.TargetRate != 500 {
		t.Errorf("config = %+v, want mode rate and target_rate 500", gotCfg)
	}
	s := gotRun.Summary
	if gotRun.Outcome != model.OutcomeFailRateNotSustained || s.Dropped != 7 || s.TargetRate != 500 || s.AchievedRate != 493.5 {
		t.Errorf("run = %+v, want the rate fields back", gotRun)
	}
}
