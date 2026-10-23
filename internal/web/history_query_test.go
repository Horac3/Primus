package web_test

import (
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Horac3/Primus/internal/model"
	"github.com/Horac3/Primus/internal/store"
	"github.com/Horac3/Primus/internal/web"
)

func seedHistory(t *testing.T, db *sql.DB) {
	t.Helper()
	now := time.Now().UTC()
	if err := store.SaveRequestDefinition(db, model.RequestDefinition{ID: "req-login", Name: "Sign in", Method: "POST", URLTemplate: "https://secret-host.example.test/v1/login"}); err != nil {
		t.Fatalf("SaveRequestDefinition: %v", err)
	}
	if err := store.SaveTestConfig(db, model.TestConfig{ID: "cfg-login", RequestID: "req-login", Mode: model.ModeBurst}); err != nil {
		t.Fatalf("SaveTestConfig: %v", err)
	}
	runs := []model.Run{
		{ID: "run-burst-fail", TestConfigID: "cfg-login", Mode: model.ModeBurst, Outcome: model.OutcomeFail, StartedAt: now.Add(-time.Hour)},
		{ID: "run-burst-pass", TestConfigID: "cfg-login", Mode: model.ModeBurst, Outcome: model.OutcomePass, StartedAt: now.Add(-2 * time.Hour)},
		{ID: "run-load-fail", TestConfigID: "cfg-other", Mode: model.ModeLoad, Outcome: model.OutcomeFail, StartedAt: now.Add(-3 * time.Hour)},
	}
	for _, run := range runs {
		if err := store.SaveRun(db, run); err != nil {
			t.Fatalf("SaveRun: %v", err)
		}
	}
}

func getHistory(t *testing.T, mux http.Handler, question string) string {
	t.Helper()
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/history-fragment?q="+url.QueryEscape(question), nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /history-fragment?q=%q = %d %s", question, rr.Code, rr.Body.String())
	}
	return rr.Body.String()
}

func TestHistoryQueryFiltersWithRules(t *testing.T) {
	t.Setenv("PRIMUS_ANALYZER", "rules")
	db := newTestDB(t)
	seedHistory(t, db)
	mux := web.NewServer(db)

	body := getHistory(t, mux, "failed burst runs")
	if !strings.Contains(body, "run-burst-fail") || strings.Contains(body, "run-burst-pass") || strings.Contains(body, "run-load-fail") {
		t.Fatalf("filtered history lists the wrong runs: %s", body)
	}
	if !strings.Contains(body, "Interpreted as") || !strings.Contains(body, "burst runs · failed") || !strings.Contains(body, "rules") {
		t.Fatalf("history does not say how the question was read: %s", body)
	}
}

func TestHistoryQueryBlankListsEveryRun(t *testing.T) {
	t.Setenv("PRIMUS_ANALYZER", "rules")
	db := newTestDB(t)
	seedHistory(t, db)
	body := getHistory(t, web.NewServer(db), "  ")
	for _, id := range []string{"run-burst-fail", "run-burst-pass", "run-load-fail"} {
		if !strings.Contains(body, id) {
			t.Errorf("blank query hides %s", id)
		}
	}
	if strings.Contains(body, "Interpreted as") {
		t.Errorf("blank query shows an interpretation: %s", body)
	}
}

func TestHistoryQueryNoMatchesAndNotUnderstood(t *testing.T) {
	t.Setenv("PRIMUS_ANALYZER", "rules")
	db := newTestDB(t)
	seedHistory(t, db)
	mux := web.NewServer(db)

	if body := getHistory(t, mux, "soak runs"); !strings.Contains(body, "No runs match") {
		t.Errorf("empty result does not say no runs match: %s", body)
	}
	body := getHistory(t, mux, "what is the meaning of life")
	if !strings.Contains(body, "not understood") || !strings.Contains(body, "run-load-fail") {
		t.Errorf("unparsed question should list every run with a note: %s", body)
	}
}

func TestHistoryQueryRejectsLongQuestion(t *testing.T) {
	db := newTestDB(t)
	rr := httptest.NewRecorder()
	web.NewServer(db).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/history-fragment?q="+strings.Repeat("a", 501), nil))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("long question = %d, want 400", rr.Code)
	}
	// The limit counts characters, as the input's maxlength does, not bytes.
	rr = httptest.NewRecorder()
	web.NewServer(db).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/history-fragment?q="+url.QueryEscape(strings.Repeat("é", 300)), nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("300-character question = %d, want 200", rr.Code)
	}
}

func TestHistoryQueryUsesOllamaWithoutSendingRunData(t *testing.T) {
	// The request body crosses from the server goroutine through a channel so -race sees the hand-off.
	bodies := make(chan string, 1)
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bodies <- string(body)
		_, _ = w.Write([]byte(`{"message":{"content":"{\"outcome\":\"pass\"}"}}`))
	}))
	defer ollama.Close()
	t.Setenv("PRIMUS_ANALYZER", "ollama")
	t.Setenv("PRIMUS_LLM_URL", ollama.URL)
	t.Setenv("PRIMUS_LLM_MODEL", "test-model")
	db := newTestDB(t)
	seedHistory(t, db)

	body := getHistory(t, web.NewServer(db), "which runs passed")
	if !strings.Contains(body, "run-burst-pass") || strings.Contains(body, "run-burst-fail") {
		t.Fatalf("history did not apply the model's filter: %s", body)
	}
	if !strings.Contains(body, "ollama") {
		t.Errorf("history does not name the ollama interpreter: %s", body)
	}
	sent := <-bodies
	if !strings.Contains(sent, "which runs passed") {
		t.Fatalf("model was not sent the question: %s", sent)
	}
	for _, private := range []string{"run-burst", "cfg-login", "Sign in", "secret-host"} {
		if strings.Contains(sent, private) {
			t.Errorf("model request contains %q: %s", private, sent)
		}
	}
}

func TestHistoryQueryFallsBackWhenOllamaFails(t *testing.T) {
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "model not loaded", http.StatusInternalServerError)
	}))
	defer ollama.Close()
	t.Setenv("PRIMUS_ANALYZER", "ollama")
	t.Setenv("PRIMUS_LLM_URL", ollama.URL)
	t.Setenv("PRIMUS_LLM_MODEL", "test-model")
	db := newTestDB(t)
	seedHistory(t, db)

	body := getHistory(t, web.NewServer(db), "failed burst runs")
	if !strings.Contains(body, "run-burst-fail") || strings.Contains(body, "run-load-fail") {
		t.Fatalf("fallback did not filter: %s", body)
	}
	if !strings.Contains(body, "ollama unavailable") {
		t.Errorf("fallback is not labelled: %s", body)
	}
}

func TestIndexHasHistoryQueryForm(t *testing.T) {
	db := newTestDB(t)
	rr := httptest.NewRecorder()
	web.NewServer(db).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	body := rr.Body.String()
	for _, want := range []string{`name="q"`, `hx-get="/history-fragment"`, `hx-target="#history"`} {
		if !strings.Contains(body, want) {
			t.Errorf("index is missing %s", want)
		}
	}
}
