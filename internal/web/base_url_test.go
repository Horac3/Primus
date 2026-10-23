package web_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Horac3/Primus/internal/store"
	"github.com/Horac3/Primus/internal/web"
)

// baseURLTarget serves /hello and counts hits on it.
func baseURLTarget(t *testing.T) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/hello" {
			http.NotFound(w, r)
			return
		}
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func TestProbe_ResolvesCollectionBaseURL(t *testing.T) {
	db := newTestDB(t)
	mux := web.NewServer(db)
	srv, hits := baseURLTarget(t)
	col, err := store.CreateCollection(db, "Local", srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	rr := postForm(t, mux, "/probe", url.Values{
		"method": {"GET"}, "url": {"{{base_url}}/hello"}, "collection_id": {col.ID},
	})

	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "HTTP 200") {
		t.Fatalf("probe = %d: %s", rr.Code, rr.Body.String())
	}
	if hits.Load() != 1 {
		t.Errorf("target hits = %d, want 1", hits.Load())
	}
}

func TestProbe_PlaceholderWithoutBaseURL_Returns400(t *testing.T) {
	db := newTestDB(t)
	mux := web.NewServer(db)
	col, _ := store.CreateCollection(db, "Empty", "")

	rr := postForm(t, mux, "/probe", url.Values{
		"method": {"GET"}, "url": {"{{base_url}}/hello"}, "collection_id": {col.ID},
	})

	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "base URL") {
		t.Errorf("probe = %d, want 400 mentioning base URL: %s", rr.Code, rr.Body.String())
	}
}

func TestRun_ResolvesBaseURLAndStoresTemplate(t *testing.T) {
	db := newTestDB(t)
	mux := web.NewServer(db)
	srv, hits := baseURLTarget(t)
	col, _ := store.CreateCollection(db, "Local", srv.URL)

	rr := postForm(t, mux, "/runs", url.Values{
		"request_id": {"ep1"}, "method": {"GET"}, "url": {"{{base_url}}/hello"}, "collection_id": {col.ID},
		"mode": {"burst"}, "concurrency": {"1"}, "total_requests": {"3"}, "burst_count": {"3"},
	})
	if rr.Code != http.StatusAccepted {
		t.Fatalf("POST /runs = %d: %s", rr.Code, rr.Body.String())
	}
	pollDone(t, mux, runIDFrom(t, rr.Body.String()))

	if hits.Load() != 3 {
		t.Errorf("target hits = %d, want 3", hits.Load())
	}
	def, err := store.GetRequestDefinition(db, "ep1")
	if err != nil {
		t.Fatal(err)
	}
	if def.URLTemplate != "{{base_url}}/hello" || def.CollectionID != col.ID {
		t.Errorf("stored def = %q in %q, want the template kept in %q", def.URLTemplate, def.CollectionID, col.ID)
	}
}
