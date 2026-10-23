package web_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Horac3/Primus/internal/model"
	"github.com/Horac3/Primus/internal/store"
	"github.com/Horac3/Primus/internal/web"
)

func TestEndpointReport_UnknownEndpointIs404(t *testing.T) {
	mux := web.NewServer(newTestDB(t))
	if rr := getPath(t, mux, "/requests/nope/report"); rr.Code != http.StatusNotFound {
		t.Errorf("status %d, want 404", rr.Code)
	}
}

func TestEndpointReport_HeaderShowsMethodURLWithoutQueryAndCollection(t *testing.T) {
	db := newTestDB(t)
	mux := web.NewServer(db)
	col := seedCollection(t, db, "Shop", 1, 0)
	def := model.RequestDefinition{ID: "Shop-epa", Name: "Shop-epa", Method: "POST",
		URLTemplate: "http://x/orders?token=hunter2", CollectionID: col.ID}
	if err := store.SaveRequestDefinition(db, def); err != nil {
		t.Fatal(err)
	}

	rr := getPath(t, mux, "/requests/Shop-epa/report")

	body := rr.Body.String()
	if rr.Code != http.StatusOK {
		t.Fatalf("GET report = %d: %s", rr.Code, body)
	}
	for _, want := range []string{"POST", "http://x/orders", "Shop"} {
		if !strings.Contains(body, want) {
			t.Errorf("report lacks %q: %s", want, body)
		}
	}
	if strings.Contains(body, "hunter2") || strings.Contains(body, "token=") {
		t.Errorf("report leaks the URL query: %s", body)
	}
}

func TestEndpointReport_NoRunsSaysSoAndOffersTheBuilder(t *testing.T) {
	db := newTestDB(t)
	mux := web.NewServer(db)
	seedCollection(t, db, "Shop", 1, 0)

	body := getPath(t, mux, "/requests/Shop-epa/report").Body.String()

	if !strings.Contains(body, "no runs yet") {
		t.Errorf("empty report should say so: %s", body)
	}
	if !strings.Contains(body, `hx-get="/requests/Shop-epa"`) {
		t.Errorf("empty report should offer a run button that opens the builder: %s", body)
	}
}

func TestEndpointReport_TrendTableHoldsLastTenRunsNewestFirst(t *testing.T) {
	db := newTestDB(t)
	mux := web.NewServer(db)
	seedCollection(t, db, "Shop", 1, 0)
	t0 := time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 12; i++ {
		at := t0.Add(time.Duration(i) * time.Hour)
		run := model.Run{ID: fmt.Sprintf("run-%02d", i), TestConfigID: "cfg-Shop-epa", StartedAt: at, FinishedAt: at.Add(2 * time.Second),
			Outcome: model.OutcomePass, Passed: true,
			Summary: model.RunSummary{TotalRequests: 100, P50Millis: 10 + i, P95Millis: 200 + i, P99Millis: 300 + i, ErrorRate: 0.05, Cancelled: i}}
		if err := store.SaveRun(db, run); err != nil {
			t.Fatal(err)
		}
	}

	body := getPath(t, mux, "/requests/Shop-epa/report").Body.String()

	if got := strings.Count(body, `data-testid="trend-row"`); got != 10 {
		t.Fatalf("trend table has %d rows, want 10: %s", got, body)
	}
	if strings.Contains(body, "run-00") || strings.Contains(body, "run-01") {
		t.Errorf("trend table includes runs older than the last ten: %s", body)
	}
	if strings.Index(body, `href="/runs/run-11"`) > strings.Index(body, `href="/runs/run-10"`) {
		t.Errorf("trend table is not newest first")
	}
	row := tableRow(t, body, "/runs/run-05")
	for _, want := range []string{"2026-03-04 05:00", "100", "15 ms", "205 ms", "305 ms", "5.0%", "50.0", ">5<", "PASSED"} {
		if !strings.Contains(row, want) {
			t.Errorf("run-05 row lacks %q: %s", want, row)
		}
	}
}

