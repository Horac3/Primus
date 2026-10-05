package web_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Horac3/Primus/internal/author"
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

// pollDone polls GET /runs/{id}/status until the result fragment renders.
func pollDone(t *testing.T, mux http.Handler, id string) string {
	t.Helper()
	var body string
	for i := 0; i < 100; i++ {
		time.Sleep(50 * time.Millisecond)
		req := httptest.NewRequest(http.MethodGet, "/runs/"+id+"/status", nil)
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
		"method":           {"GET"},
		"url":              {targetSrv.URL + "/fail"},
		"mode":             {"load"},
		"concurrency":      {"2"},
		"duration_seconds": {"1"},
		// no expected_status, no assertions
	}

	// Act — start async, poll to completion
	id := postRuns(t, mux, form)
	body := pollDone(t, mux, id)
	if !strings.Contains(body, "Run insight") || !strings.Contains(body, "No assertions were configured") {
		t.Errorf("completed run card missing rules insight; body: %s", body)
	}

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
		"method":           {"GET"},
		"url":              {targetSrv.URL + "/ok"},
		"mode":             {"load"},
		"concurrency":      {"2"},
		"duration_seconds": {"1"},
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
		"method":           {"GET"},
		"url":              {targetSrv.URL + "/ok"},
		"mode":             {"load"},
		"concurrency":      {"2"},
		"duration_seconds": {"1"},
		"expected_status":  {"200"},
	}

	// Act
	id := postRuns(t, mux, form)
	body := pollDone(t, mux, id)

	// Assert — result fragment carries the per-assertion checklist
	if !strings.Contains(body, "Status code: 200") {
		t.Errorf("result missing assertion checklist; body: %s", body)
	}
	if !strings.Contains(body, "PASSED") {
		t.Errorf("checklist missing per-row verdict; body: %s", body)
	}
	if !strings.Contains(body, "verdict-pass") {
		t.Errorf("checklist missing per-row verdict class; body: %s", body)
	}
	if !strings.Contains(body, "Requests/sec") {
		t.Errorf("result missing requests/sec metric; body: %s", body)
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
		"method":           {"GET"},
		"url":              {targetSrv.URL + "/soak"},
		"mode":             {"soak"},
		"concurrency":      {"2"},
		"duration_seconds": {"1"},
		"expected_status":  {"200"},
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
		"method":           {"GET"},
		"url":              {targetSrv.URL + "/slow"},
		"mode":             {"load"},
		"concurrency":      {"1"},
		"duration_seconds": {"30"},
		"expected_status":  {"200"},
	}

	// Act — let the first slow request complete, then stop promptly
	id := postRuns(t, mux, form)
	time.Sleep(400 * time.Millisecond)
	stopReq := httptest.NewRequest(http.MethodPost, "/runs/"+id+"/stop", nil)
	stopRR := httptest.NewRecorder()
	mux.ServeHTTP(stopRR, stopReq)
	if stopRR.Code != http.StatusOK {
		t.Fatalf("stop status = %d, want 200; body: %s", stopRR.Code, stopRR.Body.String())
	}
	pollDone(t, mux, id)

	// Assert — stop settled promptly (not stuck running) with a run persisted
	runs, _ := store.ListAllRuns(db)
	if len(runs) == 0 {
		t.Fatal("no runs persisted")
	}
	total := 0
	for _, c := range runs[0].Summary.StatusCounts {
		total += c
	}
	total += runs[0].Summary.ErrorCount
	if total == 0 {
		t.Error("stopped run recorded no requests at all")
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
		"method":           {"GET"},
		"url":              {"http://x.invalid/"},
		"mode":             {"soak"},
		"concurrency":      {"1"},
		"duration_seconds": {"0"},
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
		Summary: model.RunSummary{
			StatusCounts: map[int]int{200: 10},
			ErrorDetails: []model.ErrorDetail{{Cause: "connection refused", Count: 2}},
		},
		Outcome: model.OutcomePass,
		Passed:  true,
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
	if !strings.Contains(rr.Body.String(), "connection refused") || !strings.Contains(rr.Body.String(), "Failure cause") {
		t.Errorf("run page does not show failure cause details; body: %s", rr.Body.String())
	}
}

