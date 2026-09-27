package engine_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Horac3/Primus/internal/engine"
	"github.com/Horac3/Primus/internal/model"
)

// ============================================================
// Percentile calculation
// ============================================================

func TestPercentiles_KnownInput(t *testing.T) {
	// Arrange — 100 samples 1..100 ms
	samples := make([]int, 100)
	for i := range samples {
		samples[i] = i + 1
	}

	// Act
	p50, p95, p99 := engine.Percentiles(samples)

	// Assert
	if p50 != 50 {
		t.Errorf("p50 = %d, want 50", p50)
	}
	if p95 != 95 {
		t.Errorf("p95 = %d, want 95", p95)
	}
	if p99 != 99 {
		t.Errorf("p99 = %d, want 99", p99)
	}
}

// ============================================================
// Assertion evaluation
// ============================================================

func TestEvaluateAssertions_TableDriven(t *testing.T) {
	cases := []struct {
		name       string
		assertions model.Assertions
		summary    model.RunSummary
		wantPassed bool
	}{
		{
			name:       "no assertions always passes",
			assertions: model.Assertions{},
			summary:    model.RunSummary{ErrorRate: 0.5, P95Millis: 9999},
			wantPassed: true,
		},
		{
			name: "all thresholds met",
			assertions: model.Assertions{
				ExpectedStatus: []int{200},
				MaxErrorRate:   0.05,
				MaxP95Millis:   500,
			},
			summary: model.RunSummary{
				StatusCounts: map[int]int{200: 100},
				ErrorRate:    0.01,
				P95Millis:    300,
			},
			wantPassed: true,
		},
		{
			name: "fails on error rate",
			assertions: model.Assertions{
				MaxErrorRate: 0.05,
			},
			summary:    model.RunSummary{ErrorRate: 0.10},
			wantPassed: false,
		},
		{
			name: "fails on p95",
			assertions: model.Assertions{
				MaxP95Millis: 200,
			},
			summary:    model.RunSummary{P95Millis: 500},
			wantPassed: false,
		},
		{
			name: "fails on unexpected status code",
			assertions: model.Assertions{
				ExpectedStatus: []int{200},
			},
			summary: model.RunSummary{
				StatusCounts: map[int]int{200: 90, 404: 10},
				ErrorRate:    0.1,
			},
			wantPassed: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange — tc fields set above

			// Act
			passed := engine.EvaluateAssertions(tc.assertions, tc.summary)

			// Assert
			if passed != tc.wantPassed {
				t.Errorf("passed = %v, want %v", passed, tc.wantPassed)
			}
		})
	}
}

// ============================================================
// Dynamic param resolution
// ============================================================

func TestResolveParams_RandomIntVariesAndStaysInBounds(t *testing.T) {
	// Arrange
	spec := map[string]model.ParamSpec{
		"petId": {
			Dynamic: &model.DynamicValue{
				Kind:   model.DynamicKindRandomInt,
				IntMin: 1,
				IntMax: 100,
			},
		},
	}

	// Act — resolve 50 times and collect values
	seen := make(map[string]bool)
	for i := 0; i < 50; i++ {
		resolved := engine.ResolveParams(spec)
		v, ok := resolved["petId"]
		if !ok {
			t.Fatal("petId missing from resolved params")
		}
		// Assert in-bounds
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err != nil {
			t.Fatalf("petId %q is not an integer: %v", v, err)
		}
		if n < 1 || n > 100 {
			t.Errorf("petId = %d, want in [1,100]", n)
		}
		seen[v] = true
	}

	// Assert values vary (with 50 samples and range 1-100, collision probability is very low)
	if len(seen) < 5 {
		t.Errorf("only %d distinct values in 50 samples — values are not varying", len(seen))
	}
}

// ============================================================
// RunLoad — max concurrency never exceeds cfg.Concurrency
// ============================================================

