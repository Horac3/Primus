package store_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/Horac3/Primus/internal/model"
	"github.com/Horac3/Primus/internal/store"
)

func TestMigrate_AddsSectionsColumnToOldInsightsTable(t *testing.T) {
	db, cleanup := tempDB(t)
	defer cleanup()
	if _, err := db.Exec(`CREATE TABLE insights (
		run_id TEXT PRIMARY KEY, analyzer TEXT NOT NULL, model TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL CHECK (status IN ('pending', 'done', 'failed')),
		text TEXT NOT NULL DEFAULT '', error TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO insights VALUES ('old', 'rules', 'rules-v1', 'done', 'Old paragraph.', '', ?)`,
		time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := store.Migrate(db); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	got, err := store.GetInsight(db, "old")
	if err != nil || got.Text != "Old paragraph." || got.Sections != "" {
		t.Fatalf("legacy row = %+v, %v", got, err)
	}
}

func TestCompleteInsightSections_RoundTripsAndRestartClearsIt(t *testing.T) {
	db := migrated(t)
	if _, _, err := store.StartInsight(db, "r", "rules", "rules-v1"); err != nil {
		t.Fatal(err)
	}
	const sections = `{"numbers":["a"],"causes":["b"],"next_steps":["c"]}`
	if err := store.CompleteInsightSections(db, "r", "rules", "rules-v1", "a b c", sections); err != nil {
		t.Fatalf("CompleteInsightSections: %v", err)
	}
	got, err := store.GetInsight(db, "r")
	if err != nil || got.Status != store.InsightDone || got.Sections != sections || got.Text != "a b c" {
		t.Fatalf("insight = %+v, %v", got, err)
	}
	if _, started, err := store.StartInsight(db, "r", "rules", "rules-v1"); err != nil || !started {
		t.Fatalf("restart started=%v err=%v", started, err)
	}
	if got, _ := store.GetInsight(db, "r"); got.Sections != "" {
		t.Errorf("restarted insight kept sections %q", got.Sections)
	}
}

func TestCompleteInsight_LeavesSectionsEmpty(t *testing.T) {
	db := migrated(t)
	if _, _, err := store.StartInsight(db, "r", "rules", "rules-v1"); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteInsight(db, "r", "rules", "rules-v1", "text"); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.GetInsight(db, "r"); got.Sections != "" {
		t.Errorf("Sections = %q, want empty", got.Sections)
	}
}

func TestPreviousRunOfEndpoint(t *testing.T) {
	db := migrated(t)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, c := range []model.TestConfig{{ID: "c1", RequestID: "r1"}, {ID: "c2", RequestID: "r1"}, {ID: "c3", RequestID: "r2"}} {
		if err := store.SaveTestConfig(db, c); err != nil {
			t.Fatal(err)
		}
	}
	save := func(id, cfg string, hours, total int) {
		t.Helper()
		at := t0.Add(time.Duration(hours) * time.Hour)
		run := model.Run{ID: id, TestConfigID: cfg, StartedAt: at, FinishedAt: at}
		run.Summary.TotalRequests = total
		if err := store.SaveRun(db, run); err != nil {
			t.Fatal(err)
		}
	}
	save("first", "c1", 0, 10)
	save("empty", "c2", 1, 0) // completed nothing: never a baseline
	save("other-endpoint", "c3", 2, 10)
	save("second", "c2", 3, 10) // a different config of the same endpoint still counts
	save("third", "c1", 4, 10)

	got, err := store.PreviousRunOfEndpoint(db, "third")
	if err != nil || got == nil || got.ID != "second" {
		t.Fatalf("previous of third = %+v, %v; want second", got, err)
	}
	got, err = store.PreviousRunOfEndpoint(db, "second")
	if err != nil || got == nil || got.ID != "first" {
		t.Fatalf("previous of second = %+v, %v; want first (skipping the empty run)", got, err)
	}
	got, err = store.PreviousRunOfEndpoint(db, "first")
	if err != nil || got != nil {
		t.Fatalf("previous of first = %+v, %v; want none", got, err)
	}
	if _, err := store.PreviousRunOfEndpoint(db, fmt.Sprintf("missing-%d", 1)); err == nil {
		t.Fatal("an unknown run must be an error, not a silent no-comparison")
	}
}
