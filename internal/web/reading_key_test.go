package web_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Horac3/Primus/internal/model"
	"github.com/Horac3/Primus/internal/store"
	"github.com/Horac3/Primus/internal/web"
)

// keyTerms are the terms every report's "how to read this" key must define (FR-REP-02),
// minus login latency, which only appears when a credential pool was active.
var keyTerms = []string{"p50", "p95", "p99", "Error rate", "Throughput", "Concurrency", "Cancelled", "PASSED", "FAILED", "no assertions"}

func requireKey(t *testing.T, where, body string) {
	t.Helper()
	if !strings.Contains(body, "How to read this") {
		t.Errorf("%s has no key: %s", where, body)
		return
	}
	for _, term := range keyTerms {
		if !strings.Contains(body, term) {
			t.Errorf("%s key lacks %q", where, term)
		}
	}
}

// keySection returns the key block of an endpoint report page.
func keySection(t *testing.T, body string) string {
	t.Helper()
	i := strings.Index(body, `data-testid="reading-key"`)
	if i < 0 {
		t.Fatalf("no key on page: %s", body)
	}
	end := strings.Index(body[i:], "</details>")
	if end < 0 {
		t.Fatalf("unterminated key on page: %s", body)
	}
	return body[i : i+end]
}

func TestEndpointReport_KeyDefinesEveryTermEvenWithNoRuns(t *testing.T) {
	db := newTestDB(t)
	mux := web.NewServer(db)
	seedCollection(t, db, "Shop", 1, 0)

	body := getPath(t, mux, "/requests/Shop-epa/report").Body.String()

	requireKey(t, "endpoint report", keySection(t, body))
}

func TestEndpointReport_KeyIsTheSameTextForEveryEndpoint(t *testing.T) {
	db := newTestDB(t)
	mux := web.NewServer(db)
	seedCollection(t, db, "Shop", 2, 0)
	at := time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC)
	if err := store.SaveRun(db, model.Run{ID: "r", TestConfigID: "cfg-Shop-epb", StartedAt: at, FinishedAt: at.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}

	a := keySection(t, getPath(t, mux, "/requests/Shop-epa/report").Body.String())
	b := keySection(t, getPath(t, mux, "/requests/Shop-epb/report").Body.String())

	if a != b {
		t.Errorf("key differs between endpoints:\n%s\n---\n%s", a, b)
	}
}

func TestEndpointReport_LoginLatencyIsDefinedOnlyWhenAPoolRan(t *testing.T) {
	db := newTestDB(t)
	mux := web.NewServer(db)
	seedCollection(t, db, "Shop", 2, 0)
	at := time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC)
	pooled := model.Run{ID: "pooled", TestConfigID: "cfg-Shop-epa", StartedAt: at, FinishedAt: at.Add(time.Second),
		Login: &model.LoginSummary{PoolName: "users", Attempted: 2, Succeeded: 2}}
	plain := model.Run{ID: "plain", TestConfigID: "cfg-Shop-epb", StartedAt: at, FinishedAt: at.Add(time.Second)}
	for _, r := range []model.Run{pooled, plain} {
		if err := store.SaveRun(db, r); err != nil {
			t.Fatal(err)
		}
	}

	withPool := getPath(t, mux, "/requests/Shop-epa/report").Body.String()
	without := getPath(t, mux, "/requests/Shop-epb/report").Body.String()

	if !strings.Contains(keySection(t, withPool), "Login latency") {
		t.Errorf("key should define login latency when a pool ran")
	}
	if strings.Contains(keySection(t, without), "Login latency") {
		t.Errorf("key should not define login latency without a pool")
	}
}

func TestRunReports_AllCarryTheKey(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().UTC()
	seedReportRun(t, db, model.Run{ID: "run-key", Mode: model.ModeBurst, StartedAt: now, FinishedAt: now.Add(time.Second),
		Summary: model.RunSummary{TotalRequests: 10}, Outcome: model.OutcomePass, Passed: true})
	mux := web.NewServer(db)

	for _, path := range []string{"/runs/run-key", "/runs/run-key/report.html", "/runs/run-key/report.md"} {
		rr := getExport(t, mux, path)
		if rr.Code != http.StatusOK {
			t.Fatalf("GET %s = %d", path, rr.Code)
		}
		requireKey(t, path, rr.Body.String())
	}
}
