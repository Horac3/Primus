package store_test

import (
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Horac3/Primus/internal/model"
	"github.com/Horac3/Primus/internal/store"
)

// legacyDB builds a database as it looked before collections existed: a requests table without
// collection_id, one endpoint, one config, one run and one insight.
func legacyDB(t *testing.T) *sql.DB {
	t.Helper()
	db, cleanup := tempDB(t)
	t.Cleanup(cleanup)
	for _, s := range []string{
		`CREATE TABLE requests (id TEXT PRIMARY KEY, name TEXT NOT NULL, method TEXT NOT NULL, url_template TEXT NOT NULL, payload TEXT NOT NULL)`,
		`CREATE TABLE test_configs (id TEXT PRIMARY KEY, request_id TEXT NOT NULL, payload TEXT NOT NULL)`,
		`CREATE TABLE runs (id TEXT PRIMARY KEY, test_config_id TEXT NOT NULL, started_at TEXT NOT NULL, finished_at TEXT NOT NULL, payload TEXT NOT NULL)`,
	} {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("legacy schema: %v", err)
		}
	}
	def, _ := json.Marshal(model.RequestDefinition{ID: "r1", Name: "get", Method: "GET", URLTemplate: "http://x/a"})
	if _, err := db.Exec(`INSERT INTO requests VALUES ('r1','get','GET','http://x/a',?)`, string(def)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO test_configs VALUES ('c1','r1','{"id":"c1","request_id":"r1"}')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO runs VALUES ('run1','c1','2026-01-01T00:00:00Z','2026-01-01T00:00:01Z','{"id":"run1","test_config_id":"c1"}')`); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestMigrate_MovesLegacyEndpointsIntoMyEndpoints(t *testing.T) {
	db := legacyDB(t)

	if err := store.Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	cols, err := store.ListCollections(db)
	if err != nil {
		t.Fatalf("ListCollections: %v", err)
	}
	if len(cols) != 1 || cols[0].Name != "My endpoints" || cols[0].BuiltIn() {
		t.Fatalf("collections = %+v, want one non-built-in %q", cols, "My endpoints")
	}
	def, err := store.GetRequestDefinition(db, "r1")
	if err != nil {
		t.Fatalf("GetRequestDefinition: %v", err)
	}
	if def.CollectionID != cols[0].ID {
		t.Errorf("CollectionID = %q, want %q", def.CollectionID, cols[0].ID)
	}
	if _, err := store.GetRun(db, "run1"); err != nil {
		t.Errorf("run no longer reachable: %v", err)
	}
	if _, err := store.GetTestConfig(db, "c1"); err != nil {
		t.Errorf("config no longer reachable: %v", err)
	}
}

func TestMigrate_CollectionsIsIdempotent(t *testing.T) {
	db := legacyDB(t)
	if err := store.Migrate(db); err != nil {
		t.Fatalf("first Migrate: %v", err)
	}
	before, _ := store.ListCollections(db)

	if err := store.Migrate(db); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}

	after, _ := store.ListCollections(db)
	if len(after) != len(before) || len(after) != 1 {
		t.Errorf("collections after second Migrate = %d, want %d", len(after), len(before))
	}
}

func TestMigrate_FreshDBHasNoCollections(t *testing.T) {
	db, cleanup := tempDB(t)
	defer cleanup()
	if err := store.Migrate(db); err != nil {
		t.Fatal(err)
	}
	cols, err := store.ListCollections(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(cols) != 0 {
		t.Errorf("fresh DB has %d collections, want 0 until an endpoint needs one", len(cols))
	}
}

func migrated(t *testing.T) *sql.DB {
	t.Helper()
	db, cleanup := tempDB(t)
	t.Cleanup(cleanup)
	if err := store.Migrate(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestCreateCollection_StoresNameAndBaseURL(t *testing.T) {
	db := migrated(t)

	c, err := store.CreateCollection(db, "  Orders API ", "https://api.example.com/")
	if err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}

	got, err := store.GetCollection(db, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Orders API" {
		t.Errorf("Name = %q, want trimmed %q", got.Name, "Orders API")
	}
	if got.BaseURL != "https://api.example.com/" {
		t.Errorf("BaseURL = %q", got.BaseURL)
	}
	if got.BuiltIn() {
		t.Error("user collection must not be built-in")
	}
}

func TestCreateCollection_RejectsBadInput(t *testing.T) {
	db := migrated(t)
	if _, err := store.CreateCollection(db, "Orders", ""); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		label, name string
		want        error
	}{
		{"empty name", "", store.ErrEmptyName},
		{"blank name", "   ", store.ErrEmptyName},
		{"duplicate", "Orders", store.ErrDuplicateName},
		{"duplicate ignoring case", "orders", store.ErrDuplicateName},
	}
	for _, tt := range tests {
		t.Run(tt.label, func(t *testing.T) {
			if _, err := store.CreateCollection(db, tt.name, ""); !errors.Is(err, tt.want) {
				t.Errorf("err = %v, want %v", err, tt.want)
			}
		})
	}
	for _, base := range []string{"ftp://x", "not a url", "/relative", "http://"} {
		t.Run("base "+base, func(t *testing.T) {
			if _, err := store.CreateCollection(db, "Other-"+base, base); !errors.Is(err, store.ErrInvalidBaseURL) {
				t.Errorf("err = %v, want ErrInvalidBaseURL", err)
			}
		})
	}
}

func TestUpdateCollection_RenamesAndSetsBaseURL(t *testing.T) {
	db := migrated(t)
	a, _ := store.CreateCollection(db, "A", "")
	if _, err := store.CreateCollection(db, "B", ""); err != nil {
		t.Fatal(err)
	}

	if err := store.UpdateCollection(db, a.ID, "A2", "http://localhost:9000"); err != nil {
		t.Fatalf("UpdateCollection: %v", err)
	}
	got, _ := store.GetCollection(db, a.ID)
	if got.Name != "A2" || got.BaseURL != "http://localhost:9000" {
		t.Errorf("got %+v", got)
	}
	// Keeping its own name is not a duplicate; taking another's is.
	if err := store.UpdateCollection(db, a.ID, "a2", ""); err != nil {
		t.Errorf("renaming to own name (case change): %v", err)
	}
	if err := store.UpdateCollection(db, a.ID, "b", ""); !errors.Is(err, store.ErrDuplicateName) {
		t.Errorf("err = %v, want ErrDuplicateName", err)
	}
}

func TestSaveRequestDefinition_CollectionAssignment(t *testing.T) {
	db := migrated(t)
	c, _ := store.CreateCollection(db, "Orders", "")

	if err := store.SaveRequestDefinition(db, model.RequestDefinition{ID: "a", Name: "a", Method: "GET", URLTemplate: "http://x", CollectionID: c.ID}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRequestDefinition(db, model.RequestDefinition{ID: "b", Name: "b", Method: "GET", URLTemplate: "http://x"}); err != nil {
		t.Fatal(err)
	}

	a, _ := store.GetRequestDefinition(db, "a")
	b, _ := store.GetRequestDefinition(db, "b")
	if a.CollectionID != c.ID {
		t.Errorf("a.CollectionID = %q, want %q", a.CollectionID, c.ID)
	}
	if b.CollectionID != store.DefaultCollectionID {
		t.Errorf("b.CollectionID = %q, want default %q", b.CollectionID, store.DefaultCollectionID)
	}
	if def, err := store.GetCollection(db, store.DefaultCollectionID); err != nil || def.Name != "My endpoints" {
		t.Errorf("default collection = %+v, %v", def, err)
	}
}

func TestResolveURL(t *testing.T) {
	db := migrated(t)
	withBase, _ := store.CreateCollection(db, "With", "http://localhost:9000/")
	noBase, _ := store.CreateCollection(db, "Without", "")

	tests := []struct {
		label string
		def   model.RequestDefinition
		want  string
		err   error
	}{
		{"prefix replaced, slash joined once", model.RequestDefinition{CollectionID: withBase.ID, URLTemplate: "{{base_url}}/posts/{id}"}, "http://localhost:9000/posts/{id}", nil},
		{"no placeholder left untouched", model.RequestDefinition{CollectionID: withBase.ID, URLTemplate: "http://other/x"}, "http://other/x", nil},
		{"no placeholder needs no base", model.RequestDefinition{CollectionID: noBase.ID, URLTemplate: "http://other/x"}, "http://other/x", nil},
		{"placeholder with empty base", model.RequestDefinition{CollectionID: noBase.ID, URLTemplate: "{{base_url}}/x"}, "", store.ErrNoBaseURL},
	}
	for _, tt := range tests {
		t.Run(tt.label, func(t *testing.T) {
			got, err := store.ResolveURL(db, tt.def)
			if !errors.Is(err, tt.err) {
				t.Fatalf("err = %v, want %v", err, tt.err)
			}
			if err == nil && got.URLTemplate != tt.want {
				t.Errorf("URLTemplate = %q, want %q", got.URLTemplate, tt.want)
			}
		})
	}
	// The caller's definition is not mutated.
	in := model.RequestDefinition{CollectionID: withBase.ID, URLTemplate: "{{base_url}}/a"}
	if _, err := store.ResolveURL(db, in); err != nil || in.URLTemplate != "{{base_url}}/a" {
		t.Errorf("input mutated: %q, %v", in.URLTemplate, err)
	}
}
