package store_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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
		`CREATE TABLE insights (run_id TEXT PRIMARY KEY, analyzer TEXT NOT NULL, model TEXT NOT NULL DEFAULT '', status TEXT NOT NULL CHECK (status IN ('pending', 'done', 'failed')), text TEXT NOT NULL DEFAULT '', error TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL)`,
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
	if _, err := db.Exec(`INSERT INTO insights VALUES ('run1','rules','','done','ok','','2026-01-01T00:00:02Z')`); err != nil {
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
	if _, err := store.GetInsight(db, "run1"); err != nil {
		t.Errorf("insight no longer reachable: %v", err)
	}
}

func TestSaveRequestDefinition_DefaultCollectionNameClash(t *testing.T) {
	// The user deletes "My endpoints" and creates their own collection with the same name; an
	// endpoint saved without a collection must land in a collection that exists.
	db := migrated(t)
	if err := store.SaveRequestDefinition(db, model.RequestDefinition{ID: "first", Name: "first", Method: "GET", URLTemplate: "http://x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DeleteCollection(db, store.DefaultCollectionID); err != nil {
		t.Fatal(err)
	}
	own, err := store.CreateCollection(db, "my endpoints", "")
	if err != nil {
		t.Fatal(err)
	}

	if err := store.SaveRequestDefinition(db, model.RequestDefinition{ID: "second", Name: "second", Method: "GET", URLTemplate: "http://x"}); err != nil {
		t.Fatal(err)
	}

	got, _ := store.GetRequestDefinition(db, "second")
	if got.CollectionID != own.ID {
		t.Errorf("CollectionID = %q, want the user's collection %q", got.CollectionID, own.ID)
	}
	if _, err := store.GetCollection(db, got.CollectionID); err != nil {
		t.Errorf("endpoint filed under a collection that does not exist: %v", err)
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
	for _, base := range []string{"ftp://x", "not a url", "/relative", "http://", "https://user:pass@host", "https://host?k=v", "https://host/#frag"} {
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

// history fixture: collection A holds endpoints a1 and a2, collection B holds b1. Every endpoint has
// one config and two runs, and every run has an insight.
type fixture struct {
	db   *sql.DB
	a, b model.Collection
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	db := migrated(t)
	a, _ := store.CreateCollection(db, "A", "")
	b, _ := store.CreateCollection(db, "B", "")
	for _, ep := range []struct{ id, col string }{{"a1", a.ID}, {"a2", a.ID}, {"b1", b.ID}} {
		if err := store.SaveRequestDefinition(db, model.RequestDefinition{ID: ep.id, Name: ep.id, Method: "GET", URLTemplate: "http://x/" + ep.id, CollectionID: ep.col}); err != nil {
			t.Fatal(err)
		}
		cfgID := "cfg-" + ep.id
		if err := store.SaveTestConfig(db, model.TestConfig{ID: cfgID, RequestID: ep.id, Mode: model.ModeLoad, Concurrency: 1, TotalRequests: 1}); err != nil {
			t.Fatal(err)
		}
		for _, n := range []string{"1", "2"} {
			runID := "run-" + ep.id + "-" + n
			if err := store.SaveRun(db, model.Run{ID: runID, TestConfigID: cfgID}); err != nil {
				t.Fatal(err)
			}
			if _, _, err := store.StartInsight(db, runID, "rules", ""); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := store.SaveCredentialPool(db, model.CredentialPool{ID: "pool1", Name: "p"}); err != nil {
		t.Fatal(err)
	}
	return fixture{db: db, a: a, b: b}
}

func (f fixture) runExists(id string) bool {
	_, err := store.GetRun(f.db, id)
	return err == nil
}

func (f fixture) insightExists(runID string) bool {
	_, err := store.GetInsight(f.db, runID)
	return err == nil
}

func TestCountScope(t *testing.T) {
	f := newFixture(t)
	tests := []struct {
		label     string
		scope     store.Scope
		eps, runs int
	}{
		{"collection", store.Scope{Kind: store.ScopeCollection, ID: f.a.ID}, 2, 4},
		{"endpoint", store.Scope{Kind: store.ScopeEndpoint, ID: "b1"}, 1, 2},
		{"all", store.Scope{Kind: store.ScopeAll}, 3, 6},
	}
	for _, tt := range tests {
		t.Run(tt.label, func(t *testing.T) {
			got, err := store.CountScope(f.db, tt.scope)
			if err != nil {
				t.Fatal(err)
			}
			if got.Endpoints != tt.eps || got.Runs != tt.runs {
				t.Errorf("counts = %+v, want %d endpoints, %d runs", got, tt.eps, tt.runs)
			}
		})
	}
}

func TestDeleteCollection_RemovesOnlyThatCollection(t *testing.T) {
	f := newFixture(t)

	got, err := store.DeleteCollection(f.db, f.a.ID)
	if err != nil {
		t.Fatalf("DeleteCollection: %v", err)
	}

	if got.Endpoints != 2 || got.Runs != 4 {
		t.Errorf("deleted counts = %+v, want 2 endpoints, 4 runs", got)
	}
	if _, err := store.GetCollection(f.db, f.a.ID); !errors.Is(err, store.ErrNoSuchCollection) {
		t.Errorf("collection A still there: %v", err)
	}
	for _, id := range []string{"a1", "a2"} {
		if _, err := store.GetRequestDefinition(f.db, id); err == nil {
			t.Errorf("endpoint %s survived", id)
		}
		if _, err := store.GetTestConfig(f.db, "cfg-"+id); err == nil {
			t.Errorf("config of %s survived", id)
		}
		for _, n := range []string{"1", "2"} {
			if f.runExists("run-"+id+"-"+n) || f.insightExists("run-"+id+"-"+n) {
				t.Errorf("run or insight %s-%s survived", id, n)
			}
		}
	}
	// B, its history and the credential pools are untouched.
	if _, err := store.GetRequestDefinition(f.db, "b1"); err != nil {
		t.Errorf("b1 gone: %v", err)
	}
	if !f.runExists("run-b1-1") || !f.insightExists("run-b1-2") {
		t.Error("B's runs or insights were deleted")
	}
	if _, err := store.GetCredentialPoolMasked(f.db, "pool1"); err != nil {
		t.Errorf("credential pool gone: %v", err)
	}
}

func TestDeleteCollection_Tombstone(t *testing.T) {
	f := newFixture(t)
	// A built-in collection, as the seeder (C2) will create it.
	if _, err := f.db.Exec(`UPDATE collections SET seed_id = 'jsonplaceholder', seed_version = 1 WHERE id = ?`, f.b.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := store.DeleteCollection(f.db, f.a.ID); err != nil {
		t.Fatal(err)
	}
	if tomb, _ := store.Tombstoned(f.db, "jsonplaceholder"); tomb {
		t.Error("deleting a user collection wrote a tombstone")
	}
	if _, err := store.DeleteCollection(f.db, f.b.ID); err != nil {
		t.Fatal(err)
	}

	if tomb, err := store.Tombstoned(f.db, "jsonplaceholder"); err != nil || !tomb {
		t.Errorf("Tombstoned = %v, %v; want true after deleting the built-in", tomb, err)
	}
}

func TestDeleteCollection_UnknownIDIsAnError(t *testing.T) {
	f := newFixture(t)
	if _, err := store.DeleteCollection(f.db, "nope"); !errors.Is(err, store.ErrNoSuchCollection) {
		t.Errorf("err = %v, want ErrNoSuchCollection", err)
	}
}

func TestClearHistory_KeepsEndpointsAndConfigs(t *testing.T) {
	f := newFixture(t)
	tests := []struct {
		label   string
		scope   store.Scope
		cleared int
		gone    []string
		kept    []string
	}{
		{"endpoint", store.Scope{Kind: store.ScopeEndpoint, ID: "a1"}, 2, []string{"run-a1-1", "run-a1-2"}, []string{"run-a2-1", "run-b1-1"}},
		{"collection", store.Scope{Kind: store.ScopeCollection, ID: f.a.ID}, 2, []string{"run-a2-1", "run-a2-2"}, []string{"run-b1-1"}},
		{"all", store.Scope{Kind: store.ScopeAll}, 2, []string{"run-b1-1", "run-b1-2"}, nil},
	}
	// Each scope is applied in order on the same database; "cleared" is what remained in that scope.
	for _, tt := range tests {
		t.Run(tt.label, func(t *testing.T) {
			n, err := store.ClearHistory(f.db, tt.scope)
			if err != nil {
				t.Fatal(err)
			}
			if n != tt.cleared {
				t.Errorf("cleared = %d, want %d", n, tt.cleared)
			}
			for _, id := range tt.gone {
				if f.runExists(id) || f.insightExists(id) {
					t.Errorf("%s or its insight survived", id)
				}
			}
			for _, id := range tt.kept {
				if !f.runExists(id) || !f.insightExists(id) {
					t.Errorf("%s or its insight was removed", id)
				}
			}
		})
	}
	for _, id := range []string{"a1", "a2", "b1"} {
		if _, err := store.GetRequestDefinition(f.db, id); err != nil {
			t.Errorf("endpoint %s removed by clear: %v", id, err)
		}
		if _, err := store.GetTestConfig(f.db, "cfg-"+id); err != nil {
			t.Errorf("config of %s removed by clear: %v", id, err)
		}
	}
	// Nothing left: clearing again reports zero and changes nothing.
	if n, err := store.ClearHistory(f.db, store.Scope{Kind: store.ScopeAll}); err != nil || n != 0 {
		t.Errorf("second clear = %d, %v; want 0, nil", n, err)
	}
}

func TestScopeRequestIDs(t *testing.T) {
	f := newFixture(t)
	got, err := store.ScopeRequestIDs(f.db, store.Scope{Kind: store.ScopeCollection, ID: f.a.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("ids = %v, want a1 and a2", got)
	}
}

// Another process (a second handle on the file, like `primus run`) holds the write lock and then commits.
// With a deferred transaction the delete would read first and fail at once with SQLITE_BUSY_SNAPSHOT;
// BEGIN IMMEDIATE waits for the lock under busy_timeout.
func TestDeleteCollection_WaitsForWriterInAnotherProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared.db")
	a, err := store.OpenDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err := store.Migrate(a); err != nil {
		t.Fatal(err)
	}
	col, err := store.CreateCollection(a, "Doomed", "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := store.OpenDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	ctx := context.Background()
	conn, err := b.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := store.DeleteCollection(a, col.ID)
		done <- err
	}()
	time.Sleep(150 * time.Millisecond) // let the delete reach its wait for the lock
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("DeleteCollection behind another process's writer: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("DeleteCollection still blocked after the other writer committed")
	}
}

// In-process regression guard: writers sharing the one connection never see SQLITE_BUSY, and nothing is
// retried. It does not reproduce the cross-process upgrade failure; the test above covers that.
func TestDeleteAndClear_SurviveConcurrentWriters(t *testing.T) {
	f := newFixture(t) // b1 has config cfg-b1; the writer keeps adding runs to it
	stop := make(chan struct{})
	writerErr := make(chan error, 1) // reported from the test goroutine: t.Errorf after the test returns panics
	var saved atomic.Int64
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			// Never retried: the single connection queues writers, so any error is a failure.
			if err := store.SaveRun(f.db, model.Run{ID: fmt.Sprintf("w-%d", i), TestConfigID: "cfg-b1"}); err != nil {
				writerErr <- err
				return
			}
			saved.Add(1)
		}
	}()
	var stopOnce sync.Once
	join := func() {
		stopOnce.Do(func() { close(stop) })
		wg.Wait()
	}
	defer join() // also covers t.Fatal below, so the writer never outlives the test

	// Run until the writer has landed enough saves to overlap the delete and clear calls, not for a fixed
	// number of back-to-back iterations: the loop is bounded by the writer's progress.
	const wantSaves, minIters, maxIters = 20, 5, 60
	for i := 0; i < maxIters && (i < minIters || saved.Load() < wantSaves); i++ {
		select {
		case err := <-writerErr:
			t.Fatalf("writer SaveRun: %v", err)
		default:
		}
		col, err := store.CreateCollection(f.db, fmt.Sprintf("Temp%d", i), "")
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SaveRequestDefinition(f.db, model.RequestDefinition{ID: fmt.Sprintf("t%d", i), Name: "t", Method: "GET", URLTemplate: "http://x", CollectionID: col.ID}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.DeleteCollection(f.db, col.ID); err != nil {
			t.Errorf("DeleteCollection #%d: %v", i, err)
		}
		if _, err := store.ClearHistory(f.db, store.Scope{Kind: store.ScopeEndpoint, ID: "a1"}); err != nil {
			t.Errorf("ClearHistory #%d: %v", i, err)
		}
	}
	join()
	select {
	case err := <-writerErr:
		t.Errorf("writer SaveRun: %v", err)
	default:
	}
}

func TestListRequestDefinitionsByCollection_OnlyThatCollectionByName(t *testing.T) {
	db := migrated(t)
	a, _ := store.CreateCollection(db, "A", "")
	b, _ := store.CreateCollection(db, "B", "")
	for _, d := range []model.RequestDefinition{
		{ID: "a2", Name: "zeta", Method: "GET", URLTemplate: "http://x", CollectionID: a.ID},
		{ID: "a1", Name: "alpha", Method: "GET", URLTemplate: "http://x", CollectionID: a.ID},
		{ID: "b1", Name: "other", Method: "GET", URLTemplate: "http://x", CollectionID: b.ID},
	} {
		if err := store.SaveRequestDefinition(db, d); err != nil {
			t.Fatal(err)
		}
	}
	got, err := store.ListRequestDefinitionsByCollection(db, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "a1" || got[1].ID != "a2" || got[0].CollectionID != a.ID {
		t.Fatalf("got %+v", got)
	}
}

func TestLatestTestConfig_NewestSavedWins(t *testing.T) {
	db := migrated(t)
	if _, err := store.LatestTestConfig(db, "r1"); !errors.Is(err, store.ErrNoSuchConfig) {
		t.Fatalf("want ErrNoSuchConfig, got %v", err)
	}
	for _, id := range []string{"c1", "c2", "c3"} {
		if err := store.SaveTestConfig(db, model.TestConfig{ID: id, RequestID: "r1"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SaveTestConfig(db, model.TestConfig{ID: "x", RequestID: "other"}); err != nil {
		t.Fatal(err)
	}
	// Re-saving an older config makes it the newest.
	if err := store.SaveTestConfig(db, model.TestConfig{ID: "c1", RequestID: "r1"}); err != nil {
		t.Fatal(err)
	}
	got, err := store.LatestTestConfig(db, "r1")
	if err != nil || got.ID != "c1" {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestLatestRunsByRequest_NewestPerRequest(t *testing.T) {
	db := migrated(t)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, c := range []model.TestConfig{{ID: "c1", RequestID: "r1"}, {ID: "c2", RequestID: "r1"}, {ID: "c3", RequestID: "r2"}} {
		if err := store.SaveTestConfig(db, c); err != nil {
			t.Fatal(err)
		}
	}
	for _, r := range []model.Run{
		{ID: "old", TestConfigID: "c1", StartedAt: t0, FinishedAt: t0},
		{ID: "new", TestConfigID: "c2", StartedAt: t0.Add(time.Hour), FinishedAt: t0.Add(time.Hour)},
		{ID: "r2run", TestConfigID: "c3", StartedAt: t0, FinishedAt: t0},
	} {
		if err := store.SaveRun(db, r); err != nil {
			t.Fatal(err)
		}
	}
	got, err := store.LatestRunsByRequest(db, []string{"r1", "r2", "never"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got["r1"].ID != "new" || got["r2"].ID != "r2run" {
		t.Fatalf("got %+v", got)
	}
	if _, ok := got["never"]; ok {
		t.Fatal("never-run endpoint must be absent")
	}
}

func TestCreateCollectionUnique_AddsNumericSuffix(t *testing.T) {
	db := migrated(t)
	var names []string
	for i := 0; i < 3; i++ {
		c, err := store.CreateCollectionUnique(db, "Pets", "")
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, c.Name)
	}
	if names[0] != "Pets" || names[1] != "Pets 2" || names[2] != "Pets 3" {
		t.Fatalf("got %v", names)
	}
}

func TestSetCollectionExpanded_PersistsAndDefaultsOpen(t *testing.T) {
	db := migrated(t)
	c, _ := store.CreateCollection(db, "A", "")
	got, _ := store.GetCollection(db, c.ID)
	if !got.Expanded {
		t.Fatal("new collection should default to expanded")
	}
	if err := store.SetCollectionExpanded(db, c.ID, false); err != nil {
		t.Fatal(err)
	}
	got, _ = store.GetCollection(db, c.ID)
	if got.Expanded {
		t.Fatal("expected collapsed")
	}
	cols, _ := store.ListCollections(db)
	if len(cols) != 1 || cols[0].Expanded {
		t.Fatalf("list should carry the flag: %+v", cols)
	}
	if err := store.SetCollectionExpanded(db, "nope", true); !errors.Is(err, store.ErrNoSuchCollection) {
		t.Fatalf("want ErrNoSuchCollection, got %v", err)
	}
}

// A panic inside the callback must not return the only connection to the pool with the transaction open:
// later writes would never commit and the file would stay write-locked for other processes.
func TestInImmediateTx_PanicRollsBackAndFreesTheLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "panic.db")
	a, err := store.OpenDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err := store.Migrate(a); err != nil {
		t.Fatal(err)
	}
	func() {
		defer func() { _ = recover() }()
		_ = store.InImmediateTx(a, func(q store.Queryer) error {
			if _, err := q.Exec(`INSERT INTO runs (id, test_config_id, started_at, finished_at, payload) VALUES ('lost','c','','','{}')`); err != nil {
				t.Error(err)
			}
			panic("boom")
		})
	}()

	b, err := store.OpenDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if err := store.SaveRun(b, model.Run{ID: "after"}); err != nil {
		t.Fatalf("second handle write after a panicked transaction: %v", err)
	}
	if _, err := store.GetRun(a, "lost"); err == nil {
		t.Error("write from the panicked transaction was kept, want it rolled back")
	}
}
