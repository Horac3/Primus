package engine_test

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Horac3/Primus/internal/engine"
	"github.com/Horac3/Primus/internal/model"
)

func countCfg(concurrency, total int) model.TestConfig {
	return model.TestConfig{ID: "cfg", Mode: model.ModeLoad, Concurrency: concurrency, TotalRequests: total}
}

func hasReason(s *model.LoginSummary, substr string) bool {
	for _, r := range s.FailureReasons {
		if strings.Contains(r.Cause, substr) {
			return true
		}
	}
	return false
}

// ---- ValidatePool ----------------------------------------------------------

func TestValidatePool_Rejects(t *testing.T) {
	f := newAuthFixture(t, 4)
	cases := []struct {
		name    string
		mutate  func(p *model.CredentialPool)
		cfg     model.TestConfig
		wantMsg string
	}{
		{"empty pool", func(p *model.CredentialPool) { p.Credentials = nil }, countCfg(1, 1), "no credentials"},
		{"missing login url", func(p *model.CredentialPool) { p.Login.URL = "" }, countCfg(1, 1), "login URL"},
		{"missing extraction path", func(p *model.CredentialPool) { p.Login.Extraction.Path = "" }, countCfg(1, 1), "extraction path"},
		{"bad extraction source", func(p *model.CredentialPool) { p.Login.Extraction.From = "cookie" }, countCfg(1, 1), "extraction source"},
		{"unknown body placeholder", func(p *model.CredentialPool) { p.Login.Body = `{"e":"{{email}}"}` }, countCfg(1, 1), "{{email}}"},
		{"unknown header placeholder", func(p *model.CredentialPool) { p.Login.Headers["X-Tenant"] = "{{tenant}}" }, countCfg(1, 1), "{{tenant}}"},
		{"apply format without token", func(p *model.CredentialPool) { p.Apply.Format = "Bearer {{username}}" }, countCfg(1, 1), "{{token}}"},
		{"pool smaller than concurrency", func(p *model.CredentialPool) {}, countCfg(5, 10), "allow credential reuse"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			pool := f.pool(4)
			tc.mutate(&pool)

			// Act
			err := engine.ValidatePool(tc.cfg, pool)

			// Assert
			if err == nil || !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("ValidatePool err = %v, want it to mention %q", err, tc.wantMsg)
			}
		})
	}
}

func TestValidatePool_ReuseFlagAllowsSmallPool(t *testing.T) {
	// Arrange
	f := newAuthFixture(t, 2)
	pool := f.pool(2)
	pool.AllowReuse = true

	// Act
	err := engine.ValidatePool(countCfg(5, 10), pool)

	// Assert
	if err != nil {
		t.Errorf("ValidatePool: %v, want nil with reuse allowed", err)
	}
}

// ---- login phase -----------------------------------------------------------

func TestRunWithPool_AllLoginsSucceed(t *testing.T) {
	// Arrange
	f := newAuthFixture(t, 3)
	pool := f.pool(3)

	// Act
	run, err := engine.RunWithPool(context.Background(), f.meRequest(), countCfg(3, 30), model.AuthConfig{}, &pool, nil)

	// Assert
	if err != nil {
		t.Fatalf("RunWithPool: %v", err)
	}
	if run.Login == nil || run.Login.Attempted != 3 || run.Login.Succeeded != 3 || run.Login.Failed != 0 {
		t.Fatalf("login = %+v, want 3 attempted, 3 succeeded", run.Login)
	}
	if run.Summary.StatusCounts[200] != 30 {
		t.Errorf("status counts = %v, want 30 x 200", run.Summary.StatusCounts)
	}
	if seen := f.snapshotSeen(); len(seen) != 3 {
		t.Errorf("users seen = %v, want all 3", seen)
	}
}

