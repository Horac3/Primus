package web_test

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Horac3/Primus/internal/model"
	"github.com/Horac3/Primus/internal/seed"
	"github.com/Horac3/Primus/internal/store"
	"github.com/Horac3/Primus/internal/web"
)

// seedCollection fills a collection with `eps` endpoints and `runsEach` runs per endpoint.
func seedCollection(t *testing.T, db *sql.DB, name string, eps, runsEach int) model.Collection {
	t.Helper()
	col, err := store.CreateCollection(db, name, "")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < eps; i++ {
		id := name + "-ep" + string(rune('a'+i))
		if err := store.SaveRequestDefinition(db, model.RequestDefinition{ID: id, Name: id, Method: "GET", URLTemplate: "http://x/" + id, CollectionID: col.ID}); err != nil {
			t.Fatal(err)
		}
		cfg := "cfg-" + id
		if err := store.SaveTestConfig(db, model.TestConfig{ID: cfg, RequestID: id, Mode: model.ModeLoad, Concurrency: 1, TotalRequests: 1}); err != nil {
			t.Fatal(err)
		}
		for n := 0; n < runsEach; n++ {
			if err := store.SaveRun(db, model.Run{ID: "run-" + id + "-" + string(rune('0'+n)), TestConfigID: cfg}); err != nil {
				t.Fatal(err)
			}
		}
	}
	return col
}

func TestCollectionsPanel_ListsCollectionsWithBaseURL(t *testing.T) {
	db := newTestDB(t)
	mux := web.NewServer(db)
	if _, err := store.CreateCollection(db, "Orders", "http://localhost:9000"); err != nil {
		t.Fatal(err)
	}

	rr := getPath(t, mux, "/collections")

	body := rr.Body.String()
	if rr.Code != http.StatusOK || !strings.Contains(body, "Orders") || !strings.Contains(body, "http://localhost:9000") {
		t.Errorf("GET /collections = %d: %s", rr.Code, body)
	}
}

func TestCollectionsCreateAndRename(t *testing.T) {
	db := newTestDB(t)
	mux := web.NewServer(db)

	rr := postForm(t, mux, "/collections", url.Values{"name": {"Orders"}, "base_url": {"http://localhost:9000"}})
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "Orders") {
		t.Fatalf("create = %d: %s", rr.Code, rr.Body.String())
	}
	cols, _ := store.ListCollections(db)
	if len(cols) != 1 || cols[0].BaseURL != "http://localhost:9000" {
		t.Fatalf("collections = %+v", cols)
	}

	rr = postForm(t, mux, "/collections/"+cols[0].ID, url.Values{"name": {"Billing"}, "base_url": {"http://localhost:9100"}})
	if rr.Code != http.StatusOK {
		t.Fatalf("rename = %d: %s", rr.Code, rr.Body.String())
	}
	got, _ := store.GetCollection(db, cols[0].ID)
	if got.Name != "Billing" || got.BaseURL != "http://localhost:9100" {
		t.Errorf("after rename: %+v", got)
	}
}

func TestCollectionsCreateAndRename_ShowMessageOnBadInput(t *testing.T) {
	db := newTestDB(t)
	mux := web.NewServer(db)
	a, _ := store.CreateCollection(db, "A", "")
	if _, err := store.CreateCollection(db, "B", ""); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		label, path string
		form        url.Values
		want        string
	}{
		{"empty name", "/collections", url.Values{"name": {"  "}}, "required"},
		{"duplicate name", "/collections", url.Values{"name": {"b"}}, "already exists"},
		{"bad base url", "/collections", url.Values{"name": {"C"}, "base_url": {"ftp://x"}}, "http"},
		{"rename to duplicate", "/collections/" + a.ID, url.Values{"name": {"B"}}, "already exists"},
	}
	for _, tt := range tests {
		t.Run(tt.label, func(t *testing.T) {
			rr := postForm(t, mux, tt.path, tt.form)
			// htmx does not swap 4xx, so the message comes back in a 200 fragment.
			if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), tt.want) {
				t.Errorf("= %d, want 200 containing %q: %s", rr.Code, tt.want, rr.Body.String())
			}
		})
	}
	if cols, _ := store.ListCollections(db); len(cols) != 2 {
		t.Errorf("collections = %d, want the 2 originals", len(cols))
	}
}