func TestRunInsightAsyncLifecycleAndReuse(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	var calls atomic.Int32
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		started <- struct{}{}
		<-release
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":{"content":"{\"headline\":\"Run finished\",\"explanation\":\"The recorded requests completed.\",\"next_steps\":[\"Review the result.\"]}"}}`))
	}))
	defer ollama.Close()
	t.Setenv("PRIMUS_ANALYZER", "ollama")
	t.Setenv("PRIMUS_LLM_URL", ollama.URL)
	t.Setenv("PRIMUS_LLM_MODEL", "test-model")
	t.Setenv("PRIMUS_LLM_TIMEOUT", "2s")

	db := newTestDB(t)
	now := time.Now().UTC()
	if err := store.SaveRun(db, model.Run{ID: "run-insight", TestConfigID: "cfg-x", StartedAt: now, FinishedAt: now.Add(time.Second)}); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}
	mux := web.NewServer(db)
	post := httptest.NewRequest(http.MethodPost, "/runs/run-insight/insights", nil)
	postResponse := httptest.NewRecorder()
	mux.ServeHTTP(postResponse, post)
	if postResponse.Code != http.StatusOK || !strings.Contains(postResponse.Body.String(), "Generating...") || !strings.Contains(postResponse.Body.String(), `hx-trigger="every 2s"`) {
		t.Fatalf("POST insight response = %d %s", postResponse.Code, postResponse.Body.String())
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("analyzer was not called")
	}
	pending := httptest.NewRecorder()
	mux.ServeHTTP(pending, httptest.NewRequest(http.MethodGet, "/runs/run-insight/insights", nil))
	if !strings.Contains(pending.Body.String(), "Generating...") || !strings.Contains(pending.Body.String(), `hx-trigger="every 2s"`) {
		t.Fatalf("GET pending insight = %d %s", pending.Code, pending.Body.String())
	}

	duplicate := httptest.NewRecorder()
	mux.ServeHTTP(duplicate, httptest.NewRequest(http.MethodPost, "/runs/run-insight/insights", nil))
	select {
	case <-started:
		t.Fatal("duplicate POST started a second analyzer")
	default:
	}

	unblock()
	deadline := time.Now().Add(2 * time.Second)
	var finalBody string
	for time.Now().Before(deadline) {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/runs/run-insight/insights", nil))
		finalBody = response.Body.String()
		if strings.Contains(finalBody, "Run finished") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(finalBody, "via ollama") || !strings.Contains(finalBody, "test-model") || strings.Contains(finalBody, `hx-trigger="every 2s"`) {
		t.Fatalf("final insight fragment = %s", finalBody)
	}

	page := httptest.NewRecorder()
	mux.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/runs/run-insight", nil))
	if !strings.Contains(page.Body.String(), "Run finished") || !strings.Contains(page.Body.String(), "via ollama") || !strings.Contains(page.Body.String(), "test-model") {
		t.Fatalf("reopened run page did not reuse insight: %s", page.Body.String())
	}
	if calls.Load() != 1 {
		t.Errorf("analyzer calls = %d, want 1", calls.Load())
	}
}

func TestRunInsightDisplaysRulesFallbackAnalyzer(t *testing.T) {
	// Arrange
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"message":{"content":"{\"headline\":\"Uptime was 87.65 percent\",\"explanation\":\"The run was successful.\",\"next_steps\":[\"Review the run.\"]}"}}`))
	}))
	defer ollama.Close()
	t.Setenv("PRIMUS_ANALYZER", "ollama")
	t.Setenv("PRIMUS_LLM_URL", ollama.URL)
	t.Setenv("PRIMUS_LLM_MODEL", "test-model")
	t.Setenv("PRIMUS_LLM_TIMEOUT", "2s")

	db := newTestDB(t)
	now := time.Now().UTC()
	if err := store.SaveRun(db, model.Run{ID: "run-rules-fallback", StartedAt: now, FinishedAt: now.Add(time.Second)}); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}
	mux := web.NewServer(db)

	// Act
	post := httptest.NewRecorder()
	mux.ServeHTTP(post, httptest.NewRequest(http.MethodPost, "/runs/run-rules-fallback/insights", nil))
	deadline := time.Now().Add(2 * time.Second)
	var finalBody string
	for time.Now().Before(deadline) {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/runs/run-rules-fallback/insights", nil))
		finalBody = response.Body.String()
		if strings.Contains(finalBody, "via rules") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Assert
	if post.Code != http.StatusOK || !strings.Contains(post.Body.String(), "Generating...") {
		t.Fatalf("POST insight response = %d %s", post.Code, post.Body.String())
	}
	if !strings.Contains(finalBody, "via rules") || !strings.Contains(finalBody, "No assertions were configured") {
		t.Fatalf("fallback insight fragment = %s", finalBody)
	}
}

