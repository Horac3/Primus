package engine_test

import (
	"context"
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
		wantErr string
	}{
		{"probe with cap", model.ModeAuthProbe, 5, ""},
		{"probe without cap", model.ModeAuthProbe, 0, ""},
		{"negative", model.ModeAuthProbe, -1, "max_rate"},
		{"load with cap", model.ModeLoad, 5, "max_rate"},
		{"burst with cap", model.ModeBurst, 5, "max_rate"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := probeConfig(10, tc.rate)
			cfg.Mode = tc.mode
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

func TestRun_MaxRateSpacesAttempts(t *testing.T) {
	var arrivals atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		arrivals.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	req := model.RequestDefinition{ID: "req-probe", Method: http.MethodGet, URLTemplate: srv.URL}
	start := time.Now()
	run, err := engine.Run(context.Background(), req, probeConfig(6, 20), model.AuthConfig{})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := arrivals.Load(); got != 6 {
		t.Fatalf("target saw %d attempts, want 6", got)
	}
	if run.Summary.TotalRequests != 6 {
		t.Fatalf("summary total = %d, want 6", run.Summary.TotalRequests)
	}
	// 6 attempts at 20/s: releases at 0, 50, ..., 250ms. Allow timer slack.
	if min := 240 * time.Millisecond; elapsed < min {
		t.Fatalf("run took %s, want at least %s at max_rate 20", elapsed, min)
	}
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
	if run.Summary.TotalRequests >= 5 {
		t.Fatalf("summary total = %d, want fewer than 5 after an early stop", run.Summary.TotalRequests)
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
	cfg := probeConfig(0, 2) // releases at 0 and 500ms
	cfg.Duration = 300 * time.Millisecond
	if _, err := engine.Run(context.Background(), req, cfg, model.AuthConfig{}); err != nil {
		t.Fatalf("Run: %v", err)
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