func TestCollectionDelete_AsksWithCountsThenDeletes(t *testing.T) {
	db := newTestDB(t)
	mux := web.NewServer(db)
	col := seedCollection(t, db, "Eight", 8, 0)
	// 12 runs spread over the endpoints.
	for i := 0; i < 12; i++ {
		id := "extra-" + string(rune('a'+i))
		if err := store.SaveRun(db, model.Run{ID: id, TestConfigID: "cfg-Eight-epa"}); err != nil {
			t.Fatal(err)
		}
	}

	rr := postForm(t, mux, "/collections/"+col.ID+"/delete", url.Values{})
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "8 endpoints, 12 runs") {
		t.Fatalf("prompt = %d: %s", rr.Code, rr.Body.String())
	}
	if _, err := store.GetCollection(db, col.ID); err != nil {
		t.Fatalf("collection deleted before confirming: %v", err)
	}

	rr = postForm(t, mux, "/collections/"+col.ID+"/delete", url.Values{"confirm": {"1"}})
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "Deleted") {
		t.Fatalf("confirm = %d: %s", rr.Code, rr.Body.String())
	}
	if _, err := store.GetCollection(db, col.ID); err == nil {
		t.Error("collection still exists after confirming")
	}
	if got, _ := store.CountScope(db, store.Scope{Kind: store.ScopeAll}); got.Endpoints != 0 || got.Runs != 0 {
		t.Errorf("leftovers: %+v", got)
	}
}

func TestClearHistory_NothingToClearSaysSo(t *testing.T) {
	db := newTestDB(t)
	mux := web.NewServer(db)
	seedCollection(t, db, "Quiet", 2, 0)

	rr := postForm(t, mux, "/history/clear", url.Values{"scope": {"all"}, "confirm": {"1"}})

	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "no history to clear") {
		t.Errorf("= %d: %s", rr.Code, rr.Body.String())
	}
}

func TestClearHistory_AsksWithCountThenClearsKeepingEndpoints(t *testing.T) {
	db := newTestDB(t)
	mux := web.NewServer(db)
	col := seedCollection(t, db, "Busy", 2, 3)
	other := seedCollection(t, db, "Other", 1, 2)
	form := url.Values{"scope": {"collection"}, "id": {col.ID}}

	rr := postForm(t, mux, "/history/clear", form)
	if !strings.Contains(rr.Body.String(), "6 runs") {
		t.Fatalf("prompt should state 6 runs: %s", rr.Body.String())
	}
	if got, _ := store.CountScope(db, store.Scope{Kind: store.ScopeCollection, ID: col.ID}); got.Runs != 6 {
		t.Fatalf("cleared before confirming: %+v", got)
	}

	form.Set("confirm", "1")
	rr = postForm(t, mux, "/history/clear", form)

	if !strings.Contains(rr.Body.String(), "Cleared") {
		t.Errorf("confirm: %s", rr.Body.String())
	}
	if got, _ := store.CountScope(db, store.Scope{Kind: store.ScopeCollection, ID: col.ID}); got.Runs != 0 || got.Endpoints != 2 {
		t.Errorf("after clear: %+v, want 0 runs and 2 endpoints", got)
	}
	if got, _ := store.CountScope(db, store.Scope{Kind: store.ScopeCollection, ID: other.ID}); got.Runs != 2 {
		t.Errorf("other collection's runs touched: %+v", got)
	}
}

func TestClearHistory_RejectsUnknownScope(t *testing.T) {
	db := newTestDB(t)
	mux := web.NewServer(db)

	rr := postForm(t, mux, "/history/clear", url.Values{"scope": {"everything-please"}, "confirm": {"1"}})

	if rr.Code != http.StatusBadRequest {
		t.Errorf("= %d, want 400", rr.Code)
	}
}

// ---- active runs block delete and clear -------------------------------------

// slowTarget blocks every request until release is closed.
func slowTarget(t *testing.T) (srv *httptest.Server, release func()) {
	t.Helper()
	gate := make(chan struct{})
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-gate:
		case <-r.Context().Done():
		}
		w.WriteHeader(http.StatusOK)
	}))
	var closed bool
	release = func() {
		if !closed {
			closed = true
			close(gate)
		}
	}
	t.Cleanup(func() { release(); srv.Close() })
	return srv, release
}

