package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Horac3/Primus/internal/model"
	"github.com/Horac3/Primus/internal/store"
)

// ---- helpers ---------------------------------------------------------------

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := store.OpenDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	if err := store.Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func writeConfigFile(t *testing.T, cfg model.TestConfig) string {
	t.Helper()
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return p
}

// ============================================================
// Exit 0 — valid config, passing run
// ============================================================

func TestRunCmd_PassingRun_ExitsZero(t *testing.T) {
	// Arrange — fast httptest target that always returns 200
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	db := newTestDB(t)
	reqDef := model.RequestDefinition{
		ID: "req-cli-1", Name: "health",
		Method: "GET", URLTemplate: srv.URL + "/health",
	}
	if err := store.SaveRequestDefinition(db, reqDef); err != nil {
		t.Fatalf("SaveRequestDefinition: %v", err)
	}

	cfg := model.TestConfig{
		ID:            "cfg-cli-1",
		RequestID:     "req-cli-1",
		Mode:          model.ModeLoad,
		Concurrency:   2,
		TotalRequests: 4,
		Assertions:    model.Assertions{ExpectedStatus: []int{200}},
	}
	cfgPath := writeConfigFile(t, cfg)

	var out bytes.Buffer

	// Act
	code := runCmd(context.Background(), []string{cfgPath}, &out, db)

	// Assert — exit 0
	if code != 0 {
		t.Errorf("exit code = %d, want 0; output: %s", code, out.String())
	}
	// Output must be valid JSON with a "passed" field = true
	var summary map[string]any
	if err := json.Unmarshal(out.Bytes(), &summary); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, out.String())
	}
	passed, ok := summary["passed"].(bool)
	if !ok || !passed {
		t.Errorf("summary[passed] = %v, want true", summary["passed"])
	}
}

// ============================================================
// Exit 1 — valid config, failing assertions
// ============================================================

func TestRunCmd_FailingAssertions_ExitsOne(t *testing.T) {
	// Arrange — server returns 500
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	db := newTestDB(t)
	reqDef := model.RequestDefinition{
		ID: "req-cli-2", Name: "broken",
		Method: "GET", URLTemplate: srv.URL + "/broken",
	}
	if err := store.SaveRequestDefinition(db, reqDef); err != nil {
		t.Fatalf("SaveRequestDefinition: %v", err)
	}

	cfg := model.TestConfig{
		ID:            "cfg-cli-2",
		RequestID:     "req-cli-2",
		Mode:          model.ModeLoad,
		Concurrency:   2,
		TotalRequests: 4,
		Assertions:    model.Assertions{ExpectedStatus: []int{200}}, // expects 200, gets 500
	}
	cfgPath := writeConfigFile(t, cfg)

	var out bytes.Buffer

	// Act
	code := runCmd(context.Background(), []string{cfgPath}, &out, db)

	// Assert — exit 1
	if code != 1 {
		t.Errorf("exit code = %d, want 1; output: %s", code, out.String())
	}
	var summary map[string]any
	if err := json.Unmarshal(out.Bytes(), &summary); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, out.String())
	}
	if passed, _ := summary["passed"].(bool); passed {
		t.Errorf("summary[passed] = true, want false")
	}
}

// ============================================================
// Exit 2 — missing config file
// ============================================================

func TestRunCmd_MissingConfigFile_ExitsTwo(t *testing.T) {
	// Arrange
	db := newTestDB(t)
	var out bytes.Buffer

	// Act
	code := runCmd(context.Background(), []string{"/nonexistent/config.json"}, &out, db)

	// Assert
	if code != 2 {
		t.Errorf("exit code = %d, want 2; output: %s", code, out.String())
	}
}

// ============================================================
// Exit 2 — unknown request ID
// ============================================================

func TestRunCmd_UnknownRequestID_ExitsTwo(t *testing.T) {
	// Arrange — config references a request that isn't in the DB
	db := newTestDB(t)
	cfg := model.TestConfig{
		ID:            "cfg-cli-x",
		RequestID:     "req-does-not-exist",
		Mode:          model.ModeLoad,
		Concurrency:   1,
		TotalRequests: 1,
	}
	cfgPath := writeConfigFile(t, cfg)
	var out bytes.Buffer

	// Act
	code := runCmd(context.Background(), []string{cfgPath}, &out, db)

	// Assert
	if code != 2 {
		t.Errorf("exit code = %d, want 2; output: %s", code, out.String())
	}
}
