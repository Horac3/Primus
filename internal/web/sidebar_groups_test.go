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

// sidebarGroup returns the markup of one collection's group in the sidebar.
func sidebarGroup(t *testing.T, body, colID string) string {
	t.Helper()
	marker := `data-collection="` + colID + `"`
	i := strings.Index(body, marker)
	if i < 0 {
		t.Fatalf("no group for %s in sidebar: %s", colID, body)
	}
	rest := body[strings.LastIndex(body[:i], "<details"):]
	if j := strings.Index(rest[len(marker):], "</details>"); j >= 0 {
		rest = rest[:len(marker)+j]
	}
	return rest
}

func TestSidebar_GroupsEndpointsByCollection(t *testing.T) {
	db := newTestDB(t)
	mux := web.NewServer(db)
	a := seedCollection(t, db, "Alpha", 2, 0)
	b := seedCollection(t, db, "Beta", 1, 0)

	body := getPath(t, mux, "/sidebar-fragment").Body.String()

	ga, gb := sidebarGroup(t, body, a.ID), sidebarGroup(t, body, b.ID)
	for _, want := range []string{"Alpha-epa", "Alpha-epb"} {
		if !strings.Contains(ga, want) {
			t.Errorf("Alpha group lacks %s", want)
		}
	}
	if strings.Contains(ga, "Beta-epa") || !strings.Contains(gb, "Beta-epa") {
		t.Errorf("endpoints are not split per collection: alpha=%s beta=%s", ga, gb)
	}
	if !strings.Contains(ga, "open") {
		t.Error("collections start expanded")
	}
}

func TestSidebar_ExpandedStateSurvivesReload(t *testing.T) {
	db := newTestDB(t)
	mux := web.NewServer(db)
	a := seedCollection(t, db, "Alpha", 1, 0)
	b := seedCollection(t, db, "Beta", 1, 0)

	rr := postForm(t, mux, "/collections/"+a.ID+"/expanded", url.Values{"open": {"0"}})
	if rr.Code != http.StatusNoContent {
		t.Fatalf("POST expanded = %d: %s", rr.Code, rr.Body.String())
	}

	body := getPath(t, mux, "/sidebar-fragment").Body.String()
	if strings.Contains(sidebarGroup(t, body, a.ID), "<details open") {
		t.Error("collapsed collection renders open after reload")
	}
	if !strings.Contains(sidebarGroup(t, body, b.ID), "<details open") {
		t.Error("untouched collection should stay open")
	}

	postForm(t, mux, "/collections/"+a.ID+"/expanded", url.Values{"open": {"1"}})
	body = getPath(t, mux, "/sidebar-fragment").Body.String()
	if !strings.Contains(sidebarGroup(t, body, a.ID), "<details open") {
		t.Error("re-expanded collection renders closed")
	}
}

func TestSidebar_ExpandedUnknownCollectionIs404(t *testing.T) {
	mux := web.NewServer(newTestDB(t))
	if rr := postForm(t, mux, "/collections/nope/expanded", url.Values{"open": {"0"}}); rr.Code != http.StatusNotFound {
		t.Errorf("status %d, want 404", rr.Code)
	}
}

func TestSidebar_EmptyCollectionOffersAddEndpoint(t *testing.T) {
	db := newTestDB(t)
	mux := web.NewServer(db)
	empty, err := store.CreateCollection(db, "Empty", "")
	if err != nil {
		t.Fatal(err)
	}

	g := sidebarGroup(t, getPath(t, mux, "/sidebar-fragment").Body.String(), empty.ID)

	if !strings.Contains(g, "no endpoints yet") {
		t.Errorf("empty collection has no placeholder: %s", g)
	}
	if !strings.Contains(g, `hx-get="/requests/new?collection=`+empty.ID+`"`) {
		t.Errorf("empty collection has no add-endpoint control: %s", g)
	}
}

func TestNewEndpointPanel_PreselectsCollection(t *testing.T) {
	db := newTestDB(t)
	mux := web.NewServer(db)
	if err := store.SaveRequestDefinition(db, model.RequestDefinition{ID: "x", Name: "x", Method: "GET", URLTemplate: "http://x"}); err != nil {
		t.Fatal(err)
	}
	col, _ := store.CreateCollection(db, "Target", "")

	body := getPath(t, mux, "/requests/new?collection="+col.ID).Body.String()

	if !strings.Contains(body, `value="`+col.ID+`" selected`) {
		t.Errorf("collection not preselected: %s", body)
	}
}