func startSlowRun(t *testing.T, mux http.Handler, col model.Collection, srvURL string) string {
	t.Helper()
	rr := postForm(t, mux, "/runs", url.Values{
		"request_id": {"slow-ep"}, "method": {"GET"}, "url": {srvURL}, "collection_id": {col.ID},
		"mode": {"burst"}, "concurrency": {"1"}, "total_requests": {"1"}, "burst_count": {"1"},
	})
	if rr.Code != http.StatusAccepted {
		t.Fatalf("POST /runs = %d: %s", rr.Code, rr.Body.String())
	}
	return runIDFrom(t, rr.Body.String())
}

func TestCollectionDeleteAndClear_RefusedWhileRunActive(t *testing.T) {
	db := newTestDB(t)
	mux := web.NewServer(db)
	srv, release := slowTarget(t)
	col, _ := store.CreateCollection(db, "Live", "")
	runID := startSlowRun(t, mux, col, srv.URL)

	del := postForm(t, mux, "/collections/"+col.ID+"/delete", url.Values{"confirm": {"1"}})
	clr := postForm(t, mux, "/history/clear", url.Values{"scope": {"collection"}, "id": {col.ID}, "confirm": {"1"}})
	clrAll := postForm(t, mux, "/history/clear", url.Values{"scope": {"all"}, "confirm": {"1"}})
	clrEp := postForm(t, mux, "/history/clear", url.Values{"scope": {"endpoint"}, "id": {"slow-ep"}, "confirm": {"1"}})

	for name, rr := range map[string]*httptest.ResponseRecorder{"delete": del, "clear collection": clr, "clear all": clrAll, "clear endpoint": clrEp} {
		if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "active") {
			t.Errorf("%s = %d, want a message saying a run is active: %s", name, rr.Code, rr.Body.String())
		}
	}
	if _, err := store.GetCollection(db, col.ID); err != nil {
		t.Errorf("collection deleted despite an active run: %v", err)
	}
	if _, err := store.GetRequestDefinition(db, "slow-ep"); err != nil {
		t.Errorf("endpoint deleted despite an active run: %v", err)
	}

	// Once the run is done the same delete goes through.
	release()
	pollDone(t, mux, runID)
	del = postForm(t, mux, "/collections/"+col.ID+"/delete", url.Values{"confirm": {"1"}})
	if !strings.Contains(del.Body.String(), "Deleted") {
		t.Errorf("delete after the run finished: %s", del.Body.String())
	}
}

func TestRun_RefusedWhenItsCollectionWasDeleted(t *testing.T) {
	db := newTestDB(t)
	mux := web.NewServer(db)
	col, _ := store.CreateCollection(db, "Gone", "")
	if _, err := store.DeleteCollection(db, col.ID); err != nil {
		t.Fatal(err)
	}

	rr := postForm(t, mux, "/runs", url.Values{
		"request_id": {"orphan"}, "method": {"GET"}, "url": {"http://127.0.0.1:1/x"}, "collection_id": {col.ID},
		"mode": {"burst"}, "concurrency": {"1"}, "total_requests": {"1"}, "burst_count": {"1"},
	})

	if rr.Code != http.StatusBadRequest {
		t.Errorf("= %d, want 400: %s", rr.Code, rr.Body.String())
	}
	if _, err := store.GetRequestDefinition(db, "orphan"); err == nil {
		t.Error("an endpoint was saved into a deleted collection")
	}
}