func TestRunWithPool_FailedLoginExcludesWorker(t *testing.T) {
	// Arrange: user01 has a wrong password.
	f := newAuthFixture(t, 3)
	pool := f.pool(3, 1)

	// Act
	run, err := engine.RunWithPool(context.Background(), f.meRequest(), countCfg(3, 30), model.AuthConfig{}, &pool, nil)

	// Assert
	if err != nil {
		t.Fatalf("RunWithPool: %v", err)
	}
	if run.Login.Succeeded != 2 || run.Login.Failed != 1 || !hasReason(run.Login, "HTTP 401") {
		t.Errorf("login = %+v, want 2 ok, 1 failed with HTTP 401", run.Login)
	}
	if run.Summary.TotalRequests != 30 || run.Summary.StatusCounts[200] != 30 {
		t.Errorf("summary = %+v, want 30 requests, all 200", run.Summary)
	}
	if seen := f.snapshotSeen(); seen["user01"] != 0 || len(seen) != 2 {
		t.Errorf("users seen = %v, want user00 and user02 only", seen)
	}
}

func TestRunWithPool_ZeroSuccessfulLoginsFailsWithoutDispatch(t *testing.T) {
	// Arrange
	f := newAuthFixture(t, 2)
	pool := f.pool(2, 0, 1)
	cfg := countCfg(2, 20)
	cfg.Assertions.MaxErrorRate = 0.5

	// Act
	run, err := engine.RunWithPool(context.Background(), f.meRequest(), cfg, model.AuthConfig{}, &pool, nil)

	// Assert
	if err != nil {
		t.Fatalf("RunWithPool: %v", err)
	}
	if run.Outcome != model.OutcomeFailNoLogins || run.Passed {
		t.Errorf("outcome = %q passed = %v, want %q and not passed", run.Outcome, run.Passed, model.OutcomeFailNoLogins)
	}
	if hits := f.meHits.Load(); hits != 0 {
		t.Errorf("protected endpoint hit %d times, want 0 (never run unauthenticated)", hits)
	}
}

func TestRunWithPool_PoolSmallerThanConcurrencyRejected(t *testing.T) {
	// Arrange
	f := newAuthFixture(t, 2)
	pool := f.pool(2)

	// Act
	_, err := engine.RunWithPool(context.Background(), f.meRequest(), countCfg(4, 10), model.AuthConfig{}, &pool, nil)

	// Assert
	if err == nil || f.loginHits.Load() != 0 {
		t.Errorf("err = %v with %d logins, want a validation error before any login", err, f.loginHits.Load())
	}
}

func TestRunWithPool_ReuseLogsEachCredentialInOnce(t *testing.T) {
	// Arrange
	f := newAuthFixture(t, 2)
	pool := f.pool(2)
	pool.AllowReuse = true

	// Act
	run, err := engine.RunWithPool(context.Background(), f.meRequest(), countCfg(4, 40), model.AuthConfig{}, &pool, nil)

	// Assert
	if err != nil {
		t.Fatalf("RunWithPool: %v", err)
	}
	if run.Login.Attempted != 2 || f.loginHits.Load() != 2 {
		t.Errorf("login attempted = %d, server logins = %d; want 2 each", run.Login.Attempted, f.loginHits.Load())
	}
	if run.Summary.StatusCounts[200] != 40 || len(f.snapshotSeen()) != 2 {
		t.Errorf("status = %v, seen = %v; want 40 x 200 across both users", run.Summary.StatusCounts, f.snapshotSeen())
	}
}

func TestRunWithPool_LoginConcurrencyIsBounded(t *testing.T) {
	// Arrange
	f := newAuthFixture(t, 8)
	f.loginDelay = 30 * time.Millisecond
	pool := f.pool(8)
	pool.LoginConcurrency = 2

	// Act
	_, err := engine.RunWithPool(context.Background(), f.meRequest(), countCfg(8, 8), model.AuthConfig{}, &pool, nil)

	// Assert
	if err != nil {
		t.Fatalf("RunWithPool: %v", err)
	}
	if max := f.loginMaxIn.Load(); max != 2 {
		t.Errorf("max concurrent logins = %d, want 2", max)
	}
}