func TestRunLoad_RespectsMaxConcurrency(t *testing.T) {
	const concurrency = 5
	const totalRequests = 30

	// Arrange — test server tracks concurrent in-flight requests
	var inFlight atomic.Int64
	var maxObserved atomic.Int64

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := inFlight.Add(1)
		defer inFlight.Add(-1)
		// Update max observed atomically
		for {
			prev := maxObserved.Load()
			if cur <= prev {
				break
			}
			if maxObserved.CompareAndSwap(prev, cur) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond) // slow enough to observe concurrency
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	req := model.RequestDefinition{
		ID:          "r1",
		Name:        "r1",
		Method:      "GET",
		URLTemplate: srv.URL + "/test",
	}
	cfg := model.TestConfig{
		ID:            "c1",
		RequestID:     "r1",
		Mode:          model.ModeLoad,
		Concurrency:   concurrency,
		TotalRequests: totalRequests,
		Assertions:    model.Assertions{ExpectedStatus: []int{200}},
	}

	// Act
	run, err := engine.Run(context.Background(), req, cfg, model.AuthConfig{})

	// Assert
	if err != nil {
		t.Fatalf("RunLoad: %v", err)
	}
	if got := maxObserved.Load(); got > concurrency {
		t.Errorf("max concurrent = %d, want <= %d", got, concurrency)
	}
	if run.Summary.StatusCounts[200] != totalRequests {
		t.Errorf("status 200 count = %d, want %d", run.Summary.StatusCounts[200], totalRequests)
	}
}

// ============================================================
// RunBurst — requests arrive in pulses
// ============================================================

func TestRunBurst_DeliversPulses(t *testing.T) {
	const burstCount = 5
	const idleMs = 80
	const totalRequests = 15 // 3 full bursts

	// Arrange — record arrival times
	arrivalTimes := make(chan time.Time, totalRequests)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		arrivalTimes <- time.Now()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	req := model.RequestDefinition{
		ID:          "r2",
		Name:        "r2",
		Method:      "GET",
		URLTemplate: srv.URL + "/burst",
	}
	cfg := model.TestConfig{
		ID:            "c2",
		RequestID:     "r2",
		Mode:          model.ModeBurst,
		Concurrency:   burstCount,
		TotalRequests: totalRequests,
		BurstPattern:  &model.BurstPattern{Count: burstCount, Idle: idleMs},
	}

	// Act
	_, err := engine.Run(context.Background(), req, cfg, model.AuthConfig{})

	// Assert
	if err != nil {
		t.Fatalf("RunBurst: %v", err)
	}
	close(arrivalTimes)

	var times []time.Time
	for t := range arrivalTimes {
		times = append(times, t)
	}
	if len(times) != totalRequests {
		t.Fatalf("got %d arrivals, want %d", len(times), totalRequests)
	}

	// There should be at least 2 gaps >= idleMs between bursts (3 bursts → 2 inter-burst gaps)
	// Collect all inter-request gaps, find the largest two.
	gapsAboveIdle := 0
	slack := time.Duration(idleMs/2) * time.Millisecond
	threshold := time.Duration(idleMs)*time.Millisecond - slack
	for i := 1; i < len(times); i++ {
		if times[i].Sub(times[i-1]) >= threshold {
			gapsAboveIdle++
		}
	}
	if gapsAboveIdle < 2 {
		t.Errorf("found %d inter-burst gaps >= %v, want >= 2", gapsAboveIdle, threshold)
	}
}

// ============================================================
// Context cancellation — run stops early
// ============================================================

func TestRunLoad_StopsOnContextCancel(t *testing.T) {
	const totalRequests = 500

	// Arrange — slow server so in-flight requests keep the pool busy
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	req := model.RequestDefinition{
		ID:          "r3",
		Name:        "r3",
		Method:      "GET",
		URLTemplate: srv.URL + "/slow",
	}
	cfg := model.TestConfig{
		ID:            "c3",
		RequestID:     "r3",
		Mode:          model.ModeLoad,
		Concurrency:   10,
		TotalRequests: totalRequests,
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Act — cancel after a short delay
	go func() {
		time.Sleep(120 * time.Millisecond)
		cancel()
	}()
	run, _ := engine.Run(ctx, req, cfg, model.AuthConfig{})

	// Assert — far fewer than totalRequests should have completed
	total := 0
	for _, c := range run.Summary.StatusCounts {
		total += c
	}
	total += run.Summary.ErrorCount
	if total >= totalRequests {
		t.Errorf("run completed %d requests despite cancellation (want < %d)", total, totalRequests)
	}
}