func TestStopThenDelete_EventuallySucceeds(t *testing.T) {
	db := newTestDB(t)
	mux := web.NewServer(db)
	srv, _ := slowTarget(t)
	col, _ := store.CreateCollection(db, "Stopped", "")
	runID := startSlowRun(t, mux, col, srv.URL)
	postForm(t, mux, "/runs/"+runID+"/stop", url.Values{})

	deadline := time.Now().Add(5 * time.Second)
	for {
		rr := postForm(t, mux, "/collections/"+col.ID+"/delete", url.Values{"confirm": {"1"}})
		if strings.Contains(rr.Body.String(), "Deleted") {
			// The stopped run persisted before the delete saw it as finished, so the delete took its row
			// with it; nothing may re-insert it afterwards.
			time.Sleep(200 * time.Millisecond)
			if _, err := store.GetRun(db, runID); err == nil {
				t.Error("the stopped run's row exists after its collection was deleted")
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("delete never succeeded after Stop: %s", rr.Body.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// ---- wiring in the existing pages -------------------------------------------

func TestEndpointPanel_CollectionSelectListsAndSelectsOwn(t *testing.T) {
	db := newTestDB(t)
	mux := web.NewServer(db)
	a, _ := store.CreateCollection(db, "Alpha", "")
	b, _ := store.CreateCollection(db, "Beta", "")
	if err := store.SaveRequestDefinition(db, model.RequestDefinition{ID: "ep", Name: "ep", Method: "GET", URLTemplate: "{{base_url}}/x", CollectionID: b.ID}); err != nil {
		t.Fatal(err)
	}

	body := getPath(t, mux, "/requests/ep").Body.String()

	for _, want := range []string{`name="collection_id"`, ">Alpha<", ">Beta<", `value="` + b.ID + `" selected`} {
		if !strings.Contains(body, want) {
			t.Errorf("panel missing %q", want)
		}
	}
	if strings.Contains(body, `value="`+a.ID+`" selected`) {
		t.Error("the other collection is selected")
	}
}

func TestEndpointPanel_NewEndpointOffersCollections(t *testing.T) {
	db := newTestDB(t)
	mux := web.NewServer(db)
	if _, err := store.CreateCollection(db, "Alpha", ""); err != nil {
		t.Fatal(err)
	}

	body := getPath(t, mux, "/requests/new").Body.String()

	if !strings.Contains(body, `name="collection_id"`) || !strings.Contains(body, ">Alpha<") {
		t.Errorf("new-endpoint panel has no collection select: %s", body)
	}
}

func TestEndpointPanel_URLFieldAcceptsBasePlaceholder(t *testing.T) {
	db := newTestDB(t)
	mux := web.NewServer(db)

	body := getPath(t, mux, "/requests/new").Body.String()

	// type="url" would make the browser reject "{{base_url}}/x" before the form is sent.
	if strings.Contains(body, `type="url" name="url"`) {
		t.Error("URL input is type=url and rejects {{base_url}}")
	}
	if !strings.Contains(body, `name="url"`) {
		t.Error("URL input missing")
	}
}

func TestEndpointPanel_SavedEndpointCanClearItsHistory(t *testing.T) {
	db := newTestDB(t)
	mux := web.NewServer(db)
	if err := store.SaveRequestDefinition(db, model.RequestDefinition{ID: "ep", Name: "ep", Method: "GET", URLTemplate: "http://x"}); err != nil {
		t.Fatal(err)
	}

	saved := getPath(t, mux, "/requests/ep").Body.String()
	fresh := getPath(t, mux, "/requests/new").Body.String()

	if !strings.Contains(saved, `hx-post="/history/clear"`) || !strings.Contains(saved, `value="endpoint"`) {
		t.Error("saved endpoint has no clear-history control")
	}
	if strings.Contains(fresh, `hx-post="/history/clear"`) {
		t.Error("an unsaved endpoint offers clear history")
	}
}

func TestSidebar_LinksToCollectionsPanel(t *testing.T) {
	db := newTestDB(t)
	mux := web.NewServer(db)

	body := getPath(t, mux, "/sidebar-fragment").Body.String()

	if !strings.Contains(body, `hx-get="/collections"`) || !strings.Contains(body, `#collections-panel`) {
		t.Errorf("sidebar has no link to the collections panel: %s", body)
	}
}

func TestIndex_HasCollectionsPanelClearAllAndRefreshTriggers(t *testing.T) {
	db := newTestDB(t)
	mux := web.NewServer(db)

	body := getPath(t, mux, "/").Body.String()

	for _, want := range []string{
		`id="collections-panel"`,
		`collections-changed from:body`,
		`history-changed from:body`,
		`hx-post="/history/clear"`,
		`value="all"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("index missing %q", want)
		}
	}
}

// ---- review fixes ------------------------------------------------------------

func TestEndpointPanel_NewEndpointDefaultsToMyEndpoints(t *testing.T) {
	db := newTestDB(t)
	mux := web.NewServer(db)
	// "My endpoints" exists but sorts after "Alpha"; a browser would otherwise pick Alpha.
	if err := store.SaveRequestDefinition(db, model.RequestDefinition{ID: "x", Name: "x", Method: "GET", URLTemplate: "http://x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateCollection(db, "Alpha", ""); err != nil {
		t.Fatal(err)
	}

	body := getPath(t, mux, "/requests/new").Body.String()

	if !strings.Contains(body, `value="`+store.DefaultCollectionID+`" selected`) {
		t.Errorf("new endpoint does not preselect My endpoints: %s", body)
	}
}

func TestEndpointPanel_NewEndpointWithoutDefaultCollectionListsItFirst(t *testing.T) {
	db := newTestDB(t)
	mux := web.NewServer(db)
	if _, err := store.CreateCollection(db, "Alpha", ""); err != nil {
		t.Fatal(err)
	}

	body := getPath(t, mux, "/requests/new").Body.String()

	mine, alpha := strings.Index(body, `>My endpoints<`), strings.Index(body, ">Alpha<")
	if mine < 0 || alpha < 0 || mine > alpha {
		t.Errorf("want a leading My endpoints option before Alpha (mine=%d alpha=%d)", mine, alpha)
	}
}

func TestRun_ExistingEndpointKeepsItsCollectionWhenFormOmitsIt(t *testing.T) {
	db := newTestDB(t)
	mux := web.NewServer(db)
	srv, _ := baseURLTarget(t)
	col, _ := store.CreateCollection(db, "Keep", "")
	if err := store.SaveRequestDefinition(db, model.RequestDefinition{ID: "ep", Name: "ep", Method: "GET", URLTemplate: srv.URL + "/hello", CollectionID: col.ID}); err != nil {
		t.Fatal(err)
	}

	rr := postForm(t, mux, "/runs", url.Values{
		"request_id": {"ep"}, "method": {"GET"}, "url": {srv.URL + "/hello"},
		"mode": {"burst"}, "concurrency": {"1"}, "total_requests": {"1"}, "burst_count": {"1"},
	})
	if rr.Code != http.StatusAccepted {
		t.Fatalf("POST /runs = %d: %s", rr.Code, rr.Body.String())
	}
	pollDone(t, mux, runIDFrom(t, rr.Body.String()))

	got, _ := store.GetRequestDefinition(db, "ep")
	if got.CollectionID != col.ID {
		t.Errorf("CollectionID = %q, want it kept as %q", got.CollectionID, col.ID)
	}
}

func TestRun_DeletedCollectionMessageIsPlain(t *testing.T) {
	db := newTestDB(t)
	mux := web.NewServer(db)
	col, _ := store.CreateCollection(db, "Gone", "")
	if _, err := store.DeleteCollection(db, col.ID); err != nil {
		t.Fatal(err)
	}

	rr := postForm(t, mux, "/runs", url.Values{
		"request_id": {"o"}, "method": {"GET"}, "url": {"http://127.0.0.1:1/x"}, "collection_id": {col.ID},
		"mode": {"burst"}, "concurrency": {"1"}, "total_requests": {"1"}, "burst_count": {"1"},
	})

	if !strings.Contains(rr.Body.String(), "no longer exists") || strings.Contains(rr.Body.String(), "GetCollection") {
		t.Errorf("message = %q, want a plain 'no longer exists'", rr.Body.String())
	}
}

// Runs starting while their collection is deleted must never leave an endpoint in a missing
// collection, or a run whose config is gone. Meaningful under scripts/race.sh.
func TestRunStartVersusCollectionDelete_LeavesNoOrphans(t *testing.T) {
	db := newTestDB(t)
	mux := web.NewServer(db)
	srv, _ := baseURLTarget(t)

	var mu sync.Mutex
	var accepted []string
	var wg sync.WaitGroup
	for i := 0; i < 25; i++ {
		col, err := store.CreateCollection(db, fmt.Sprintf("Race%d", i), "")
		if err != nil {
			t.Fatal(err)
		}
		wg.Add(2)
		go func() {
			defer wg.Done()
			rr := postForm(t, mux, "/runs", url.Values{
				"request_id": {"race-" + col.ID}, "method": {"GET"}, "url": {srv.URL + "/hello"}, "collection_id": {col.ID},
				"mode": {"burst"}, "concurrency": {"1"}, "total_requests": {"1"}, "burst_count": {"1"},
			})
			if rr.Code == http.StatusAccepted {
				mu.Lock()
				accepted = append(accepted, runIDFrom(t, rr.Body.String()))
				mu.Unlock()
			}
		}()
		go func() {
			defer wg.Done()
			postForm(t, mux, "/collections/"+col.ID+"/delete", url.Values{"confirm": {"1"}})
		}()
	}
	wg.Wait()
	for _, id := range accepted {
		pollDone(t, mux, id)
	}

	for _, q := range []struct{ what, sql string }{
		{"endpoints in a missing collection", `SELECT COUNT(*) FROM requests WHERE collection_id NOT IN (SELECT id FROM collections)`},
		{"configs of a missing endpoint", `SELECT COUNT(*) FROM test_configs WHERE request_id NOT IN (SELECT id FROM requests)`},
		{"runs of a missing config", `SELECT COUNT(*) FROM runs WHERE test_config_id NOT IN (SELECT id FROM test_configs)`},
	} {
		var n int
		if err := db.QueryRow(q.sql).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("%d %s", n, q.what)
		}
	}
}

// Importing files endpoints under "My endpoints"; a delete of that collection racing the import must not
// leave them pointing at a collection that is gone. Probabilistic: it can only fail, never mislead.
func TestImportVersusDefaultCollectionDelete_LeavesNoOrphans(t *testing.T) {
	db := newTestDB(t)
	mux := web.NewServer(db)
	const spec = `{"openapi":"3.0.0","info":{"title":"t","version":"1"},"servers":[{"url":"http://x"}],"paths":{"/a":{"get":{"operationId":"op"}}}}`

	for i := 0; i < 40; i++ {
		if err := store.SaveRequestDefinition(db, model.RequestDefinition{ID: "seed", Name: "seed", Method: "GET", URLTemplate: "http://x"}); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			postForm(t, mux, "/import", url.Values{"spec_text": {spec}})
		}()
		go func() {
			defer wg.Done()
			postForm(t, mux, "/collections/"+store.DefaultCollectionID+"/delete", url.Values{"confirm": {"1"}})
		}()
		wg.Wait()

		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM requests WHERE collection_id NOT IN (SELECT id FROM collections)`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("iteration %d: %d endpoints in a missing collection", i, n)
		}
	}
}

func seededByWeb(t *testing.T, db *sql.DB) string {
	t.Helper()
	if _, err := seed.Ensure(db); err != nil {
		t.Fatal(err)
	}
	cols, _ := store.ListCollections(db)
	for _, c := range cols {
		if c.SeedID == "jsonplaceholder" {
			return c.ID
		}
	}
	t.Fatal("seeded collection missing")
	return ""
}

func TestRestoreExamples_AfterDelete_BringsCollectionBack(t *testing.T) {
	db := newTestDB(t)
	mux := web.NewServer(db)
	id := seededByWeb(t, db)
	if _, err := store.DeleteCollection(db, id); err != nil {
		t.Fatal(err)
	}

	rr := postForm(t, mux, "/collections/restore", url.Values{})

	body := rr.Body.String()
	if rr.Code != http.StatusOK || !strings.Contains(body, "restored") || !strings.Contains(body, "JSONPlaceholder") {
		t.Fatalf("restore = %d: %s", rr.Code, body)
	}
	if got := rr.Header().Get("HX-Trigger"); !strings.Contains(got, "collections-changed") {
		t.Errorf("HX-Trigger = %q, want collections-changed", got)
	}
	if _, err := store.GetCollection(db, id); err != nil {
		t.Errorf("collection not back: %v", err)
	}
}

func TestRestoreExamples_WhilePresent_ReportsAlreadyPresent(t *testing.T) {
	db := newTestDB(t)
	mux := web.NewServer(db)
	id := seededByWeb(t, db)

	rr := postForm(t, mux, "/collections/restore", url.Values{})

	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "already present") {
		t.Fatalf("restore = %d: %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("HX-Trigger") != "" {
		t.Errorf("nothing changed, but HX-Trigger = %q", rr.Header().Get("HX-Trigger"))
	}
	if _, err := store.GetCollection(db, id); err != nil {
		t.Errorf("collection gone: %v", err)
	}
}

func TestRestoreExamples_NameTaken_ShowsMessage(t *testing.T) {
	db := newTestDB(t)
	mux := web.NewServer(db)
	if _, err := store.CreateCollection(db, "JSONPlaceholder", ""); err != nil {
		t.Fatal(err)
	}

	rr := postForm(t, mux, "/collections/restore", url.Values{})

	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "already exists") {
		t.Errorf("restore = %d: %s", rr.Code, rr.Body.String())
	}
}
