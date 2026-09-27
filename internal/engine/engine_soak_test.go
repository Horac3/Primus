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
// Global auth — applied at dispatch, verified by the server
// ============================================================

func TestProbe_AppliesBearerAuth(t *testing.T) {
	// Arrange — server asserts on the header it received
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer tok-123" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	def := model.RequestDefinition{
		ID: "a1", Name: "a1", Method: "GET", URLTemplate: srv.URL + "/x",
	}
	auth := model.AuthConfig{Type: model.AuthTypeBearer, Token: "tok-123"}

	// Act
	result, err := engine.Probe(context.Background(), def, auth)

	// Assert — 200 proves the bearer header was sent
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if result.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, want 200 (bearer header missing?)", result.StatusCode)
	}
}

func TestProbe_AppliesAPIKeyAuth(t *testing.T) {
	// Arrange
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-API-Key"); got != "key-abc" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	def := model.RequestDefinition{
		ID: "a2", Name: "a2", Method: "GET", URLTemplate: srv.URL + "/x",
	}
	auth := model.AuthConfig{Type: model.AuthTypeAPIKey, Header: "X-API-Key", Value: "key-abc"}

	// Act
	result, err := engine.Probe(context.Background(), def, auth)

	// Assert
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if result.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, want 200 (api-key header missing?)", result.StatusCode)
	}
}

func TestProbe_AppliesBasicAuth(t *testing.T) {
	// Arrange
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != "user" || p != "s3cret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	def := model.RequestDefinition{
		ID: "a3", Name: "a3", Method: "GET", URLTemplate: srv.URL + "/x",
	}
	auth := model.AuthConfig{Type: model.AuthTypeBasic, Username: "user", Password: "s3cret"}

	// Act
	result, err := engine.Probe(context.Background(), def, auth)

	// Assert
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if result.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, want 200 (basic auth missing?)", result.StatusCode)
	}
}

func TestProbe_NoAuth_SendsNoAuthHeader(t *testing.T) {
	// Arrange — server fails if ANY auth header is present
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	def := model.RequestDefinition{
		ID: "a4", Name: "a4", Method: "GET", URLTemplate: srv.URL + "/x",
	}

	// Act
	result, err := engine.Probe(context.Background(), def, model.AuthConfig{})

	// Assert
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if result.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, want 200 (unexpected auth header?)", result.StatusCode)
	}
}

func TestRunLoad_AppliesGlobalAuthToEveryRequest(t *testing.T) {
	// Arrange — counts how many hits carried the bearer header
	var authed, total int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		total++
		if r.Header.Get("Authorization") == "Bearer shared-tok" {
			authed++
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	def := model.RequestDefinition{
		ID: "a5", Name: "a5", Method: "GET", URLTemplate: srv.URL + "/x",
	}
	cfg := model.TestConfig{
		ID: "cfg-a5", RequestID: "a5", Mode: model.ModeLoad,
		Concurrency: 2, TotalRequests: 6,
		Assertions: model.Assertions{ExpectedStatus: []int{200}},
	}
	auth := model.AuthConfig{Type: model.AuthTypeBearer, Token: "shared-tok"}

	// Act
	_, err := engine.RunLoad(context.Background(), def, cfg, auth)

	// Assert — same auth on every request without re-entering it per endpoint
	if err != nil {
		t.Fatalf("RunLoad: %v", err)
	}
	if total != 6 {
		t.Fatalf("total = %d, want 6", total)
	}
	if authed != total {
		t.Errorf("authed = %d, want %d (global auth must apply to every request)", authed, total)
	}
}

// ============================================================
// RunSoak — deadline-based dispatch, not fixed count
// ============================================================

func TestRunSoak_StopsAtDeadline(t *testing.T) {
	// Arrange — fast target, short deadline
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	def := model.RequestDefinition{
		ID: "s1", Name: "s1", Method: "GET", URLTemplate: srv.URL + "/x",
	}
	cfg := model.TestConfig{
		ID: "cfg-s1", RequestID: "s1", Mode: model.ModeSoak,
		Concurrency: 2, Duration: 2 * time.Second,
		Assertions: model.Assertions{ExpectedStatus: []int{200}},
	}

	// Act
	start := time.Now()
	run, err := engine.RunSoak(context.Background(), def, cfg, model.AuthConfig{})
	elapsed := time.Since(start)

	// Assert — stops at approximately the deadline, not a fixed count
	if err != nil {
		t.Fatalf("RunSoak: %v", err)
	}
	if elapsed < 1500*time.Millisecond || elapsed > 15*time.Second {
		t.Errorf("elapsed = %v, want ≈2s deadline", elapsed)
	}
	if len(run.Summary.StatusCounts) == 0 {
		t.Error("no requests recorded during soak")
	}
	if run.Outcome != model.OutcomePass {
		t.Errorf("Outcome = %q, want %q", run.Outcome, model.OutcomePass)
	}
	if !run.Passed {
		t.Error("Passed = false, want true when assertions met")
	}
}
