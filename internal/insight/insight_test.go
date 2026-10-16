package insight

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Horac3/Primus/internal/model"
)

func sampleRun() model.Run {
	started := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	duration := 10*time.Second + 213*time.Millisecond
	return model.Run{
		Mode:        model.ModeLoad,
		Concurrency: 400,
		StartedAt:   started,
		FinishedAt:  started.Add(duration),
		Summary: model.RunSummary{
			P50Millis:     1781,
			P95Millis:     2358,
			P99Millis:     2531,
			TotalRequests: 2733,
			ErrorCount:    247,
			ErrorRate:     247.0 / 2733.0,
			StatusCounts:  map[int]int{200: 2486},
			ErrorDetails: []model.ErrorDetail{
				{Cause: `Request failed: Get "https://first.example/api": context deadline exceeded`, Count: 200},
				{Cause: `Request failed: Get "http://second.example/health": context deadline exceeded`, Count: 47},
			},
		},
		LatencyBuckets: []int{0, 0, 0, 0, 0, 0, 0, 0, 2486},
		TimeSeries: []model.Point{
			{ElapsedMs: 1000, AvgLatencyMs: 1781, RPS: 267.6, ErrorRate: 247.0 / 2733.0},
			{ElapsedMs: 2000, AvgLatencyMs: 1781, RPS: 267.6, ErrorRate: 247.0 / 2733.0},
			{ElapsedMs: 3000, AvgLatencyMs: 1781, RPS: 267.6, ErrorRate: 247.0 / 2733.0},
			{ElapsedMs: 4000, AvgLatencyMs: 1781, RPS: 267.6, ErrorRate: 247.0 / 2733.0},
			{ElapsedMs: 5000, AvgLatencyMs: 1781, RPS: 267.6, ErrorRate: 247.0 / 2733.0},
			{ElapsedMs: 6000, AvgLatencyMs: 1781, RPS: 267.6, ErrorRate: 247.0 / 2733.0},
			{ElapsedMs: 7000, AvgLatencyMs: 1781, RPS: 267.6, ErrorRate: 247.0 / 2733.0},
			{ElapsedMs: 8000, AvgLatencyMs: 1781, RPS: 267.6, ErrorRate: 247.0 / 2733.0},
			{ElapsedMs: 9000, AvgLatencyMs: 1781, RPS: 267.6, ErrorRate: 247.0 / 2733.0},
			{ElapsedMs: 10000, AvgLatencyMs: 1781, RPS: 267.6, ErrorRate: 247.0 / 2733.0},
			{ElapsedMs: 10213, AvgLatencyMs: 1781, RPS: 267.6, ErrorRate: 247.0 / 2733.0},
		},
	}
}

func TestBuildSummary(t *testing.T) {
	tests := []struct {
		name string
		run  model.Run
	}{
		{name: "specified run metrics", run: sampleRun()},
		{name: "empty run", run: model.Run{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildSummary(tt.run)
			if tt.name == "empty run" {
				if got.RequestCount != 0 || got.RequestsPerSecond != 0 || len(got.FailureCauses) != 0 {
					t.Fatalf("empty summary = %+v", got)
				}
				return
			}

			if got.P50Millis != 1781 || got.P95Millis != 2358 || got.P99Millis != 2531 {
				t.Errorf("percentiles = %d/%d/%d", got.P50Millis, got.P95Millis, got.P99Millis)
			}
			if got.RequestCount != 2733 || math.Abs(got.RequestsPerSecond-267.6) > 0.1 {
				t.Errorf("requests = %d at %.3f/sec", got.RequestCount, got.RequestsPerSecond)
			}
			if got.ErrorCount != 247 || math.Abs(got.ErrorRate-247.0/2733.0) > 1e-9 {
				t.Errorf("errors = %d at rate %.6f", got.ErrorCount, got.ErrorRate)
			}
			if got.StatusCounts[200] != 2486 {
				t.Errorf("200 status count = %d, want 2486", got.StatusCounts[200])
			}
			if len(got.FailureCauses) != 1 || got.FailureCauses[0].Count != 247 ||
				!strings.Contains(got.FailureCauses[0].Cause, "<url>") {
				t.Errorf("URL-normalized failure causes = %+v", got.FailureCauses)
			}
			if len(got.LatencyBuckets) != len(model.LatencyBucketEdges)+1 || got.LatencyBuckets[8].Count != 2486 {
				t.Errorf("latency buckets = %+v", got.LatencyBuckets)
			}
			if got.Mode != model.ModeLoad || got.AssertionsConfigured || got.Concurrency != 400 {
				t.Errorf("run config summary = mode %q, assertions %v, concurrency %d", got.Mode, got.AssertionsConfigured, got.Concurrency)
			}
		})
	}
}

