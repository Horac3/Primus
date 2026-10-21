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

func getExport(t *testing.T, mux http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
	return rr
}

func seedReportRun(t *testing.T, db *sql.DB, run model.Run) {
	t.Helper()
	if err := store.SaveRequestDefinition(db, model.RequestDefinition{
		ID: "req-r", Name: "Orders", Method: "GET",
		URLTemplate: "https://api.example.test/v1/orders?token=QUERYSECRET",
		Headers:     map[string]string{"X-Api-Key": "HEADERSECRET"},
		Body:        &model.BodyTemplate{Raw: []byte(`{"pw":"BODYSECRET"}`)},
	}); err != nil {
		t.Fatalf("SaveRequestDefinition: %v", err)
	}
	if err := store.SaveTestConfig(db, model.TestConfig{ID: "cfg-r", RequestID: "req-r", Mode: model.ModeBurst}); err != nil {
		t.Fatalf("SaveTestConfig: %v", err)
	}
	if run.TestConfigID == "" {
		run.TestConfigID = "cfg-r"
	}
	if err := store.SaveRun(db, run); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}
}

func TestMarkdownReport(t *testing.T) {
	now := time.Now().UTC()
	base := model.Run{
		ID: "run-md", Mode: model.ModeBurst, Concurrency: 5,
		StartedAt: now, FinishedAt: now.Add(4 * time.Second),
		Summary: model.RunSummary{
			TotalRequests: 100, ErrorCount: 3, ErrorRate: 0.03, P50Millis: 11, P95Millis: 42, P99Millis: 77,
			StatusCounts: map[int]int{200: 97, 500: 3},
			ErrorDetails: []model.ErrorDetail{{Cause: `Request failed: Get "http://u:p@h.test/x?api_key=ERRSECRET": refused`, Count: 3}},
		},
		AssertionResults: []model.AssertionResult{{Name: "p95 under 100ms", Passed: true}, {Name: "error rate under 1%", Passed: false}},
		Outcome:          model.OutcomeFail,
	}

	t.Run("contains the facts and no secrets", func(t *testing.T) {
		// Arrange
		db := newTestDB(t)
		seedReportRun(t, db, base)
		mux := web.NewServer(db)

		// Act
		rr := getExport(t, mux, "/runs/run-md/report.md")

		// Assert
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
		}
		if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/markdown") {
			t.Errorf("Content-Type = %q, want text/markdown", ct)
		}
		if cd := rr.Header().Get("Content-Disposition"); cd != `attachment; filename="primus-run-run-md.md"` {
			t.Errorf("Content-Disposition = %q", cd)
		}
		body := rr.Body.String()
		for _, want := range []string{
			"# Primus run run-md", "FAILED", "GET https://api.example.test/v1/orders",
			"| p95 | 42 ms |", "p95 under 100ms", "error rate under 1%", "http://h.test/x", "No insight generated",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("report missing %q:\n%s", want, body)
			}
		}
		for _, secret := range []string{"QUERYSECRET", "HEADERSECRET", "BODYSECRET", "ERRSECRET", "u:p"} {
			if strings.Contains(body, secret) {
				t.Errorf("report leaks %q:\n%s", secret, body)
			}
		}
	})

	t.Run("verdict wording is tri-state", func(t *testing.T) {
		for outcome, want := range map[model.RunOutcome]string{
			model.OutcomePass:         "PASSED",
			model.OutcomeNoAssertions: "no assertions configured",
			model.OutcomeFail:         "FAILED",
		} {
			db := newTestDB(t)
			run := base
			run.Outcome = outcome
			seedReportRun(t, db, run)
			body := getExport(t, web.NewServer(db), "/runs/run-md/report.md").Body.String()
			if !strings.Contains(body, want) {
				t.Errorf("outcome %q: report missing %q:\n%s", outcome, want, body)
			}
		}
	})

	t.Run("names the analyzer that wrote the insight", func(t *testing.T) {
		db := newTestDB(t)
		seedReportRun(t, db, base)
		if _, _, err := store.StartInsight(db, "run-md", "rules", ""); err != nil {
			t.Fatalf("StartInsight: %v", err)
		}
		if err := store.CompleteInsight(db, "run-md", "rules", "", "Latency is stable."); err != nil {
			t.Fatalf("CompleteInsight: %v", err)
		}
		body := getExport(t, web.NewServer(db), "/runs/run-md/report.md").Body.String()
		if !strings.Contains(body, "Insight (rules)") || !strings.Contains(body, "Latency is stable.") {
			t.Errorf("report does not carry the insight and analyzer:\n%s", body)
		}
	})

	t.Run("deleted endpoint is stated", func(t *testing.T) {
		db := newTestDB(t)
		if err := store.SaveRun(db, model.Run{ID: "run-orphan", TestConfigID: "cfg-gone", Outcome: model.OutcomePass}); err != nil {
			t.Fatalf("SaveRun: %v", err)
		}
		body := getExport(t, web.NewServer(db), "/runs/run-orphan/report.md").Body.String()
		if !strings.Contains(body, "endpoint deleted") {
			t.Errorf("report does not say the endpoint was deleted:\n%s", body)
		}
	})

	t.Run("unknown run is 404", func(t *testing.T) {
		db := newTestDB(t)
		if rr := getExport(t, web.NewServer(db), "/runs/nope/report.md"); rr.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", rr.Code)
		}
	})
}

func TestSeriesCSV(t *testing.T) {
	// Arrange
	db := newTestDB(t)
	if err := store.SaveRun(db, model.Run{
		ID: "run-csv",
		TimeSeries: []model.Point{
			{ElapsedMs: 1000, AvgLatencyMs: 12.5, RPS: 80, ErrorRate: 0},
			{ElapsedMs: 2000, AvgLatencyMs: 20, RPS: 75.5, ErrorRate: 0.02},
		},
	}); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}
	if err := store.SaveRun(db, model.Run{ID: "run-empty"}); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}
	mux := web.NewServer(db)

	t.Run("rows follow the header", func(t *testing.T) {
		// Act
		rr := getExport(t, mux, "/runs/run-csv/series.csv")

		// Assert
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body.String())
		}
		if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
			t.Errorf("Content-Type = %q, want text/csv", ct)
		}
		if cd := rr.Header().Get("Content-Disposition"); cd != `attachment; filename="primus-run-run-csv.csv"` {
			t.Errorf("Content-Disposition = %q", cd)
		}
		want := "elapsed_ms,avg_latency_ms,rps,error_rate\n1000,12.5,80,0\n2000,20,75.5,0.02\n"
		if got := strings.ReplaceAll(rr.Body.String(), "\r\n", "\n"); got != want {
			t.Errorf("body = %q, want %q", got, want)
		}
	})

	t.Run("empty series is header only", func(t *testing.T) {
		rr := getExport(t, mux, "/runs/run-empty/series.csv")
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rr.Code)
		}
		if got := strings.TrimSpace(rr.Body.String()); got != "elapsed_ms,avg_latency_ms,rps,error_rate" {
			t.Errorf("body = %q, want header only", got)
		}
	})

	t.Run("unknown run is 404", func(t *testing.T) {
		if rr := getExport(t, mux, "/runs/nope/series.csv"); rr.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", rr.Code)
		}
	})
}
