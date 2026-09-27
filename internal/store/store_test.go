package store_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Horac3/Primus/internal/model"
	"github.com/Horac3/Primus/internal/store"
)

// tempDB creates a temporary SQLite file and returns the *sql.DB and a cleanup func.
func tempDB(t *testing.T) (*sql.DB, func()) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	db, err := store.OpenDB(path)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	return db, func() { db.Close() }
}

// ---- OpenDB ----------------------------------------------------------------

func TestOpenDB_CreatesFile(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	path := filepath.Join(dir, "primus.db")

	// Act
	db, err := store.OpenDB(path)
	if err != nil {
		t.Fatalf("OpenDB returned error: %v", err)
	}
	defer db.Close()

	// Assert
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Errorf("expected DB file to exist at %s", path)
	}
}

// ---- Migrate ---------------------------------------------------------------

func TestMigrate_CreatesTablesOnFreshDB(t *testing.T) {
	// Arrange
	db, cleanup := tempDB(t)
	defer cleanup()

	// Act
	err := store.Migrate(db)

	// Assert
	if err != nil {
		t.Fatalf("Migrate returned error: %v", err)
	}
	for _, table := range []string{"requests", "test_configs", "runs", "global_auth"} {
		var name string
		row := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table)
		if err := row.Scan(&name); err != nil {
			t.Errorf("table %q not found after Migrate: %v", table, err)
		}
	}
}

func TestMigrate_IsIdempotent(t *testing.T) {
	// Arrange
	db, cleanup := tempDB(t)
	defer cleanup()

	// Act
	if err := store.Migrate(db); err != nil {
		t.Fatalf("first Migrate: %v", err)
	}
	err := store.Migrate(db)

	// Assert
	if err != nil {
		t.Errorf("second Migrate returned error: %v", err)
	}
}

// ---- RequestDefinition CRUD ------------------------------------------------

func TestSaveAndGetRequestDefinition(t *testing.T) {
	// Arrange
	db, cleanup := tempDB(t)
	defer cleanup()
	if err := store.Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	want := model.RequestDefinition{
		ID:          "req-1",
		Name:        "Health check",
		Method:      "GET",
		URLTemplate: "https://example.com/health",
		Headers:     map[string]string{"Accept": "application/json"},
	}

	// Act
	if err := store.SaveRequestDefinition(db, want); err != nil {
		t.Fatalf("SaveRequestDefinition: %v", err)
	}
	got, err := store.GetRequestDefinition(db, "req-1")
	if err != nil {
		t.Fatalf("GetRequestDefinition: %v", err)
	}

	// Assert
	if got.ID != want.ID {
		t.Errorf("ID: got %q, want %q", got.ID, want.ID)
	}
	if got.Name != want.Name {
		t.Errorf("Name: got %q, want %q", got.Name, want.Name)
	}
	if got.Method != want.Method {
		t.Errorf("Method: got %q, want %q", got.Method, want.Method)
	}
	if got.URLTemplate != want.URLTemplate {
		t.Errorf("URLTemplate: got %q, want %q", got.URLTemplate, want.URLTemplate)
	}
	if got.Headers["Accept"] != "application/json" {
		t.Errorf("Headers[Accept]: got %q", got.Headers["Accept"])
	}
}

func TestListRequestDefinitions(t *testing.T) {
	// Arrange
	db, cleanup := tempDB(t)
	defer cleanup()
	if err := store.Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	for _, id := range []string{"req-a", "req-b", "req-c"} {
		if err := store.SaveRequestDefinition(db, model.RequestDefinition{
			ID: id, Name: id, Method: "GET", URLTemplate: "https://x.com",
		}); err != nil {
			t.Fatalf("SaveRequestDefinition(%s): %v", id, err)
		}
	}

	// Act
	list, err := store.ListRequestDefinitions(db)

	// Assert
	if err != nil {
		t.Fatalf("ListRequestDefinitions: %v", err)
	}
	if len(list) != 3 {
		t.Errorf("len: got %d, want 3", len(list))
	}
}

// ---- GlobalAuth ------------------------------------------------------------

func TestGetGlobalAuth_EmptyDB_ReturnsZeroAuth(t *testing.T) {
	// Arrange
	db, cleanup := tempDB(t)
	defer cleanup()
	if err := store.Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	// Act
	got, err := store.GetGlobalAuth(db)

	// Assert
	if err != nil {
		t.Fatalf("GetGlobalAuth: %v", err)
	}
	if got.Type != "" && got.Type != model.AuthTypeNone {
		t.Errorf("Type: got %q, want zero/none", got.Type)
	}
}

func TestSaveAndGetGlobalAuth_RoundTrips(t *testing.T) {
	cases := []model.AuthConfig{
		{Type: model.AuthTypeNone},
		{Type: model.AuthTypeBearer, Token: "tok-123"},
		{Type: model.AuthTypeAPIKey, Header: "X-API-Key", Value: "key-abc"},
		{Type: model.AuthTypeBasic, Username: "u", Password: "p"},
	}
	for _, want := range cases {
		t.Run(string(want.Type), func(t *testing.T) {
			// Arrange
			db, cleanup := tempDB(t)
			defer cleanup()
			if err := store.Migrate(db); err != nil {
				t.Fatalf("Migrate: %v", err)
			}

			// Act
			if err := store.SaveGlobalAuth(db, want); err != nil {
				t.Fatalf("SaveGlobalAuth: %v", err)
			}
			got, err := store.GetGlobalAuth(db)
			if err != nil {
				t.Fatalf("GetGlobalAuth: %v", err)
			}

			// Assert
			if got != want {
				t.Errorf("got %+v, want %+v", got, want)
			}
		})
	}
}

