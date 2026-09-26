package web_test

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Horac3/Primus/internal/model"
	"github.com/Horac3/Primus/internal/store"
	"github.com/Horac3/Primus/internal/web"
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

// minimalSpec returns a tiny OpenAPI 3.0 JSON spec as a string.
func minimalSpec() string {
	return `{
		"openapi":"3.0.0",
		"info":{"title":"T","version":"1"},
		"paths":{
			"/health":{
				"get":{
					"operationId":"getHealth",
					"responses":{"200":{"description":"ok"}}
				}
			}
		}
	}`
}

// ============================================================
// POST /import — valid spec URL
// ============================================================

func TestImportHandler_ValidURL_PersistsDefinitions(t *testing.T) {
	// Arrange — stand up a spec server
	specSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(minimalSpec()))
	}))
	defer specSrv.Close()

	db := newTestDB(t)
	mux := web.NewServerWithTemplateDir(db, "../../web/templates")

	form := url.Values{"spec_url": {specSrv.URL + "/spec.json"}}
	req := httptest.NewRequest(http.MethodPost, "/import", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	// Act
	mux.ServeHTTP(rr, req)

	// Assert — non-error response
	if rr.Code >= 500 {
		t.Errorf("status = %d, want < 500; body: %s", rr.Code, rr.Body.String())
	}
	// Definitions must be persisted
	defs, err := store.ListRequestDefinitions(db)
	if err != nil {
		t.Fatalf("ListRequestDefinitions: %v", err)
	}
	if len(defs) == 0 {
		t.Error("expected at least 1 RequestDefinition persisted, got 0")
	}
}

// ============================================================
// POST /import — malformed URL returns clear error
// ============================================================

func TestImportHandler_MalformedURL_Returns4xx(t *testing.T) {
	// Arrange
	db := newTestDB(t)
	mux := web.NewServerWithTemplateDir(db, "../../web/templates")

	form := url.Values{"spec_url": {"not-a-valid-url-%%"}}
	req := httptest.NewRequest(http.MethodPost, "/import", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	// Act
	mux.ServeHTTP(rr, req)

	// Assert — should get a 4xx, not a 500
	if rr.Code == http.StatusOK {
		t.Errorf("expected error status, got 200")
	}
	if rr.Code >= 500 {
		t.Errorf("expected 4xx, got %d; body: %s", rr.Code, rr.Body.String())
	}
}

// ============================================================
// POST /runs — valid config persists Run with correct Passed
// ============================================================

func TestRunsHandler_ValidConfig_PersistsRun(t *testing.T) {
	// Arrange — target server that always responds 200
	targetSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer targetSrv.Close()

	db := newTestDB(t)
	// Pre-save a request definition pointing at the test server
	reqDef := model.RequestDefinition{
		ID:          "req-web-1",
		Name:        "Health",
		Method:      "GET",
		URLTemplate: targetSrv.URL + "/health",
	}
	if err := store.SaveRequestDefinition(db, reqDef); err != nil {
		t.Fatalf("SaveRequestDefinition: %v", err)
	}

	mux := web.NewServerWithTemplateDir(db, "../../web/templates")

	form := url.Values{
		"request_id":      {"req-web-1"},
		"mode":            {"load"},
		"concurrency":     {"2"},
		"total_requests":  {"4"},
		"expected_status": {"200"},
	}
	req := httptest.NewRequest(http.MethodPost, "/runs", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	// Act
	mux.ServeHTTP(rr, req)

	// Assert — non-error response
	if rr.Code >= 500 {
		t.Errorf("status = %d, want < 500; body: %s", rr.Code, rr.Body.String())
	}
	// Run must be persisted and passed (all 200s, assertion is 200)
	runs, err := store.ListAllRuns(db)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(runs) == 0 {
		t.Fatal("expected at least 1 Run persisted")
	}
	if !runs[0].Passed {
		t.Errorf("run.Passed = false, want true (all responses were 200)")
	}
}

// ============================================================
// GET /runs/{id} — 404 for unknown, 200 for known
// ============================================================

func TestGetRunHandler_UnknownID_Returns404(t *testing.T) {
	// Arrange
	db := newTestDB(t)
	mux := web.NewServerWithTemplateDir(db, "../../web/templates")

	req := httptest.NewRequest(http.MethodGet, "/runs/does-not-exist", nil)
	rr := httptest.NewRecorder()

	// Act
	mux.ServeHTTP(rr, req)

	// Assert
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

func TestGetRunHandler_KnownID_Returns200(t *testing.T) {
	// Arrange
	db := newTestDB(t)
	now := time.Now().UTC()
	run := model.Run{
		ID:           "run-known",
		TestConfigID: "cfg-x",
		StartedAt:    now,
		FinishedAt:   now.Add(time.Second),
		Summary:      model.RunSummary{StatusCounts: map[int]int{200: 10}},
		Passed:       true,
	}
	if err := store.SaveRun(db, run); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}

	mux := web.NewServerWithTemplateDir(db, "../../web/templates")
	req := httptest.NewRequest(http.MethodGet, "/runs/run-known", nil)
	rr := httptest.NewRecorder()

	// Act
	mux.ServeHTTP(rr, req)

	// Assert
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "run-known") {
		t.Errorf("response body does not contain run ID; body: %s", rr.Body.String())
	}
}
