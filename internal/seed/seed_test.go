package seed_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Horac3/Primus/internal/model"
	"github.com/Horac3/Primus/internal/seed"
	"github.com/Horac3/Primus/internal/store"
)

func TestLoad_JSONPlaceholder_MatchesSpec(t *testing.T) {
	set, err := seed.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if set.Collection.Name != "JSONPlaceholder" || set.Collection.BaseURL != "https://jsonplaceholder.typicode.com" {
		t.Errorf("collection: got %+v", set.Collection)
	}
	if set.Collection.SeedID == "" || set.Collection.SeedVersion < 1 {
		t.Errorf("collection is not marked built in: %+v", set.Collection)
	}
	if n := len(set.Requests); n < 20 || n > 22 {
		t.Fatalf("endpoints: got %d, want about 20", n)
	}
	if len(set.Configs) != len(set.Requests) {
		t.Fatalf("configs: got %d, want one per endpoint (%d)", len(set.Configs), len(set.Requests))
	}
	cfgByReq := map[string]model.TestConfig{}
	for _, c := range set.Configs {
		cfgByReq[c.RequestID] = c
	}
	ids := map[string]bool{}
	byPath := map[string]model.RequestDefinition{}
	for _, r := range set.Requests {
		if ids[r.ID] {
			t.Errorf("duplicate endpoint ID %q", r.ID)
		}
		ids[r.ID] = true
		byPath[r.Method+" "+strings.TrimPrefix(r.URLTemplate, model.BaseURLPlaceholder)] = r
		if !strings.HasPrefix(r.URLTemplate, model.BaseURLPlaceholder+"/") {
			t.Errorf("%s: URL %q does not start with {{base_url}}/", r.ID, r.URLTemplate)
		}
		if r.Notes == "" {
			t.Errorf("%s: empty note", r.ID)
		}
		c, ok := cfgByReq[r.ID]
		if !ok {
			t.Errorf("%s: no saved config", r.ID)
			continue
		}
		if c.Mode != model.ModeLoad || c.TotalRequests != 20 || c.Concurrency != 2 {
			t.Errorf("%s: config %s/%d/%d, want load/20/2", r.ID, c.Mode, c.TotalRequests, c.Concurrency)
		}
		if len(c.Assertions.ExpectedStatus) != 1 || c.Assertions.MaxP95Millis == 0 {
			t.Errorf("%s: assertions %+v", r.ID, c.Assertions)
		}
	}

	wantStatus := map[string]int{
		"GET /posts/9999": 404, "POST /posts": 201,
		"GET /posts": 200, "PUT /posts/1": 200, "PATCH /posts/1": 200, "DELETE /posts/1": 200,
	}
	for key, want := range wantStatus {
		r, ok := byPath[key]
		if !ok {
			t.Errorf("missing endpoint %q", key)
			continue
		}
		if got := cfgByReq[r.ID].Assertions.ExpectedStatus; len(got) != 1 || got[0] != want {
			t.Errorf("%s: expected status %v, want [%d]", key, got, want)
		}
	}
	for _, key := range []string{
		"GET /posts/1/comments", "GET /users/1/posts", "GET /comments?postId=1", "GET /todos?completed=false",
		"GET /comments", "GET /albums", "GET /photos", "GET /todos", "GET /users",
	} {
		if _, ok := byPath[key]; !ok {
			t.Errorf("missing endpoint %q", key)
		}
	}
}

func TestLoad_Photos_HasHigherLatencyLimitAndSizeNote(t *testing.T) {
	set, _ := seed.Load()
	var photoID string
	for _, r := range set.Requests {
		if r.Method == "GET" && r.URLTemplate == "{{base_url}}/photos" {
			photoID = r.ID
			if !strings.Contains(strings.ToLower(r.Notes), "payload") {
				t.Errorf("photos note does not mention payload size: %q", r.Notes)
			}
		}
	}
	var photosP95, otherMax int
	for _, c := range set.Configs {
		if c.RequestID == photoID {
			photosP95 = c.Assertions.MaxP95Millis
		} else if c.Assertions.MaxP95Millis > otherMax {
			otherMax = c.Assertions.MaxP95Millis
		}
	}

	if photosP95 == 0 || photosP95 <= otherMax {
		t.Errorf("photos p95 limit %d should exceed every other (%d)", photosP95, otherMax)
	}
}

func TestLoad_Writes_SayTheServerFakesThem(t *testing.T) {
	set, _ := seed.Load()
	writes := 0
	for _, r := range set.Requests {
		if r.Method == "GET" {
			continue
		}
		writes++
		if !strings.Contains(strings.ToLower(r.Notes), "fake") {
			t.Errorf("%s %s: note should say the write is faked: %q", r.Method, r.URLTemplate, r.Notes)
		}
		if (r.Method == "POST" || r.Method == "PUT" || r.Method == "PATCH") && r.Body == nil {
			t.Errorf("%s %s: no body", r.Method, r.URLTemplate)
		}
	}
	if writes != 4 {
		t.Errorf("writes: got %d, want 4", writes)
	}
}

func TestEnsure_DeleteThenEnsure_StaysGone_RestoreBringsItBack(t *testing.T) {
	db, err := store.OpenDB(filepath.Join(t.TempDir(), "seed.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := store.Migrate(db); err != nil {
		t.Fatal(err)
	}

	res, err := seed.Ensure(db)
	if err != nil || res != store.SeedCreated {
		t.Fatalf("first Ensure: %v, %v", res, err)
	}
	cols, _ := store.ListCollections(db)
	var id string
	for _, c := range cols {
		if c.SeedID == "jsonplaceholder" {
			id = c.ID
		}
	}
	if id == "" {
		t.Fatal("seeded collection not listed")
	}
	if res, _ := seed.Ensure(db); res != store.SeedAlreadyPresent {
		t.Errorf("second Ensure: got %v, want SeedAlreadyPresent", res)
	}

	if _, err := store.DeleteCollection(db, id); err != nil {
		t.Fatal(err)
	}
	if res, _ := seed.Ensure(db); res != store.SeedTombstoned {
		t.Errorf("Ensure after delete: got %v, want SeedTombstoned", res)
	}
	res, err = seed.Restore(db)
	if err != nil || res != store.SeedCreated {
		t.Fatalf("Restore: %v, %v", res, err)
	}
	if res, _ := seed.Restore(db); res != store.SeedAlreadyPresent {
		t.Errorf("Restore while present: got %v, want SeedAlreadyPresent", res)
	}
	reqs, _ := store.ListRequestDefinitions(db)
	if len(reqs) < 20 {
		t.Errorf("endpoints after restore: %d", len(reqs))
	}
}