func TestSaveGlobalAuth_Upsert_KeepsSingleRow(t *testing.T) {
	// Arrange
	db, cleanup := tempDB(t)
	defer cleanup()
	if err := store.Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	// Act — save three times with different values
	for _, a := range []model.AuthConfig{
		{Type: model.AuthTypeBearer, Token: "first"},
		{Type: model.AuthTypeAPIKey, Header: "X-K", Value: "second"},
		{Type: model.AuthTypeBasic, Username: "u", Password: "third"},
	} {
		if err := store.SaveGlobalAuth(db, a); err != nil {
			t.Fatalf("SaveGlobalAuth: %v", err)
		}
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM global_auth`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}

	// Assert — exactly one row, holding the last value
	if count != 1 {
		t.Errorf("row count = %d, want 1", count)
	}
	got, err := store.GetGlobalAuth(db)
	if err != nil {
		t.Fatalf("GetGlobalAuth: %v", err)
	}
	if got.Type != model.AuthTypeBasic || got.Password != "third" {
		t.Errorf("got %+v, want last-saved basic auth", got)
	}
}

// ---- TestConfig CRUD -------------------------------------------------------

func TestSaveAndGetTestConfig(t *testing.T) {
	// Arrange
	db, cleanup := tempDB(t)
	defer cleanup()
	if err := store.Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	want := model.TestConfig{
		ID:            "cfg-1",
		RequestID:     "req-1",
		Mode:          model.ModeLoad,
		Concurrency:   5,
		TotalRequests: 100,
		Assertions:    model.Assertions{ExpectedStatus: []int{200}, MaxP95Millis: 300},
	}

	// Act
	if err := store.SaveTestConfig(db, want); err != nil {
		t.Fatalf("SaveTestConfig: %v", err)
	}
	got, err := store.GetTestConfig(db, "cfg-1")
	if err != nil {
		t.Fatalf("GetTestConfig: %v", err)
	}

	// Assert
	if got.ID != want.ID {
		t.Errorf("ID: got %q, want %q", got.ID, want.ID)
	}
	if got.Mode != want.Mode {
		t.Errorf("Mode: got %q, want %q", got.Mode, want.Mode)
	}
	if got.Concurrency != want.Concurrency {
		t.Errorf("Concurrency: got %d, want %d", got.Concurrency, want.Concurrency)
	}
	if len(got.Assertions.ExpectedStatus) != 1 || got.Assertions.ExpectedStatus[0] != 200 {
		t.Errorf("Assertions.ExpectedStatus: got %v", got.Assertions.ExpectedStatus)
	}
}

// ---- Run CRUD --------------------------------------------------------------

func TestSaveAndGetRun(t *testing.T) {
	// Arrange
	db, cleanup := tempDB(t)
	defer cleanup()
	if err := store.Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	want := model.Run{
		ID:           "run-1",
		TestConfigID: "cfg-1",
		StartedAt:    now,
		FinishedAt:   now.Add(3 * time.Second),
		Summary: model.RunSummary{
			StatusCounts: map[int]int{200: 90, 500: 10},
			P50Millis:    80,
			P95Millis:    250,
			P99Millis:    400,
			ErrorRate:    0.1,
		},
		Passed: false,
	}

	// Act
	if err := store.SaveRun(db, want); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}
	got, err := store.GetRun(db, "run-1")
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}

	// Assert
	if got.ID != want.ID {
		t.Errorf("ID: got %q, want %q", got.ID, want.ID)
	}
	if !got.StartedAt.Equal(want.StartedAt) {
		t.Errorf("StartedAt: got %v, want %v", got.StartedAt, want.StartedAt)
	}
	if got.Summary.P95Millis != want.Summary.P95Millis {
		t.Errorf("P95: got %d, want %d", got.Summary.P95Millis, want.Summary.P95Millis)
	}
	if got.Passed != want.Passed {
		t.Errorf("Passed: got %v, want %v", got.Passed, want.Passed)
	}
}

func TestListRuns(t *testing.T) {
	// Arrange
	db, cleanup := tempDB(t)
	defer cleanup()
	if err := store.Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	for _, id := range []string{"run-a", "run-b"} {
		if err := store.SaveRun(db, model.Run{
			ID:           id,
			TestConfigID: "cfg-1",
			StartedAt:    now,
			FinishedAt:   now.Add(time.Second),
		}); err != nil {
			t.Fatalf("SaveRun(%s): %v", id, err)
		}
	}

	// Act
	list, err := store.ListRuns(db, "cfg-1")

	// Assert
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(list) != 2 {
		t.Errorf("len: got %d, want 2", len(list))
	}
}