func TestEndpointReport_LatestSummaryShowsNewestRunVerdictAndThroughput(t *testing.T) {
	db := newTestDB(t)
	mux := web.NewServer(db)
	seedCollection(t, db, "Shop", 1, 0)
	t0 := time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC)
	for _, r := range []model.Run{
		{ID: "run-old", TestConfigID: "cfg-Shop-epa", StartedAt: t0, FinishedAt: t0.Add(time.Second), Outcome: model.OutcomePass, Passed: true,
			Summary: model.RunSummary{TotalRequests: 10}},
		{ID: "run-new", TestConfigID: "cfg-Shop-epa", StartedAt: t0.Add(time.Hour), FinishedAt: t0.Add(time.Hour + 4*time.Second), Outcome: model.OutcomeFail,
			Summary: model.RunSummary{TotalRequests: 80}},
	} {
		if err := store.SaveRun(db, r); err != nil {
			t.Fatal(err)
		}
	}

	body := getPath(t, mux, "/requests/Shop-epa/report").Body.String()

	i := strings.Index(body, `data-testid="latest-run"`)
	if i < 0 {
		t.Fatalf("no latest-run section: %s", body)
	}
	latest := body[i:]
	if end := strings.Index(latest, `data-testid="trend"`); end >= 0 {
		latest = latest[:end]
	}
	for _, want := range []string{"FAILED", "20.0", `href="/runs/run-new"`} {
		if !strings.Contains(latest, want) {
			t.Errorf("latest-run section lacks %q: %s", want, latest)
		}
	}
}

func TestEndpointReport_FindingsShowTheLatestRunsInsightOnly(t *testing.T) {
	db := newTestDB(t)
	mux := web.NewServer(db)
	seedCollection(t, db, "Shop", 1, 0)
	t0 := time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC)
	for i, id := range []string{"run-old", "run-new"} {
		at := t0.Add(time.Duration(i) * time.Hour)
		if err := store.SaveRun(db, model.Run{ID: id, TestConfigID: "cfg-Shop-epa", StartedAt: at, FinishedAt: at}); err != nil {
			t.Fatal(err)
		}
		if _, _, err := store.StartInsight(db, id, "rules", "none"); err != nil {
			t.Fatal(err)
		}
		if err := store.CompleteInsight(db, id, "rules", "none", "finding for "+id); err != nil {
			t.Fatal(err)
		}
	}

	body := getPath(t, mux, "/requests/Shop-epa/report").Body.String()

	if !strings.Contains(body, "finding for run-new") {
		t.Errorf("report lacks the latest run's finding: %s", body)
	}
	if strings.Contains(body, "finding for run-old") {
		t.Errorf("report shows an older run's finding: %s", body)
	}
}

func TestEndpointReport_HeaderLeaksNoUserinfoQueryOrFragment(t *testing.T) {
	db := newTestDB(t)
	mux := web.NewServer(db)
	col, err := store.CreateCollection(db, "Shop", "http://base.test/api")
	if err != nil {
		t.Fatal(err)
	}
	for id, tmpl := range map[string]string{
		"direct":  "http://u:hunter2@x.test/orders?token=URLSECRET#frag",
		"viabase": "{{base_url}}/orders?token=URLSECRET#frag",
	} {
		def := model.RequestDefinition{ID: id, Name: id, Method: "GET", URLTemplate: tmpl, CollectionID: col.ID}
		if err := store.SaveRequestDefinition(db, def); err != nil {
			t.Fatal(err)
		}
	}

	for id, want := range map[string]string{"direct": "http://x.test/orders", "viabase": "http://base.test/api/orders"} {
		body := getPath(t, mux, "/requests/"+id+"/report").Body.String()

		if !strings.Contains(body, want) {
			t.Errorf("report of %s lacks the cleaned URL %q: %s", id, want, body)
		}
		for _, secret := range []string{"hunter2", "u:", "URLSECRET", "token=", "#frag"} {
			if strings.Contains(body, secret) {
				t.Errorf("report of %s leaks %q: %s", id, secret, body)
			}
		}
	}
}
