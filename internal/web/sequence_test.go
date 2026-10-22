package web_test

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Horac3/Primus/internal/model"
	"github.com/Horac3/Primus/internal/store"
	"github.com/Horac3/Primus/internal/web"
)

// runAllCollection makes a collection whose base URL is the target and one endpoint per name.
// Endpoints listed in noConfig get no saved test config.
func runAllCollection(t *testing.T, db *sql.DB, baseURL string, names []string, noConfig map[string]bool) model.Collection {
	t.Helper()
	col, err := store.CreateCollection(db, "Batch", baseURL)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if err := store.SaveRequestDefinition(db, model.RequestDefinition{
			ID: "ep-" + n, Name: n, Method: "GET", URLTemplate: "{{base_url}}/" + n, CollectionID: col.ID}); err != nil {
			t.Fatal(err)
		}
		if noConfig[n] {
			continue
		}
		if err := store.SaveTestConfig(db, model.TestConfig{ID: "cfg-" + n, RequestID: "ep-" + n,
			Mode: model.ModeLoad, Concurrency: 1, TotalRequests: 2}); err != nil {
			t.Fatal(err)
		}
	}
	return col
}

var sequenceIDRe = regexp.MustCompile(`data-sequence-id="([^"]+)"`)

func startRunAll(t *testing.T, mux http.Handler, col model.Collection) string {
	t.Helper()
	rr := postForm(t, mux, "/collections/"+col.ID+"/run-all", nil)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("POST run-all = %d: %s", rr.Code, rr.Body.String())
	}
	m := sequenceIDRe.FindStringSubmatch(rr.Body.String())
	if m == nil {
		t.Fatalf("no sequence id in: %s", rr.Body.String())
	}
	return m[1]
}

// waitSequence polls the sequence view until cond holds on it, and returns that view.
func waitSequence(t *testing.T, mux http.Handler, id string, cond func(body string) bool) string {
	t.Helper()
	var body string
	for i := 0; i < 200; i++ {
		body = getPath(t, mux, "/sequences/"+id).Body.String()
		if cond(body) {
			return body
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("sequence %s never reached the expected state: %s", id, body)
	return ""
}

func finished(body string) bool { return strings.Contains(body, `data-finished="true"`) }

// itemRow returns the <li> of one endpoint in the sequence view.
func itemRow(t *testing.T, body, endpointName string) string {
	t.Helper()
	for _, li := range strings.Split(body, "<li")[1:] {
		if end := strings.Index(li, "</li>"); end >= 0 && strings.Contains(li[:end], `data-endpoint="ep-`+endpointName+`"`) {
			return li[:end]
		}
	}
	t.Fatalf("no item for %s in: %s", endpointName, body)
	return ""
}

func TestRunAll_RunsEndpointsOneAfterAnotherAndSkipsUnconfigured(t *testing.T) {
	db := newTestDB(t)
	mux := web.NewServer(db)
	var inFlight, maxInFlight atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			m := maxInFlight.Load()
			if n <= m || maxInFlight.CompareAndSwap(m, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}))
	defer target.Close()
	col := runAllCollection(t, db, target.URL, []string{"a", "b", "c"}, map[string]bool{"b": true})

	id := startRunAll(t, mux, col)
	body := waitSequence(t, mux, id, finished)

	runs, err := store.ListAllRuns(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 {
		t.Fatalf("%d runs saved, want 2 (the unconfigured endpoint is skipped)", len(runs))
	}
	if got := maxInFlight.Load(); got != 1 {
		t.Errorf("max in flight = %d, want 1: endpoints must run one after another, each at concurrency 1", got)
	}
	if row := itemRow(t, body, "b"); !strings.Contains(row, `data-status="skipped"`) || !strings.Contains(row, "no saved test config") {
		t.Errorf("unconfigured endpoint should be listed as skipped with a reason: %s", row)
	}
	for _, n := range []string{"a", "c"} {
		if row := itemRow(t, body, n); !strings.Contains(row, `data-status="done"`) || !strings.Contains(row, `href="/runs/run-`) {
			t.Errorf("endpoint %s should be done and link its run: %s", n, row)
		}
	}
}

func TestRunAll_StopCancelsCurrentRunAndTheRest(t *testing.T) {
	db := newTestDB(t)
	mux := web.NewServer(db)
	srv, release := slowTarget(t)
	col := runAllCollection(t, db, srv.URL, []string{"first", "second"}, nil)

	id := startRunAll(t, mux, col)
	waitSequence(t, mux, id, func(b string) bool { return strings.Contains(b, `data-status="running"`) })
	if rr := postForm(t, mux, "/sequences/"+id+"/stop", nil); rr.Code != http.StatusOK {
		t.Fatalf("POST stop = %d: %s", rr.Code, rr.Body.String())
	}
	body := waitSequence(t, mux, id, finished)
	release()

	runs, _ := store.ListAllRuns(db)
	if len(runs) != 1 || !runs[0].Stopped {
		t.Fatalf("want exactly one stopped run, got %+v", runs)
	}
	if row := itemRow(t, body, "second"); !strings.Contains(row, `data-status="stopped"`) || strings.Contains(row, "/runs/") {
		t.Errorf("the second endpoint should be stopped without a run: %s", row)
	}
}

func TestRunAll_EmptyOrUnknownCollection(t *testing.T) {
	db := newTestDB(t)
	mux := web.NewServer(db)
	empty, _ := store.CreateCollection(db, "Empty", "")

	if rr := postForm(t, mux, "/collections/nope/run-all", nil); rr.Code != http.StatusNotFound {
		t.Errorf("unknown collection = %d, want 404", rr.Code)
	}
	if rr := postForm(t, mux, "/collections/"+empty.ID+"/run-all", nil); rr.Code != http.StatusBadRequest {
		t.Errorf("empty collection = %d, want 400", rr.Code)
	}
	if rr := getPath(t, mux, "/sequences/nope"); rr.Code != http.StatusNotFound {
		t.Errorf("unknown sequence = %d, want 404", rr.Code)
	}
}

func TestRunAll_SecondStartWhileActiveReturnsTheSameSequence(t *testing.T) {
	db := newTestDB(t)
	mux := web.NewServer(db)
	srv, release := slowTarget(t)
	col := runAllCollection(t, db, srv.URL, []string{"only"}, nil)

	first := startRunAll(t, mux, col)
	second := startRunAll(t, mux, col)
	release()
	waitSequence(t, mux, first, finished)

	if first != second {
		t.Errorf("a second run-all of an active collection started sequence %s, want %s", second, first)
	}
	if runs, _ := store.ListAllRuns(db); len(runs) != 1 {
		t.Errorf("%d runs saved, want 1", len(runs))
	}
}

func TestCollectionPage_OffersRunAllOnlyWhenThereAreEndpoints(t *testing.T) {
	db := newTestDB(t)
	mux := web.NewServer(db)
	full := runAllCollection(t, db, "http://x", []string{"a"}, nil)
	empty, _ := store.CreateCollection(db, "Hollow", "")

	withEndpoints := getPath(t, mux, "/collections/"+full.ID).Body.String()
	without := getPath(t, mux, "/collections/"+empty.ID).Body.String()

	if !strings.Contains(withEndpoints, `hx-post="/collections/`+full.ID+`/run-all"`) {
		t.Errorf("no Run all button: %s", withEndpoints)
	}
	if strings.Contains(without, "run-all") {
		t.Errorf("an empty collection offers Run all: %s", without)
	}
}