func TestRunWithPool_LoginLatencyNotInRequestStats(t *testing.T) {
	// Arrange
	f := newAuthFixture(t, 2)
	f.loginDelay = 250 * time.Millisecond
	pool := f.pool(2)

	// Act
	run, err := engine.RunWithPool(context.Background(), f.meRequest(), countCfg(2, 20), model.AuthConfig{}, &pool, nil)

	// Assert
	if err != nil {
		t.Fatalf("RunWithPool: %v", err)
	}
	if run.Login.P50Millis < 250 {
		t.Errorf("login p50 = %dms, want >= 250ms", run.Login.P50Millis)
	}
	if run.Summary.P99Millis >= 250 || run.FinishedAt.Sub(run.StartedAt) >= 250*time.Millisecond {
		t.Errorf("request p99 = %dms, measured window = %s; login time leaked into request stats",
			run.Summary.P99Millis, run.FinishedAt.Sub(run.StartedAt))
	}
}

func TestRunWithPool_ReloginOn401CountedSeparately(t *testing.T) {
	cases := []struct {
		name         string
		relogin      bool
		wantRelogins int
		wantOK       bool
	}{
		{"enabled", true, 2, true},
		{"disabled", false, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange: every first-issued token is rejected by /me.
			f := newAuthFixture(t, 2)
			f.firstTokenExpired = true
			pool := f.pool(2)
			pool.ReloginOn401 = tc.relogin

			// Act
			run, err := engine.RunWithPool(context.Background(), f.meRequest(), countCfg(2, 40), model.AuthConfig{}, &pool, nil)

			// Assert
			if err != nil {
				t.Fatalf("RunWithPool: %v", err)
			}
			if run.Login.Attempted != 2 || run.Login.Relogins != tc.wantRelogins || run.Login.ReloginsSucceeded != tc.wantRelogins {
				t.Errorf("login = %+v, want 2 attempted and %d relogins (all succeeded)", run.Login, tc.wantRelogins)
			}
			if gotOK := run.Summary.StatusCounts[200] > 0; gotOK != tc.wantOK {
				t.Errorf("status counts = %v, want successes = %v", run.Summary.StatusCounts, tc.wantOK)
			}
		})
	}
}

func TestRunWithPool_SharedCredentialReloginsOnce(t *testing.T) {
	// Arrange: 2 credentials shared by 4 workers; the first token expires
	// and every new login revokes the user's older tokens.
	f := newAuthFixture(t, 2)
	f.firstTokenExpired = true
	f.revokeOnLogin = true
	pool := f.pool(2)
	pool.AllowReuse = true
	pool.ReloginOn401 = true

	// Act
	run, err := engine.RunWithPool(context.Background(), f.meRequest(), countCfg(4, 200), model.AuthConfig{}, &pool, nil)

	// Assert
	if err != nil {
		t.Fatalf("RunWithPool: %v", err)
	}
	for _, user := range []string{"user00", "user01"} {
		if n := f.loginsFor(user); n != 2 {
			t.Errorf("%s logged in %d times, want 2 (initial + one shared re-login)", user, n)
		}
	}
	if got := run.Summary.StatusCounts[401]; got > 4 {
		t.Errorf("401 count = %d, want at most one per worker before the shared re-login", got)
	}
}

func TestRunWithPool_DurationStartsAfterLogin(t *testing.T) {
	// Arrange
	f := newAuthFixture(t, 2)
	f.loginDelay = 300 * time.Millisecond
	pool := f.pool(2)
	cfg := model.TestConfig{ID: "cfg", Mode: model.ModeSoak, Concurrency: 2, Duration: 500 * time.Millisecond}

	// Act
	run, err := engine.RunWithPool(context.Background(), f.meRequest(), cfg, model.AuthConfig{}, &pool, nil)

	// Assert
	if err != nil {
		t.Fatalf("RunWithPool: %v", err)
	}
	window := run.FinishedAt.Sub(run.StartedAt)
	if window < 450*time.Millisecond || run.Summary.TotalRequests == 0 {
		t.Errorf("measured window = %s with %d requests; want ~500ms of dispatch after login",
			window, run.Summary.TotalRequests)
	}
}

