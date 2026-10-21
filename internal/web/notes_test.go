package web_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Horac3/Primus/internal/model"
	"github.com/Horac3/Primus/internal/store"
	"github.com/Horac3/Primus/internal/web"
)

func notesRunForm(extra url.Values) url.Values {
	form := url.Values{
		"request_id": {"ep-notes"}, "request_name": {"Notes"}, "method": {"GET"}, "url": {"{{base_url}}/hello"},
		"mode": {"burst"}, "concurrency": {"1"}, "total_requests": {"1"}, "burst_count": {"1"},
	}
	for k, v := range extra {
		form[k] = v
	}
	return form
}

func TestRun_SavesPostedNotes(t *testing.T) {
	db := newTestDB(t)
	mux := web.NewServer(db)
	srv, _ := baseURLTarget(t)
	col, _ := store.CreateCollection(db, "Local", srv.URL)

	rr := postForm(t, mux, "/runs", notesRunForm(url.Values{"collection_id": {col.ID}, "notes": {"  Learn from this.  "}}))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("POST /runs = %d: %s", rr.Code, rr.Body.String())
	}
	pollDone(t, mux, runIDFrom(t, rr.Body.String()))

	def, err := store.GetRequestDefinition(db, "ep-notes")
	if err != nil {
		t.Fatal(err)
	}
	if def.Notes != "Learn from this." {
		t.Errorf("Notes = %q", def.Notes)
	}
}

func TestRun_FormWithoutNotes_KeepsExistingNotes(t *testing.T) {
	db := newTestDB(t)
	mux := web.NewServer(db)
	srv, _ := baseURLTarget(t)
	col, _ := store.CreateCollection(db, "Local", srv.URL)
	first := postForm(t, mux, "/runs", notesRunForm(url.Values{"collection_id": {col.ID}, "notes": {"keep me"}}))
	pollDone(t, mux, runIDFrom(t, first.Body.String()))

	rr := postForm(t, mux, "/runs", notesRunForm(url.Values{"collection_id": {col.ID}}))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("POST /runs = %d: %s", rr.Code, rr.Body.String())
	}
	pollDone(t, mux, runIDFrom(t, rr.Body.String()))

	def, _ := store.GetRequestDefinition(db, "ep-notes")
	if def.Notes != "keep me" {
		t.Errorf("Notes = %q, want the earlier note kept", def.Notes)
	}
}

func TestRequestPanel_ShowsNotesInEditor(t *testing.T) {
	db := newTestDB(t)
	mux := web.NewServer(db)
	col, _ := store.CreateCollection(db, "Local", "")
	if err := store.SaveRequestDefinition(db, modelDef("ep-n", col.ID, "A <b>note</b>")); err != nil {
		t.Fatal(err)
	}

	body := getPath(t, mux, "/requests/ep-n").Body.String()

	if !strings.Contains(body, `name="notes"`) || !strings.Contains(body, "A &lt;b&gt;note&lt;/b&gt;") {
		t.Errorf("panel lacks an escaped notes field: %s", body)
	}
}

func modelDef(id, collectionID, notes string) model.RequestDefinition {
	return model.RequestDefinition{ID: id, Name: id, Method: "GET", URLTemplate: "http://x/" + id, CollectionID: collectionID, Notes: notes}
}

func TestRun_EmptyPostedNotes_ClearsExistingNotes(t *testing.T) {
	db := newTestDB(t)
	mux := web.NewServer(db)
	srv, _ := baseURLTarget(t)
	col, _ := store.CreateCollection(db, "Local", srv.URL)
	first := postForm(t, mux, "/runs", notesRunForm(url.Values{"collection_id": {col.ID}, "notes": {"old"}}))
	pollDone(t, mux, runIDFrom(t, first.Body.String()))

	rr := postForm(t, mux, "/runs", notesRunForm(url.Values{"collection_id": {col.ID}, "notes": {""}}))
	pollDone(t, mux, runIDFrom(t, rr.Body.String()))

	def, _ := store.GetRequestDefinition(db, "ep-notes")
	if def.Notes != "" {
		t.Errorf("Notes = %q, want cleared", def.Notes)
	}
}
