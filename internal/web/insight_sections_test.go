package web_test

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Horac3/Primus/internal/model"
	"github.com/Horac3/Primus/internal/store"
	"github.com/Horac3/Primus/internal/web"
)

func saveEndpointRun(t *testing.T, db *sql.DB, id, cfg string, at time.Time, total, p95 int) {
	t.Helper()
	run := model.Run{ID: id, TestConfigID: cfg, Mode: model.ModeLoad, Concurrency: 2, StartedAt: at, FinishedAt: at.Add(time.Second)}
	run.Summary.TotalRequests = total
	run.Summary.P95Millis = p95
	if err := store.SaveRun(db, run); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}
}

// insightFragment generates the run's insight and returns the finished fragment.
func insightFragment(t *testing.T, mux http.Handler, runID string) string {
	t.Helper()
	mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/runs/"+runID+"/insights", nil))
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/runs/"+runID+"/insights", nil))
		if body := rec.Body.String(); !strings.Contains(body, "Generating...") {
			return body
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("insight stayed pending")
	return ""
}

func TestInsightFragment_ShowsFixedSectionsAndComparison(t *testing.T) {
	t.Setenv("PRIMUS_ANALYZER", "rules")
	db := newTestDB(t)
	if err := store.SaveTestConfig(db, model.TestConfig{ID: "cfg-a", RequestID: "req-a"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTestConfig(db, model.TestConfig{ID: "cfg-b", RequestID: "req-a"}); err != nil {
		t.Fatal(err)
	}
	t0 := time.Now().UTC().Add(-time.Hour)
	saveEndpointRun(t, db, "older", "cfg-a", t0, 50, 100)
	saveEndpointRun(t, db, "newer", "cfg-b", t0.Add(time.Minute), 50, 300)
	mux := web.NewServer(db)

	body := insightFragment(t, mux, "newer")
	for _, want := range []string{
		"What the numbers say", "Likely causes", "What to try next",
		"Compared with the previous run", "p95 rose from 100 ms to 300 ms", "via rules",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("fragment missing %q:\n%s", want, body)
		}
	}
}

func TestInsightFragment_FirstRunHasNoComparisonSection(t *testing.T) {
	t.Setenv("PRIMUS_ANALYZER", "rules")
	db := newTestDB(t)
	if err := store.SaveTestConfig(db, model.TestConfig{ID: "cfg-a", RequestID: "req-a"}); err != nil {
		t.Fatal(err)
	}
	saveEndpointRun(t, db, "only", "cfg-a", time.Now().UTC(), 50, 100)
	body := insightFragment(t, web.NewServer(db), "only")
	if !strings.Contains(body, "What the numbers say") || strings.Contains(body, "Compared with the previous run") {
		t.Errorf("first run fragment:\n%s", body)
	}
}

func TestInsightFragment_NamesTheRulesFallbackAfterAFailedOllama(t *testing.T) {
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"message":{"content":"{\"headline\":\"Uptime was 87.65 percent\",\"explanation\":\"Fine.\",\"next_steps\":[\"Review.\"]}"}}`))
	}))
	defer ollama.Close()
	t.Setenv("PRIMUS_ANALYZER", "ollama")
	t.Setenv("PRIMUS_LLM_URL", ollama.URL)
	t.Setenv("PRIMUS_LLM_MODEL", "test-model")
	t.Setenv("PRIMUS_LLM_TIMEOUT", "2s")
	db := newTestDB(t)
	saveEndpointRun(t, db, "r", "cfg-a", time.Now().UTC(), 5, 100)

	body := insightFragment(t, web.NewServer(db), "r")
	if !strings.Contains(body, "via rules") || !strings.Contains(body, "Ollama") || !strings.Contains(body, "What to try next") {
		t.Errorf("fallback fragment should say the rules analyzer wrote it after Ollama failed:\n%s", body)
	}
}

func TestInsightFragment_LegacyInsightKeepsItsParagraph(t *testing.T) {
	db := newTestDB(t)
	saveEndpointRun(t, db, "r", "cfg-a", time.Now().UTC(), 5, 100)
	if _, _, err := store.StartInsight(db, "r", "rules", "rules-v1"); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteInsight(db, "r", "rules", "rules-v1", "A paragraph from before sections."); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	web.NewServer(db).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/runs/r/insights", nil))
	body := rec.Body.String()
	if !strings.Contains(body, "A paragraph from before sections.") || strings.Contains(body, "What the numbers say") {
		t.Errorf("legacy fragment:\n%s", body)
	}
}

func TestReports_RenderInsightSections(t *testing.T) {
	t.Setenv("PRIMUS_ANALYZER", "rules")
	db := newTestDB(t)
	if err := store.SaveTestConfig(db, model.TestConfig{ID: "cfg-a", RequestID: "req-a"}); err != nil {
		t.Fatal(err)
	}
	t0 := time.Now().UTC().Add(-time.Hour)
	saveEndpointRun(t, db, "older", "cfg-a", t0, 50, 100)
	saveEndpointRun(t, db, "newer", "cfg-a", t0.Add(time.Minute), 50, 300)
	mux := web.NewServer(db)
	insightFragment(t, mux, "newer")

	md := httptest.NewRecorder()
	mux.ServeHTTP(md, httptest.NewRequest(http.MethodGet, "/runs/newer/report.md", nil))
	for _, want := range []string{"### What the numbers say", "### Likely causes", "### What to try next", "### Compared with the previous run", "p95 rose from 100 ms to 300 ms"} {
		if !strings.Contains(md.Body.String(), want) {
			t.Errorf("report.md missing %q:\n%s", want, md.Body.String())
		}
	}
	page := httptest.NewRecorder()
	mux.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/runs/newer/report.html", nil))
	for _, want := range []string{"What the numbers say", "Likely causes", "What to try next", "Compared with the previous run"} {
		if !strings.Contains(page.Body.String(), want) {
			t.Errorf("report.html missing %q", want)
		}
	}
}

func TestReports_LegacyInsightStillShowsItsText(t *testing.T) {
	db := newTestDB(t)
	saveEndpointRun(t, db, "r", "cfg-a", time.Now().UTC(), 5, 100)
	if _, _, err := store.StartInsight(db, "r", "rules", "rules-v1"); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteInsight(db, "r", "rules", "rules-v1", "Old paragraph."); err != nil {
		t.Fatal(err)
	}
	md := httptest.NewRecorder()
	web.NewServer(db).ServeHTTP(md, httptest.NewRequest(http.MethodGet, "/runs/r/report.md", nil))
	if !strings.Contains(md.Body.String(), "Old paragraph.") || strings.Contains(md.Body.String(), "What the numbers say") {
		t.Errorf("legacy report.md:\n%s", md.Body.String())
	}
}