func TestRunWithPool_CancelMidLoginSkipsUnstartedLogins(t *testing.T) {
	// Arrange: one login at a time, and the first one blocks.
	f := newAuthFixture(t, 4)
	f.loginBlock = true
	pool := f.pool(4)
	pool.LoginConcurrency = 1
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan model.Run, 1)
	go func() {
		run, _ := engine.RunWithPool(ctx, f.meRequest(), countCfg(4, 100), model.AuthConfig{}, &pool, nil)
		done <- run
	}()
	waitFor(t, func() bool { return f.loginHits.Load() == 1 })

	// Act
	cancel()
	run := <-done

	// Assert
	if hits := f.loginHits.Load(); hits != 1 {
		t.Errorf("login requests sent = %d, want 1 (no login starts after cancel)", hits)
	}
	if run.Login.Attempted != 4 || run.Login.Failed != 4 || !hasReason(run.Login, "canceled") {
		t.Errorf("login = %+v, want all 4 reported as canceled", run.Login)
	}
}

func TestRunWithPool_PoolTokenOverridesGlobalAuth(t *testing.T) {
	cases := []struct {
		name       string
		headers    map[string]string
		wantPrefix string
	}{
		{"no endpoint header uses pool token", nil, "Bearer tok-user"},
		{"[GLOBAL] sentinel uses pool token", map[string]string{"Authorization": model.HeaderGlobalSentinel}, "Bearer tok-user"},
		{"explicit endpoint override wins", map[string]string{"Authorization": "Bearer fixed-override"}, "Bearer fixed-override"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			f := newAuthFixture(t, 2)
			pool := f.pool(2)
			req := f.meRequest()
			req.Headers = tc.headers
			global := model.AuthConfig{Type: model.AuthTypeBearer, Token: "global-token"}

			// Act
			_, err := engine.RunWithPool(context.Background(), req, countCfg(2, 10), global, &pool, nil)

			// Assert
			if err != nil {
				t.Fatalf("RunWithPool: %v", err)
			}
			for header, n := range f.snapshotHeaders() {
				if !strings.HasPrefix(header, tc.wantPrefix) {
					t.Errorf("%d request(s) sent Authorization %q, want prefix %q", n, header, tc.wantPrefix)
				}
			}
		})
	}
}

func TestRunWithPool_CancelDuringLoginStopsCleanly(t *testing.T) {
	// Arrange
	f := newAuthFixture(t, 4)
	f.loginBlock = true
	pool := f.pool(4)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan model.Run, 1)

	// Act
	go func() {
		run, _ := engine.RunWithPool(ctx, f.meRequest(), countCfg(4, 100), model.AuthConfig{}, &pool, nil)
		done <- run
	}()
	waitFor(t, func() bool { return f.loginHits.Load() == 4 })
	cancel()

	// Assert
	select {
	case run := <-done:
		if run.Outcome != model.OutcomeFailNoLogins || !hasReason(run.Login, "canceled") {
			t.Errorf("outcome = %q, login = %+v; want fail_no_logins with canceled logins", run.Outcome, run.Login)
		}
		if f.meHits.Load() != 0 {
			t.Errorf("protected endpoint hit %d times after cancel during login", f.meHits.Load())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("RunWithPool did not return within 3s of cancel during login")
	}
}

func TestRunWithPool_NoGoroutineLeakAfterCancel(t *testing.T) {
	// Arrange
	baseline := runtime.NumGoroutine()
	f := newAuthFixture(t, 4)
	f.loginBlock = true
	pool := f.pool(4)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = engine.RunWithPool(ctx, f.meRequest(), countCfg(4, 100), model.AuthConfig{}, &pool, nil)
	}()
	waitFor(t, func() bool { return f.loginHits.Load() == 4 })

	// Act
	cancel()
	<-done
	f.srv.Close()

	// Assert
	deadline := time.Now().Add(3 * time.Second)
	for runtime.NumGoroutine() > baseline && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > baseline {
		buf := make([]byte, 1<<16)
		t.Errorf("goroutines = %d, baseline %d; leaked:\n%s", n, baseline, buf[:runtime.Stack(buf, true)])
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met within 3s")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
