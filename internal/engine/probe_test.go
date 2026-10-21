package engine_test

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Horac3/Primus/internal/engine"
	"github.com/Horac3/Primus/internal/model"
)

// ============================================================
// Auth probe: max_rate ceiling (FR-PROBE-02)
// ============================================================

func probeConfig(total int, maxRate float64) model.TestConfig {
	return model.TestConfig{
		ID:            "cfg-probe",
		RequestID:     "req-probe",
		Mode:          model.ModeAuthProbe,
		Concurrency:   4,
		TotalRequests: total,
		MaxRate:       maxRate,
	}
}

func TestValidateTestConfig_MaxRate(t *testing.T) {
	cases := []struct {
		name    string
		mode    model.Mode
		rate    float64
		burst   *model.BurstPattern
		wantErr string
	}{
		{"probe with cap", model.ModeAuthProbe, 5, nil, ""},
		{"probe without cap", model.ModeAuthProbe, 0, nil, ""},
		{"smallest cap", model.ModeAuthProbe, 0.001, nil, ""},
		{"negative", model.ModeAuthProbe, -1, nil, "max_rate"},
		{"below minimum", model.ModeAuthProbe, 1e-12, nil, "max_rate"},
		{"NaN", model.ModeAuthProbe, math.NaN(), nil, "max_rate"},
		{"infinite", model.ModeAuthProbe, math.Inf(1), nil, "max_rate"},
		{"load with cap", model.ModeLoad, 5, nil, "max_rate"},
		{"burst with cap", model.ModeBurst, 5, nil, "max_rate"},
		{"probe with cap and burst pattern", model.ModeAuthProbe, 5, &model.BurstPattern{Count: 2, Idle: 10}, "burst"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := probeConfig(10, tc.rate)
			cfg.Mode = tc.mode
			cfg.BurstPattern = tc.burst
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

// arrivalTarget records when each request arrives. The first hold
// requests sleep for holdFor, simulating a slow target.
type arrivalTarget struct {
	srv   *httptest.Server
	mu    sync.Mutex
	times []time.Time
}

func newArrivalTarget(t *testing.T, hold int, holdFor time.Duration) *arrivalTarget {
	t.Helper()
	at := &arrivalTarget{}
	at.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		at.mu.Lock()
		at.times = append(at.times, time.Now())
		n := len(at.times)
		at.mu.Unlock()
		if n <= hold {
			time.Sleep(holdFor)
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(at.srv.Close)
	return at
}

func (at *arrivalTarget) arrivals() []time.Time {
	at.mu.Lock()
	defer at.mu.Unlock()
	return append([]time.Time(nil), at.times...)
}

// assertMinGap fails when two consecutive arrivals are closer than min.
func assertMinGap(t *testing.T, times []time.Time, min time.Duration) {
	t.Helper()
	for i := 1; i < len(times); i++ {
		if gap := times[i].Sub(times[i-1]); gap < min {
			t.Errorf("attempts %d and %d arrived %s apart, want at least %s", i, i+1, gap, min)
		}
	}
}

func TestRun_MaxRateSpacesAttempts(t *testing.T) {
	target := newArrivalTarget(t, 0, 0)
	req := model.RequestDefinition{ID: "req-probe", Method: http.MethodGet, URLTemplate: target.srv.URL}

	start := time.Now()
	run, err := engine.Run(context.Background(), req, probeConfig(6, 20), model.AuthConfig{})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.Summary.TotalRequests != 6 || len(target.arrivals()) != 6 {
		t.Fatalf("summary total %d, target saw %d; want 6 and 6", run.Summary.TotalRequests, len(target.arrivals()))
	}
	// 20/s is one release per 50ms; allow 10ms of dispatch jitter.
	assertMinGap(t, target.arrivals(), 40*time.Millisecond)
	if elapsed > 2*time.Second {
		t.Errorf("run took %s; pacing should not wait much past 250ms", elapsed)
	}
}

func TestRun_MaxRateNoCatchUpBurstAfterSlowResponses(t *testing.T) {
	// Both workers are held 300ms by the first two attempts, so the feeder
	// falls behind schedule. The cap must still hold once they free up.
	target := newArrivalTarget(t, 2, 300*time.Millisecond)
	req := model.RequestDefinition{ID: "req-probe", Method: http.MethodGet, URLTemplate: target.srv.URL}
	cfg := probeConfig(6, 20)
	cfg.Concurrency = 2

	if _, err := engine.Run(context.Background(), req, cfg, model.AuthConfig{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if n := len(target.arrivals()); n != 6 {
		t.Fatalf("target saw %d attempts, want 6", n)
	}
	assertMinGap(t, target.arrivals(), 40*time.Millisecond)
}

func TestRun_MaxRateStopIsPrompt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	req := model.RequestDefinition{ID: "req-probe", Method: http.MethodGet, URLTemplate: srv.URL}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var run model.Run
	go func() {
		defer close(done)
		// One attempt per second: after the first, the feeder waits ~1s.
		run, _ = engine.Run(ctx, req, probeConfig(5, 1), model.AuthConfig{})
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("run did not stop within 500ms of cancel during a pacing wait")
	}
	if run.Summary.TotalRequests != 1 || !run.Stopped {
		t.Fatalf("summary total = %d, stopped = %v; want 1 attempt and a stopped run", run.Summary.TotalRequests, run.Stopped)
	}
}

func TestRun_MaxRateDurationStopsAtDeadline(t *testing.T) {
	var arrivals atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		arrivals.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	req := model.RequestDefinition{ID: "req-probe", Method: http.MethodGet, URLTemplate: srv.URL}
	cfg := probeConfig(0, 0.5) // releases at 0 and 2s
	cfg.Duration = 300 * time.Millisecond
	start := time.Now()
	if _, err := engine.Run(context.Background(), req, cfg, model.AuthConfig{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 1500*time.Millisecond {
		t.Errorf("run took %s; the deadline should end the pacing wait at ~300ms", elapsed)
	}
	if got := arrivals.Load(); got != 1 {
		t.Fatalf("target saw %d attempts, want 1: the second release falls after the deadline", got)
	}
}

// ============================================================
// Auth probe: auth_source picks the identity (FR-PROBE-03)
// ============================================================

func TestResolveAuth(t *testing.T) {
	global := model.AuthConfig{Type: model.AuthTypeBearer, Token: "global-token"}
	pool := &model.CredentialPool{ID: "pool-1", Name: "users"}
	cases := []struct {
		name       string
		source     model.AuthSource
		pool       *model.CredentialPool
		wantAuth   model.AuthType
		wantPool   bool
		wantErrSub string
	}{
		{"default with pool", model.AuthSourceDefault, pool, model.AuthTypeBearer, true, ""},
		{"default without pool", model.AuthSourceDefault, nil, model.AuthTypeBearer, false, ""},
		{"global ignores pool", model.AuthSourceGlobal, pool, model.AuthTypeBearer, false, ""},
		{"global without pool", model.AuthSourceGlobal, nil, model.AuthTypeBearer, false, ""},
		{"pool with pool", model.AuthSourcePool, pool, model.AuthTypeBearer, true, ""},
		{"pool without pool", model.AuthSourcePool, nil, "", false, "credential pool"},
		{"none with pool", model.AuthSourceNone, pool, "", false, ""},
		{"none without pool", model.AuthSourceNone, nil, "", false, ""},
		{"unknown", model.AuthSource("admin"), nil, "", false, "auth_source"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := probeConfig(1, 0)
			cfg.AuthSource = tc.source
			auth, gotPool, err := engine.ResolveAuth(cfg, global, tc.pool)
			if tc.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErrSub) {
					t.Fatalf("err = %v, want one mentioning %q", err, tc.wantErrSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if auth.Type != tc.wantAuth {
				t.Errorf("auth type = %q, want %q", auth.Type, tc.wantAuth)
			}
			if tc.wantAuth == "" && auth.Token != "" {
				t.Errorf("auth carries a token for source %q", tc.source)
			}
			if (gotPool != nil) != tc.wantPool {
				t.Errorf("pool returned = %v, want present=%v", gotPool != nil, tc.wantPool)
			}
		})
	}
}

func TestRun_AuthSourceNoneSendsNoGlobalAuth(t *testing.T) {
	var sawAuth atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			sawAuth.Add(1)
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	req := model.RequestDefinition{
		ID: "req-probe", Method: http.MethodGet, URLTemplate: srv.URL,
		Headers: map[string]string{"Authorization": model.HeaderGlobalSentinel},
	}
	cfg := probeConfig(3, 0)
	cfg.AuthSource = model.AuthSourceNone
	auth, pool, err := engine.ResolveAuth(cfg, model.AuthConfig{Type: model.AuthTypeBearer, Token: "global-token"}, nil)
	if err != nil {
		t.Fatalf("ResolveAuth: %v", err)
	}
	if _, err := engine.RunWithPool(context.Background(), req, cfg, auth, pool, nil); err != nil {
		t.Fatalf("RunWithPool: %v", err)
	}
	if n := sawAuth.Load(); n != 0 {
		t.Fatalf("target saw an Authorization header on %d attempts, want none", n)
	}
}