func TestRunInsightStartupMarksPendingFailed(t *testing.T) {
	db := newTestDB(t)
	if _, _, err := store.StartInsight(db, "run-orphan", "rules", "rules-v1"); err != nil {
		t.Fatalf("StartInsight: %v", err)
	}
	_ = web.NewServer(db)
	got, err := store.GetInsight(db, "run-orphan")
	if err != nil {
		t.Fatalf("GetInsight: %v", err)
	}
	if got.Status != store.InsightFailed {
		t.Fatalf("pending insight status = %q, want failed", got.Status)
	}
}

func TestGetFailedInsightHasFinalFragmentAndMetadata(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().UTC()
	if err := store.SaveRun(db, model.Run{ID: "run-failed-insight", StartedAt: now, FinishedAt: now.Add(time.Second)}); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}

	if _, _, err := store.StartInsight(db, "run-failed-insight", "ollama", "qwen2.5:3b"); err != nil {
		t.Fatalf("StartInsight: %v", err)
	}
	if err := store.FailInsight(db, "run-failed-insight", "ollama", "qwen2.5:3b", "model unavailable"); err != nil {
		t.Fatalf("FailInsight: %v", err)
	}

	rr := httptest.NewRecorder()
	web.NewServer(db).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/runs/run-failed-insight/insights", nil))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "ollama") || !strings.Contains(rr.Body.String(), "qwen2.5:3b") || !strings.Contains(rr.Body.String(), "model unavailable") {
		t.Fatalf("failed insight response = %d %s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), `hx-trigger="every 2s"`) {
		t.Error("failed insight fragment should not poll")
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

func TestAuthFragment_BasicCredentialsVisibleOnlyForBasic(t *testing.T) {
	// Arrange
	db := newTestDB(t)
	mux := web.NewServer(db)
	cases := []struct {
		authType model.AuthType
		visible  bool
	}{
		{authType: model.AuthTypeNone, visible: false},
		{authType: model.AuthTypeBearer, visible: false},
		{authType: model.AuthTypeAPIKey, visible: false},
		{authType: model.AuthTypeBasic, visible: true},
	}

	for _, tc := range cases {
		t.Run(string(tc.authType), func(t *testing.T) {
			if err := store.SaveGlobalAuth(db, model.AuthConfig{Type: tc.authType}); err != nil {
				t.Fatalf("SaveGlobalAuth: %v", err)
			}
			req := httptest.NewRequest(http.MethodGet, "/auth-fragment", nil)
			rr := httptest.NewRecorder()

			// Act
			mux.ServeHTTP(rr, req)

			// Assert
			body := rr.Body.String()
			for _, field := range []string{"auth_username", "auth_password"} {
				position := strings.Index(body, `name="`+field+`"`)
				if position < 0 {
					t.Errorf("fragment missing %s field", field)
					continue
				}
				labelStart := strings.LastIndex(body[:position], "<label")
				label := body[labelStart:position]
				hidden := strings.Contains(label, "hidden")
				if hidden == tc.visible {
					t.Errorf("%s hidden = %v, want visible = %v; label: %s", field, hidden, tc.visible, label)
				}
			}
		})
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
// Sidebar sparkline — recent pass/fail dots per endpoint
// ============================================================

func TestSidebarFragment_ShowsRecentRunDots(t *testing.T) {
	// Arrange — two endpoints, runs only for the first (pass then fail)
	db := newTestDB(t)
	for _, d := range []model.RequestDefinition{
		{ID: "ep-1", Name: "Get Pet", Method: "GET", URLTemplate: "https://x.com/pet"},
		{ID: "ep-2", Name: "Add Pet", Method: "POST", URLTemplate: "https://x.com/pet"},
	} {
		if err := store.SaveRequestDefinition(db, d); err != nil {
			t.Fatalf("SaveRequestDefinition: %v", err)
		}
	}
	if err := store.SaveTestConfig(db, model.TestConfig{
		ID: "cfg-1", RequestID: "ep-1", Mode: model.ModeLoad,
		Concurrency: 1, TotalRequests: 1,
	}); err != nil {
		t.Fatalf("SaveTestConfig: %v", err)
	}
	now := time.Now().UTC()
	for i, passed := range []bool{true, false} {
		if err := store.SaveRun(db, model.Run{
			ID:           string(rune('a'+i)) + "-run",
			TestConfigID: "cfg-1",
			StartedAt:    now.Add(time.Duration(i) * time.Second),
			FinishedAt:   now.Add(time.Duration(i)*time.Second + time.Second),
			Passed:       passed,
		}); err != nil {
			t.Fatalf("SaveRun: %v", err)
		}
	}
	mux := web.NewServer(db)

	req := httptest.NewRequest(http.MethodGet, "/sidebar-fragment", nil)
	rr := httptest.NewRecorder()

	// Act
	mux.ServeHTTP(rr, req)

	// Assert — one pass dot and one fail dot for ep-1, ep-2 row intact
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "dot pass") {
		t.Errorf("sidebar missing pass dot; body: %s", body)
	}
	if !strings.Contains(body, "dot fail") {
		t.Errorf("sidebar missing fail dot; body: %s", body)
	}
	if !strings.Contains(body, "Add Pet") {
		t.Errorf("endpoint without runs missing from sidebar; body: %s", body)
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
// POST /probe with authz_override — per-endpoint win over global
// ============================================================

func TestProbeHandler_OverrideField_WinsOverGlobal(t *testing.T) {
	// Arrange — target only accepts the per-endpoint credential
	targetSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer custom" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer targetSrv.Close()

	db := newTestDB(t)
	if err := store.SaveGlobalAuth(db, model.AuthConfig{Type: model.AuthTypeBearer, Token: "global-tok"}); err != nil {
		t.Fatalf("SaveGlobalAuth: %v", err)
	}
	mux := web.NewServer(db)

	form := url.Values{
		"method":         {"GET"},
		"url":            {targetSrv.URL + "/x"},
		"authz_override": {"Bearer custom"},
	}
	req := httptest.NewRequest(http.MethodPost, "/probe", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	// Act
	mux.ServeHTTP(rr, req)

	// Assert — 200 proves the override was sent instead of the global token
	if rr.Code >= 500 {
		t.Fatalf("status = %d, want < 500; body: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "200") {
		t.Errorf("probe did not use override (want 200); body: %s", rr.Body.String())
	}
}

func TestProbeHandler_SentinelOverride_DefersToGlobal(t *testing.T) {
	// Arrange — target requires the global credential
	targetSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer global-tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer targetSrv.Close()

	db := newTestDB(t)
	if err := store.SaveGlobalAuth(db, model.AuthConfig{Type: model.AuthTypeBearer, Token: "global-tok"}); err != nil {
		t.Fatalf("SaveGlobalAuth: %v", err)
	}
	mux := web.NewServer(db)

	form := url.Values{
		"method":         {"GET"},
		"url":            {targetSrv.URL + "/x"},
		"authz_override": {model.HeaderGlobalSentinel},
	}
	req := httptest.NewRequest(http.MethodPost, "/probe", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	// Act
	mux.ServeHTTP(rr, req)

	// Assert — sentinel keeps global auth; literal sentinel never sent
	if rr.Code >= 500 {
		t.Fatalf("status = %d, want < 500; body: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "200") {
		t.Errorf("probe did not apply global auth (want 200); body: %s", rr.Body.String())
	}
}

// ============================================================
// Endpoint panel shows the Authorization override row
// ============================================================

func TestRequestsHandler_PanelHasOverrideRow(t *testing.T) {
	// Arrange
	db := newTestDB(t)
	if err := store.SaveRequestDefinition(db, model.RequestDefinition{
		ID: "ep-ov", Name: "Ov", Method: "GET", URLTemplate: "https://x.com/o",
	}); err != nil {
		t.Fatalf("SaveRequestDefinition: %v", err)
	}
	mux := web.NewServer(db)

	req := httptest.NewRequest(http.MethodGet, "/requests/ep-ov", nil)
	rr := httptest.NewRecorder()

	// Act
	mux.ServeHTTP(rr, req)

	// Assert — [GLOBAL] placeholder row, removable per request
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, model.HeaderGlobalSentinel) {
		t.Errorf("panel missing [GLOBAL] placeholder; body: %s", body)
	}
	if !strings.Contains(body, "authz_override") {
		t.Errorf("panel missing override field; body: %s", body)
	}
}

// ============================================================
// POST /runs returns immediately even for long-duration runs
// ============================================================

func TestRunsHandler_ReturnsImmediately_ForLongRun(t *testing.T) {
	// Arrange — instant target, but a 60s duration (would block a sync handler)
	targetSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer targetSrv.Close()

	db := newTestDB(t)
	mux := web.NewServer(db)

	form := url.Values{
		"method":           {"GET"},
		"url":              {targetSrv.URL + "/long"},
		"mode":             {"load"},
		"concurrency":      {"1"},
		"duration_seconds": {"60"},
	}
	req := httptest.NewRequest(http.MethodPost, "/runs", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	// Act
	start := time.Now()
	mux.ServeHTTP(rr, req)
	elapsed := time.Since(start)

	// Assert — accepted immediately, far short of the 60s run
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body: %s", rr.Code, rr.Body.String())
	}
	if elapsed >= 10*time.Second {
		t.Errorf("POST /runs took %v, want immediate return", elapsed)
	}

	// Cleanup — the 60s run must still be alive (proves it outlived the
	// 202 response), then stop it and confirm it settles (not stuck)
	body := rr.Body.String()
	const marker = `data-run-id="`
	i := strings.Index(body, marker)
	if i < 0 {
		t.Fatalf("missing data-run-id; body: %s", body)
	}
	rest := body[i+len(marker):]
	id := rest[:strings.Index(rest, `"`)]
	aliveReq := httptest.NewRequest(http.MethodGet, "/runs/"+id+"/status", nil)
	aliveRR := httptest.NewRecorder()
	mux.ServeHTTP(aliveRR, aliveReq)
	if strings.Contains(aliveRR.Body.String(), "badge-") {
		t.Fatalf("run finished before Stop — it should still be running; body: %s", aliveRR.Body.String())
	}
	stopReq := httptest.NewRequest(http.MethodPost, "/runs/"+id+"/stop", nil)
	stopRR := httptest.NewRecorder()
	mux.ServeHTTP(stopRR, stopReq)
	pollDone(t, mux, id)
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
// POST /runs — failing assertions render FAILED checklist rows
// ============================================================

func TestRunsHandler_ChecklistShowsFailedRows(t *testing.T) {
	// Arrange — server returns 500, assertions demand 200
	targetSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer targetSrv.Close()

	db := newTestDB(t)
	mux := web.NewServer(db)

	form := url.Values{
		"method":           {"GET"},
		"url":              {targetSrv.URL + "/bad"},
		"mode":             {"load"},
		"concurrency":      {"2"},
		"duration_seconds": {"1"},
		"expected_status":  {"200"},
	}

	// Act
	id := postRuns(t, mux, form)
	body := pollDone(t, mux, id)

	// Assert — checklist names the assertion and marks the row FAILED
	if !strings.Contains(body, "Status code: 200") {
		t.Errorf("result missing checklist row; body: %s", body)
	}
	if !strings.Contains(body, "verdict-fail") {
		t.Errorf("result missing per-row FAILED verdict; body: %s", body)
	}
}

// ============================================================
// GET /runs/{id} — histogram bars scale to the max bucket
// ============================================================

func TestGetRunHandler_HistogramBarHeights(t *testing.T) {
	// Arrange — run with a known bucket distribution (max 10 → 100px)
	db := newTestDB(t)
	now := time.Now().UTC()
	run := model.Run{
		ID:             "run-hist",
		TestConfigID:   "cfg-x",
		StartedAt:      now,
		FinishedAt:     now.Add(time.Second),
		Summary:        model.RunSummary{StatusCounts: map[int]int{200: 15}},
		Outcome:        model.OutcomePass,
		Passed:         true,
		LatencyBuckets: []int{10, 5, 0, 0, 0, 0, 0, 0, 0},
	}
	if err := store.SaveRun(db, run); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}
	mux := web.NewServer(db)

	req := httptest.NewRequest(http.MethodGet, "/runs/run-hist", nil)
	rr := httptest.NewRecorder()

	// Act
	mux.ServeHTTP(rr, req)

	// Assert — tallest bar 100px, half bar 50px
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "height:100px") {
		t.Errorf("missing full-height bar; body: %s", body)
	}
	if !strings.Contains(body, "height:50px") {
		t.Errorf("missing half-height bar; body: %s", body)
	}
	if !strings.Contains(body, "Latency distribution") {
		t.Errorf("missing histogram label; body: %s", body)
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

// ============================================================
// Docs viewer — index, page, 404, nav link
// ============================================================

func TestDocsIndex_ListsEveryDoc(t *testing.T) {
	// Arrange
	db := newTestDB(t)
	mux := web.NewServer(db)

	req := httptest.NewRequest(http.MethodGet, "/docs", nil)
	rr := httptest.NewRecorder()

	// Act
	mux.ServeHTTP(rr, req)

	// Assert — one link per reference doc
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	for _, slug := range []string{"load", "burst", "soak", "auth-probe", "global-auth", "assertions", "cli"} {
		if !strings.Contains(body, `href="/docs/`+slug+`"`) {
			t.Errorf("index missing link to %q", slug)
		}
	}
}

func TestDocsPage_RendersDoc(t *testing.T) {
	// Arrange
	db := newTestDB(t)
	mux := web.NewServer(db)

	req := httptest.NewRequest(http.MethodGet, "/docs/load", nil)
	rr := httptest.NewRecorder()

	// Act
	mux.ServeHTTP(rr, req)

	// Assert — the markdown heading rendered as HTML, inside the shell
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "<h1>Load test</h1>") {
		t.Errorf("page missing rendered heading; body: %.300s…", body)
	}
	if !strings.Contains(body, `href="/"`) {
		t.Errorf("page missing nav home link; body: %.300s…", body)
	}
}

func TestDocsPage_UnknownSlug_Returns404(t *testing.T) {
	// Arrange
	db := newTestDB(t)
	mux := web.NewServer(db)

	req := httptest.NewRequest(http.MethodGet, "/docs/nope", nil)
	rr := httptest.NewRecorder()

	// Act
	mux.ServeHTTP(rr, req)

	// Assert — 404 page, not a panic or blank 200
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

func TestNav_DocsLinkVisibleFromShell(t *testing.T) {
	// Arrange
	db := newTestDB(t)
	mux := web.NewServer(db)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()

	// Act
	mux.ServeHTTP(rr, req)

	// Assert
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `href="/docs"`) {
		t.Errorf("main shell missing Docs nav link")
	}
}

// keep JSON import for compile check
var _ = json.Marshal

func TestPostNLRequestMatchesOnlyProvidedEndpointOrExplicitNew(t *testing.T) {
	// Arrange
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode Ollama request: %v", err)
			return
		}
		var input author.RequestPrompt
		if err := json.Unmarshal([]byte(request.Messages[1].Content), &input); err != nil {
			t.Errorf("decode prompt: %v", err)
			return
		}
		if len(input.Endpoints) != 2 || input.Endpoints[1] != (author.Endpoint{
			ID: "ep-login", Name: "User login", Method: "POST", URL: "https://api.example.com/login",
		}) {
			t.Errorf("endpoint context = %+v, want endpoint metadata only", input.Endpoints)
		}
		var content string
		if strings.Contains(strings.ToLower(input.Description), "login") {
			content = `{"matched_existing_id":"ep-login","method":"GET","url":"https://invented.example/path","headers":{"Content-Type":"application/json"},"body":"{\"password\":\"wrong\"}","confidence_note":"Matched User login."}`
		} else {
			content = `{"matched_existing_id":null,"method":"POST","url":"https://api.example.com/invoices","headers":{},"body":"{}","confidence_note":"No invoice endpoint matches; this is a new target."}`
		}
		response, err := json.Marshal(map[string]any{"message": map[string]string{"content": content}})
		if err != nil {
			t.Errorf("marshal Ollama response: %v", err)
			return
		}
		_, _ = w.Write(response)
	}))
	defer ollama.Close()
	t.Setenv("PRIMUS_ANALYZER", "ollama")
	t.Setenv("PRIMUS_LLM_URL", ollama.URL)
	t.Setenv("PRIMUS_LLM_MODEL", "test-model")

	db := newTestDB(t)
	for _, def := range []model.RequestDefinition{
		{ID: "ep-login", Name: "User login", Method: "POST", URLTemplate: "https://api.example.com/login", Body: &model.BodyTemplate{Raw: []byte(`{"username":"x"}`)}},
		{ID: "ep-health", Name: "Health", Method: "GET", URLTemplate: "https://api.example.com/health", Headers: map[string]string{"X-private": "must-not-be-sent"}},
	} {
		if err := store.SaveRequestDefinition(db, def); err != nil {
			t.Fatalf("SaveRequestDefinition: %v", err)
		}
	}
	mux := web.NewServer(db)
	tests := []struct {
		name           string
		description    string
		wantID         string
		wantMethod     string
		wantURL        string
		wantConfidence string
	}{
		{
			name: "clearly matches endpoint", description: "Test the login endpoint with a wrong password",
			wantID: "ep-login", wantMethod: "POST", wantURL: "https://api.example.com/login",
			wantConfidence: "Matched User login.",
		},
		{
			name: "falls through to new endpoint", description: "Create a new invoice endpoint",
			wantMethod: "POST", wantURL: "https://api.example.com/invoices",
			wantConfidence: "No invoice endpoint matches; this is a new target.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			payload, err := json.Marshal(map[string]string{"description": tt.description})
			if err != nil {
				t.Fatalf("marshal input: %v", err)
			}
			request := httptest.NewRequest(http.MethodPost, "/nl/request", bytes.NewReader(payload))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()

			// Act
			mux.ServeHTTP(response, request)

			// Assert
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			var got author.RequestSuggestion
			if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode suggestion: %v", err)
			}
			if got.Method != tt.wantMethod || got.URL != tt.wantURL || got.ConfidenceNote != tt.wantConfidence {
				t.Errorf("suggestion = %+v, want method %s URL %s note %q", got, tt.wantMethod, tt.wantURL, tt.wantConfidence)
			}
			if tt.wantID == "" {
				if got.MatchedExistingID != nil {
					t.Errorf("MatchedExistingID = %q, want null", *got.MatchedExistingID)
				}
			} else if got.MatchedExistingID == nil || *got.MatchedExistingID != tt.wantID {
				t.Errorf("MatchedExistingID = %v, want %q", got.MatchedExistingID, tt.wantID)
			}
			if got.MatchedExistingID != nil && got.URL != "https://api.example.com/login" &&
				got.URL != "https://api.example.com/health" {
				t.Errorf("matched URL %q was not in endpoint list", got.URL)
			}
		})
	}
	definitions, err := store.ListRequestDefinitions(db)
	if err != nil {
		t.Fatalf("ListRequestDefinitions: %v", err)
	}
	if len(definitions) != 2 {
		t.Errorf("generation persisted endpoint definitions: got %d, want the original 2", len(definitions))
	}
	configs, err := store.ListTestConfigs(db)
	if err != nil {
		t.Fatalf("ListTestConfigs: %v", err)
	}
	runs, err := store.ListAllRuns(db)
	if err != nil {
		t.Fatalf("ListAllRuns: %v", err)
	}
	if len(configs) != 0 || len(runs) != 0 {
		t.Errorf("generation persisted configs/runs: %d/%d, want none", len(configs), len(runs))
	}
}

func TestRequestPanelHidesNaturalLanguageControlsWithoutOllama(t *testing.T) {
	// Arrange
	t.Setenv("PRIMUS_ANALYZER", "rules")
	db := newTestDB(t)
	mux := web.NewServer(db)
	request := httptest.NewRequest(http.MethodGet, "/requests/new", nil)
	response := httptest.NewRecorder()

	// Act
	mux.ServeHTTP(response, request)

	// Assert
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "Generate from description") ||
		strings.Contains(response.Body.String(), "Generate test config") {
		t.Errorf("NL authoring controls should be hidden when Ollama is not selected: %s", response.Body.String())
	}
}
