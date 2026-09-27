package web_test

import (
	"database/sql"
	"encoding/json"
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

// ============================================================
// POST /probe — success path
// ============================================================

func TestProbeHandler_SuccessfulTarget_ReturnsStatusAndOK(t *testing.T) {
	// Arrange
	targetSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer targetSrv.Close()

	db := newTestDB(t)
	mux := web.NewServer(db)

	form := url.Values{
		"method": {"GET"},
		"url":    {targetSrv.URL + "/ok"},
	}
	req := httptest.NewRequest(http.MethodPost, "/probe", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	// Act
	mux.ServeHTTP(rr, req)

	// Assert
	if rr.Code >= 500 {
		t.Fatalf("status = %d, want < 500; body: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "200") {
		t.Errorf("response body does not contain status code 200; body: %s", body)
	}
}

// ============================================================
// POST /probe — unreachable target returns clear failure, not 500
// ============================================================

func TestProbeHandler_UnreachableTarget_Returns2xxWithFailureState(t *testing.T) {
	// Arrange — closed server
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	badURL := srv.URL + "/gone"
	srv.Close()

	db := newTestDB(t)
	mux := web.NewServer(db)

	form := url.Values{"method": {"GET"}, "url": {badURL}}
	req := httptest.NewRequest(http.MethodPost, "/probe", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	// Act
	mux.ServeHTTP(rr, req)

	// Assert — must return a renderable fragment (2xx), not a 500
	if rr.Code >= 500 {
		t.Errorf("status = %d, want 2xx for unreachable target; body: %s", rr.Code, rr.Body.String())
	}
	// Fragment must indicate failure
	body := rr.Body.String()
	if !strings.Contains(strings.ToLower(body), "fail") && !strings.Contains(strings.ToLower(body), "error") {
		t.Errorf("response does not indicate failure; body: %s", body)
	}
}

// ============================================================
// Async runs helpers
// ============================================================

// postRuns submits a run form, expects 202 + a running fragment, and
// returns the registry run id from data-run-id.
func postRuns(t *testing.T, mux http.Handler, form url.Values) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/runs", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("POST /runs status = %d, want 202; body: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	const marker = `data-run-id="`
	i := strings.Index(body, marker)
	if i < 0 {
		t.Fatalf("running fragment missing data-run-id; body: %s", body)
	}
	rest := body[i+len(marker):]
	j := strings.Index(rest, `"`)
	if j < 0 {
		t.Fatalf("malformed data-run-id; body: %s", body)
	}
	return rest[:j]
}

// pollDone polls GET /runs/status/{id} until the result fragment renders.
func pollDone(t *testing.T, mux http.Handler, id string) string {
	t.Helper()
	var body string
	for i := 0; i < 100; i++ {
		time.Sleep(50 * time.Millisecond)
		req := httptest.NewRequest(http.MethodGet, "/runs/status/"+id, nil)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if rr.Code == http.StatusNotFound {
			continue
		}
		body = rr.Body.String()
		if strings.Contains(body, "badge-") {
			return body
		}
	}
	t.Fatalf("run %s never finished; last body: %s", id, body)
	return ""
}

// ============================================================
// POST /runs — 100% failing requests + zero assertions → Passed=false
// ============================================================

func TestRunsHandler_AllFailed_ZeroAssertions_RunPassedFalse(t *testing.T) {
	// Arrange — server always returns 500
	targetSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer targetSrv.Close()

	db := newTestDB(t)
	mux := web.NewServer(db)

	form := url.Values{
		"method":         {"GET"},
		"url":            {targetSrv.URL + "/fail"},
		"mode":           {"load"},
		"concurrency":    {"2"},
		"total_requests": {"4"},
		// no expected_status, no assertions
	}

	// Act — start async, poll to completion
	id := postRuns(t, mux, form)
	pollDone(t, mux, id)

	// Assert — persisted run is never a green PASS
	runs, err := store.ListAllRuns(db)
	if err != nil {
		t.Fatalf("ListAllRuns: %v", err)
	}
	if len(runs) == 0 {
		t.Fatal("no runs persisted")
	}
	r := runs[0]
	if r.Passed {
		t.Errorf("Passed = true, want false (no assertions configured = never a green PASS)")
	}
	if r.Outcome == model.OutcomePass {
		t.Errorf("Outcome = %q, want anything but Pass when no assertions configured", r.Outcome)
	}
}

// ============================================================
// POST /runs — zero assertions, partial success → OutcomeNoAssertions
// ============================================================

func TestRunsHandler_ZeroAssertions_SomeSuccess_OutcomeNoAssertions(t *testing.T) {
	// Arrange — server returns 200
	targetSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer targetSrv.Close()

	db := newTestDB(t)
	mux := web.NewServer(db)

	form := url.Values{
		"method":         {"GET"},
		"url":            {targetSrv.URL + "/ok"},
		"mode":           {"load"},
		"concurrency":    {"2"},
		"total_requests": {"4"},
	}

	// Act
	id := postRuns(t, mux, form)
	pollDone(t, mux, id)

	// Assert
	runs, _ := store.ListAllRuns(db)
	if len(runs) == 0 {
		t.Fatal("no runs persisted")
	}
	r := runs[0]
	if r.Outcome != model.OutcomeNoAssertions {
		t.Errorf("Outcome = %q, want %q", r.Outcome, model.OutcomeNoAssertions)
	}
	if r.Passed {
		t.Errorf("Passed = true, want false for no-assertions outcome")
	}
}

// ============================================================
// POST /runs — configured assertions met → OutcomePass + checklist
// ============================================================

func TestRunsHandler_AssertionsMet_OutcomePass(t *testing.T) {
	// Arrange
	targetSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer targetSrv.Close()

	db := newTestDB(t)
	mux := web.NewServer(db)

	form := url.Values{
		"method":          {"GET"},
		"url":             {targetSrv.URL + "/ok"},
		"mode":            {"load"},
		"concurrency":     {"2"},
		"total_requests":  {"4"},
		"expected_status": {"200"},
	}

	// Act
	id := postRuns(t, mux, form)
	body := pollDone(t, mux, id)

	// Assert — result fragment carries the per-assertion checklist
	if !strings.Contains(body, "Status code: 200") {
		t.Errorf("result missing assertion checklist; body: %s", body)
	}
	runs, _ := store.ListAllRuns(db)
	if len(runs) == 0 {
		t.Fatal("no runs persisted")
	}
	r := runs[0]
	if r.Outcome != model.OutcomePass {
		t.Errorf("Outcome = %q, want %q", r.Outcome, model.OutcomePass)
	}
	if !r.Passed {
		t.Errorf("Passed = false, want true when assertions met")
	}
	if len(r.AssertionResults) != 1 || !r.AssertionResults[0].Passed {
		t.Errorf("AssertionResults = %+v, want one passing entry", r.AssertionResults)
	}
}

// ============================================================
// POST /runs — soak mode dispatches a deadline run, persists it
// ============================================================

func TestRunsHandler_SoakMode_PersistsRun(t *testing.T) {
	// Arrange
	targetSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer targetSrv.Close()

	db := newTestDB(t)
	mux := web.NewServer(db)

	form := url.Values{
		"method":          {"GET"},
		"url":             {targetSrv.URL + "/soak"},
		"mode":            {"soak"},
		"concurrency":     {"2"},
		"soak_seconds":    {"1"},
		"expected_status": {"200"},
	}

	// Act
	id := postRuns(t, mux, form)
	pollDone(t, mux, id)

	// Assert
	runs, err := store.ListAllRuns(db)
	if err != nil {
		t.Fatalf("ListAllRuns: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(runs))
	}
	if runs[0].Outcome != model.OutcomePass || !runs[0].Passed {
		t.Errorf("Outcome = %q Passed = %v, want pass/true", runs[0].Outcome, runs[0].Passed)
	}
	if len(runs[0].Summary.StatusCounts) == 0 {
		t.Error("soak run recorded no requests")
	}
}

// ============================================================
// POST /runs/{id}/stop — cancels mid-run, partial result persists
// ============================================================

func TestRunsHandler_Stop_CancelsMidRun(t *testing.T) {
	// Arrange — slow target so the run outlives the test's stop call
	targetSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer targetSrv.Close()

	db := newTestDB(t)
	mux := web.NewServer(db)

	form := url.Values{
		"method":          {"GET"},
		"url":             {targetSrv.URL + "/slow"},
		"mode":            {"load"},
		"concurrency":     {"1"},
		"total_requests":  {"20"},
		"expected_status": {"200"},
	}

	// Act — start, stop promptly, poll to the (partial) finish
	id := postRuns(t, mux, form)
	stopReq := httptest.NewRequest(http.MethodPost, "/runs/"+id+"/stop", nil)
	stopRR := httptest.NewRecorder()
	mux.ServeHTTP(stopRR, stopReq)
	if stopRR.Code != http.StatusOK {
		t.Fatalf("stop status = %d, want 200; body: %s", stopRR.Code, stopRR.Body.String())
	}
	pollDone(t, mux, id)

	// Assert — partial result persisted, far short of the 20 requested
	runs, _ := store.ListAllRuns(db)
	if len(runs) == 0 {
		t.Fatal("no runs persisted")
	}
	total := 0
	for _, c := range runs[0].Summary.StatusCounts {
		total += c
	}
	total += runs[0].Summary.ErrorCount
	if total >= 20 {
		t.Errorf("recorded = %d, want partial (< 20) after Stop", total)
	}
}

// ============================================================
// POST /runs — invalid soak duration → 400, nothing started
// ============================================================

func TestRunsHandler_InvalidSoakDuration_Returns400(t *testing.T) {
	// Arrange
	db := newTestDB(t)
	mux := web.NewServer(db)

	form := url.Values{
		"method":       {"GET"},
		"url":          {"http://x.invalid/"},
		"mode":         {"soak"},
		"concurrency":  {"1"},
		"soak_seconds": {"0"},
	}
	req := httptest.NewRequest(http.MethodPost, "/runs", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	// Act
	mux.ServeHTTP(rr, req)

	// Assert
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
	runs, _ := store.ListAllRuns(db)
	if len(runs) != 0 {
		t.Errorf("runs = %d, want 0 (invalid config must not start)", len(runs))
	}
}

// ============================================================
// GET /runs/{id} — 404 unknown, 200 known
// ============================================================

func TestGetRunHandler_UnknownID_Returns404(t *testing.T) {
	// Arrange
	db := newTestDB(t)
	mux := web.NewServer(db)

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
		Outcome:      model.OutcomePass,
		Passed:       true,
	}
	if err := store.SaveRun(db, run); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}

	mux := web.NewServer(db)
	req := httptest.NewRequest(http.MethodGet, "/runs/run-known", nil)
	rr := httptest.NewRecorder()

	// Act
	mux.ServeHTTP(rr, req)

	// Assert
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "run-known") {
		t.Errorf("body does not contain run ID; body: %s", rr.Body.String())
	}
}

// ============================================================
// GET /history-fragment
// ============================================================

func TestHistoryFragmentHandler_Returns200(t *testing.T) {
	// Arrange
	db := newTestDB(t)
	mux := web.NewServer(db)

	req := httptest.NewRequest(http.MethodGet, "/history-fragment", nil)
	rr := httptest.NewRecorder()

	// Act
	mux.ServeHTTP(rr, req)

	// Assert
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
}

// ============================================================
// POST /auth + GET /auth-fragment — global auth round-trip
// ============================================================

func TestAuthHandler_SaveAndRenderMasked(t *testing.T) {
	// Arrange
	db := newTestDB(t)
	mux := web.NewServer(db)

	form := url.Values{
		"auth_type":  {"bearer"},
		"auth_token": {"tok-1234"},
	}
	req := httptest.NewRequest(http.MethodPost, "/auth", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	// Act
	mux.ServeHTTP(rr, req)

	// Assert — fragment renders, secret masked but identifiable
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if strings.Contains(body, "tok-1234") {
		t.Errorf("fragment leaks full token; body: %s", body)
	}
	if !strings.Contains(body, "1234") {
		t.Errorf("fragment does not show token suffix; body: %s", body)
	}

	// Persisted — and a second save keeps a single row
	saved, err := store.GetGlobalAuth(db)
	if err != nil {
		t.Fatalf("GetGlobalAuth: %v", err)
	}
	if saved.Type != model.AuthTypeBearer || saved.Token != "tok-1234" {
		t.Errorf("saved = %+v, want bearer tok-1234", saved)
	}

	// GET /auth-fragment shows the same state
	req2 := httptest.NewRequest(http.MethodGet, "/auth-fragment", nil)
	rr2 := httptest.NewRecorder()
	mux.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("auth-fragment status = %d, want 200", rr2.Code)
	}
	if !strings.Contains(rr2.Body.String(), "1234") {
		t.Errorf("auth-fragment missing saved state; body: %s", rr2.Body.String())
	}
}

// ============================================================
// GET /sidebar-fragment — lists every endpoint
// ============================================================

func TestSidebarFragment_ListsEndpoints(t *testing.T) {
	// Arrange
	db := newTestDB(t)
	for _, d := range []model.RequestDefinition{
		{ID: "ep-1", Name: "Get Pet", Method: "GET", URLTemplate: "https://x.com/pet/{id}"},
		{ID: "ep-2", Name: "Add Pet", Method: "POST", URLTemplate: "https://x.com/pet"},
	} {
		if err := store.SaveRequestDefinition(db, d); err != nil {
			t.Fatalf("SaveRequestDefinition: %v", err)
		}
	}
	mux := web.NewServer(db)

	req := httptest.NewRequest(http.MethodGet, "/sidebar-fragment", nil)
	rr := httptest.NewRecorder()

	// Act
	mux.ServeHTTP(rr, req)

	// Assert — one row per endpoint, method + path visible
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{"Get Pet", "GET", "/pet/{id}", "Add Pet", "POST"} {
		if !strings.Contains(body, want) {
			t.Errorf("sidebar missing %q; body: %s", want, body)
		}
	}
}

// ============================================================
// GET /requests/{id} — pre-filled builder, no auth field
// ============================================================

func TestRequestsHandler_PrefilledPanel_HasNoAuthField(t *testing.T) {
	// Arrange
	db := newTestDB(t)
	def := model.RequestDefinition{
		ID: "ep-9", Name: "Find Pets", Method: "GET",
		URLTemplate: "https://x.com/find",
		Body:        &model.BodyTemplate{Raw: []byte(`{"a":1}`)},
	}
	if err := store.SaveRequestDefinition(db, def); err != nil {
		t.Fatalf("SaveRequestDefinition: %v", err)
	}
	mux := web.NewServer(db)

	req := httptest.NewRequest(http.MethodGet, "/requests/ep-9", nil)
	rr := httptest.NewRecorder()

	// Act
	mux.ServeHTTP(rr, req)

	// Assert — pre-filled, auth lives in the global bar now
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{"https://x.com/find", "ep-9", "Test connection"} {
		if !strings.Contains(body, want) {
			t.Errorf("panel missing %q; body: %s", want, body)
		}
	}
	if strings.Contains(body, "auth_type") {
		t.Errorf("panel must not contain a per-endpoint auth field; body: %s", body)
	}
}

func TestRequestsHandler_New_ReturnsBlankPanel(t *testing.T) {
	// Arrange
	db := newTestDB(t)
	mux := web.NewServer(db)

	req := httptest.NewRequest(http.MethodGet, "/requests/new", nil)
	rr := httptest.NewRecorder()

	// Act
	mux.ServeHTTP(rr, req)

	// Assert
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Test connection") {
		t.Errorf("blank panel missing builder; body: %s", rr.Body.String())
	}
}

func TestRequestsHandler_UnknownID_Returns404(t *testing.T) {
	// Arrange
	db := newTestDB(t)
	mux := web.NewServer(db)

	req := httptest.NewRequest(http.MethodGet, "/requests/nope", nil)
	rr := httptest.NewRecorder()

	// Act
	mux.ServeHTTP(rr, req)

	// Assert
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

// ============================================================
// POST /probe uses global auth — no re-entering per endpoint
// ============================================================

func TestProbeHandler_UsesGlobalAuthAutomatically(t *testing.T) {
	// Arrange — target requires the bearer header; form carries no auth
	targetSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer shared-tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer targetSrv.Close()

	db := newTestDB(t)
	if err := store.SaveGlobalAuth(db, model.AuthConfig{Type: model.AuthTypeBearer, Token: "shared-tok"}); err != nil {
		t.Fatalf("SaveGlobalAuth: %v", err)
	}
	mux := web.NewServer(db)

	form := url.Values{"method": {"GET"}, "url": {targetSrv.URL + "/x"}}
	req := httptest.NewRequest(http.MethodPost, "/probe", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	// Act
	mux.ServeHTTP(rr, req)

	// Assert — 200 Connected proves global auth was attached
	if rr.Code >= 500 {
		t.Fatalf("status = %d, want < 500; body: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "200") {
		t.Errorf("probe did not authenticate (want 200); body: %s", rr.Body.String())
	}
}

// ============================================================
// POST /import — sidebar refresh + first endpoint in main panel
// ============================================================

func TestImportHandler_URLSpec_RefreshesSidebarAndLoadsFirst(t *testing.T) {
	// Arrange — spec served locally, no real network
	spec := `{"openapi":"3.0.0","info":{"title":"T","version":"1"},
		"paths":{"/pets":{"get":{"operationId":"listPets","responses":{"200":{"description":"ok"}}}}}}`
	specSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(spec))
	}))
	defer specSrv.Close()

	db := newTestDB(t)
	mux := web.NewServer(db)

	form := url.Values{"spec_url": {specSrv.URL + "/spec.json"}}
	req := httptest.NewRequest(http.MethodPost, "/import", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	// Act
	mux.ServeHTTP(rr, req)

	// Assert — sidebar lists the endpoint, OOB swap loads it into the main panel
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "listPets") {
		t.Errorf("response missing imported endpoint; body: %s", body)
	}
	if !strings.Contains(body, "hx-swap-oob") {
		t.Errorf("response missing OOB main-panel swap; body: %s", body)
	}
	defs, err := store.ListRequestDefinitions(db)
	if err != nil {
		t.Fatalf("ListRequestDefinitions: %v", err)
	}
	if len(defs) != 1 || defs[0].ID != "listPets" {
		t.Errorf("stored defs = %+v, want [listPets]", defs)
	}
}

// keep JSON import for compile check
var _ = json.Marshal