func TestEvaluate(t *testing.T) {
	base := model.Run{
		Mode: model.ModeLoad, Concurrency: 100,
		StartedAt: time.Unix(0, 0), FinishedAt: time.Unix(1, 0),
		Summary:          model.RunSummary{TotalRequests: 1, P50Millis: 100, StatusCounts: map[int]int{200: 1}},
		AssertionResults: []model.AssertionResult{{Name: "status", Passed: true}},
	}
	withSeries := func(run model.Run, elapsed []int64, latency []float64, rps float64) model.Run {
		for i, ms := range elapsed {
			run.TimeSeries = append(run.TimeSeries, model.Point{ElapsedMs: ms, AvgLatencyMs: latency[i], RPS: rps})
		}
		return run
	}

	tests := []struct {
		name string
		run  model.Run
		want []string
	}{
		{
			name: "specified run triggers all applicable findings",
			run:  sampleRun(),
			want: []string{"CLIENT_TIMEOUT", "SATURATION_FROM_START", "CONCURRENCY_EXCEEDS_CAPACITY", "NO_ASSERTIONS", "PARTIAL_LAST_BUCKET"},
		},
		{
			name: "flat high latency from first time bucket",
			run:  withSeries(base, []int64{1000, 2000, 3000}, []float64{500, 510, 505}, 1),
			want: []string{"SATURATION_FROM_START"},
		},
		{
			name: "estimated in-flight exceeds configured workers",
			run: func() model.Run {
				r := base
				r.Concurrency = 1
				r.Summary.TotalRequests = 2
				return withSeries(r, []int64{1000}, []float64{1000}, 2)
			}(),
			want: []string{"CONCURRENCY_EXCEEDS_CAPACITY"},
		},
		{
			name: "no assertions configured",
			run: func() model.Run {
				r := base
				r.AssertionResults = nil
				return r
			}(),
			want: []string{"NO_ASSERTIONS"},
		},
		{
			name: "partial trailing time bucket",
			run: func() model.Run {
				r := base
				r.FinishedAt = r.StartedAt.Add(1500 * time.Millisecond)
				return withSeries(r, []int64{1000, 1500}, []float64{10, 10}, 0.5)
			}(),
			want: []string{"PARTIAL_LAST_BUCKET"},
		},
		{
			name: "mixed failures are not all client timeouts",
			run: func() model.Run {
				r := base
				r.Summary.ErrorCount = 2
				r.Summary.ErrorDetails = []model.ErrorDetail{{Cause: "context deadline exceeded", Count: 1}, {Cause: "connection reset", Count: 1}}
				return r
			}(),
			want: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			findings := Evaluate(BuildSummary(tt.run))
			got := make([]string, len(findings))
			for i, finding := range findings {
				got[i] = finding.Code
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("finding codes = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBuildSummary_LoginCountsOnly(t *testing.T) {
	// Arrange
	run := model.Run{
		Summary: model.RunSummary{TotalRequests: 10, StatusCounts: map[int]int{200: 10}},
		Login: &model.LoginSummary{
			PoolID: "pool-secret-id", PoolName: "secret pool name",
			Attempted: 5, Succeeded: 3, Failed: 2,
			FailureReasons: []model.ErrorDetail{{Cause: "HTTP 401", Count: 2}},
			Outcomes:       []model.LoginOutcome{{Index: 4, ErrorClass: "HTTP 401"}},
		},
	}

	// Act
	summary := BuildSummary(run)
	payload, _ := json.Marshal(summary)

	// Assert
	if summary.Logins == nil || summary.Logins.Attempted != 5 || summary.Logins.Succeeded != 3 || summary.Logins.Failed != 2 {
		t.Fatalf("Logins = %+v, want 5 attempted, 3 succeeded, 2 failed", summary.Logins)
	}
	for _, leak := range []string{"pool-secret-id", "secret pool name", "outcomes", "HTTP 401"} {
		if strings.Contains(string(payload), leak) {
			t.Errorf("analyzer summary contains %q; only login counts may reach it: %s", leak, payload)
		}
	}
}

func TestEvaluate_LoginFailuresFinding(t *testing.T) {
	cases := []struct {
		name         string
		logins       *LoginCounts
		wantSeverity string
	}{
		{"no pool", nil, ""},
		{"all logins ok", &LoginCounts{Attempted: 3, Succeeded: 3}, ""},
		{"some failed", &LoginCounts{Attempted: 3, Succeeded: 2, Failed: 1}, SeverityWarning},
		{"none succeeded", &LoginCounts{Attempted: 3, Failed: 3}, SeverityCritical},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			summary := Summary{AssertionsConfigured: true, Logins: tc.logins}

			// Act
			findings := Evaluate(summary)

			// Assert
			got := ""
			for _, f := range findings {
				if f.Code == "LOGIN_FAILURES" {
					got = f.Severity
				}
			}
			if got != tc.wantSeverity {
				t.Errorf("LOGIN_FAILURES severity = %q, want %q", got, tc.wantSeverity)
			}
		})
	}
}
