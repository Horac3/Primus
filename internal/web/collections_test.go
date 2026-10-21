package web_test

import (
	"database/sql"
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
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("delete never succeeded after Stop: %s", rr.Body.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
}
