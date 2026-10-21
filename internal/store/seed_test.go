package store_test

import (
	"errors"
	"testing"

	"github.com/Horac3/Primus/internal/model"
	"github.com/Horac3/Primus/internal/store"
)

func seedFixture() (model.Collection, []model.RequestDefinition, []model.TestConfig) {
	col := model.Collection{ID: "col-seed", Name: "Examples", BaseURL: "https://example.test", SeedID: "examples", SeedVersion: 1}
	reqs := []model.RequestDefinition{
		{ID: "seed-a", Name: "A", Method: "GET", URLTemplate: "{{base_url}}/a", Notes: "note a"},
		{ID: "seed-b", Name: "B", Method: "GET", URLTemplate: "{{base_url}}/b"},
	}
	cfgs := []model.TestConfig{
		{ID: "seed-a-cfg", RequestID: "seed-a", Mode: model.ModeLoad, Concurrency: 2, TotalRequests: 20},
		{ID: "seed-b-cfg", RequestID: "seed-b", Mode: model.ModeLoad, Concurrency: 2, TotalRequests: 20},
	}
	return col, reqs, cfgs
}

func TestSeedCollection_FreshDB_CreatesCollectionEndpointsAndConfigs(t *testing.T) {
	db := migrated(t)
	col, reqs, cfgs := seedFixture()

	res, err := store.SeedCollection(db, col, reqs, cfgs, false)
	if err != nil {
		t.Fatalf("SeedCollection: %v", err)
	}

	if res != store.SeedCreated {
		t.Fatalf("result: got %v, want SeedCreated", res)
	}
	got, err := store.GetCollection(db, "col-seed")
	if err != nil {
		t.Fatalf("GetCollection: %v", err)
	}
	if got.SeedID != "examples" || got.SeedVersion != 1 || got.BaseURL != "https://example.test" {
		t.Errorf("collection: got %+v", got)
	}
	def, err := store.GetRequestDefinition(db, "seed-a")
	if err != nil {
		t.Fatalf("GetRequestDefinition: %v", err)
	}
	if def.CollectionID != "col-seed" || def.Notes != "note a" {
		t.Errorf("endpoint: got collection %q notes %q", def.CollectionID, def.Notes)
	}
	if _, err := store.GetTestConfig(db, "seed-b-cfg"); err != nil {
		t.Errorf("GetTestConfig: %v", err)
	}
}

func TestSeedCollection_Twice_KeepsUserEdits(t *testing.T) {
	db := migrated(t)
	col, reqs, cfgs := seedFixture()
	if _, err := store.SeedCollection(db, col, reqs, cfgs, false); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateCollection(db, "col-seed", "My renamed", "https://other.test"); err != nil {
		t.Fatal(err)
	}
	edited := reqs[0]
	edited.Name = "Edited"
	edited.CollectionID = "col-seed"
	if err := store.SaveRequestDefinition(db, edited); err != nil {
		t.Fatal(err)
	}

	res, err := store.SeedCollection(db, col, reqs, cfgs, false)
	if err != nil {
		t.Fatalf("second SeedCollection: %v", err)
	}

	if res != store.SeedAlreadyPresent {
		t.Fatalf("result: got %v, want SeedAlreadyPresent", res)
	}
	got, _ := store.GetCollection(db, "col-seed")
	if got.Name != "My renamed" || got.BaseURL != "https://other.test" {
		t.Errorf("collection edits lost: %+v", got)
	}
	def, _ := store.GetRequestDefinition(db, "seed-a")
	if def.Name != "Edited" {
		t.Errorf("endpoint edit lost: %q", def.Name)
	}
}

func TestSeedCollection_AfterDelete_StaysGoneUntilRestore(t *testing.T) {
	db := migrated(t)
	col, reqs, cfgs := seedFixture()
	if _, err := store.SeedCollection(db, col, reqs, cfgs, false); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DeleteCollection(db, "col-seed"); err != nil {
		t.Fatal(err)
	}

	res, err := store.SeedCollection(db, col, reqs, cfgs, false)
	if err != nil {
		t.Fatalf("SeedCollection after delete: %v", err)
	}
	if res != store.SeedTombstoned {
		t.Fatalf("result: got %v, want SeedTombstoned", res)
	}
	if _, err := store.GetCollection(db, "col-seed"); !errors.Is(err, store.ErrNoSuchCollection) {
		t.Fatalf("collection came back: %v", err)
	}

	res, err = store.SeedCollection(db, col, reqs, cfgs, true)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if res != store.SeedCreated {
		t.Fatalf("restore result: got %v, want SeedCreated", res)
	}
	if tomb, _ := store.Tombstoned(db, "examples"); tomb {
		t.Error("tombstone not cleared by restore")
	}
	if _, err := store.GetRequestDefinition(db, "seed-b"); err != nil {
		t.Errorf("endpoint not restored: %v", err)
	}
}

func TestSeedCollection_RestoreWhilePresent_ChangesNothing(t *testing.T) {
	db := migrated(t)
	col, reqs, cfgs := seedFixture()
	if _, err := store.SeedCollection(db, col, reqs, cfgs, false); err != nil {
		t.Fatal(err)
	}

	res, err := store.SeedCollection(db, col, reqs, cfgs, true)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}

	if res != store.SeedAlreadyPresent {
		t.Fatalf("result: got %v, want SeedAlreadyPresent", res)
	}
}

func TestSeedCollection_NameTakenByUserCollection_WritesNothing(t *testing.T) {
	db := migrated(t)
	if _, err := store.CreateCollection(db, "examples", ""); err != nil {
		t.Fatal(err)
	}
	col, reqs, cfgs := seedFixture()

	_, err := store.SeedCollection(db, col, reqs, cfgs, false)

	if !errors.Is(err, store.ErrDuplicateName) {
		t.Fatalf("err: got %v, want ErrDuplicateName", err)
	}
	if _, err := store.GetRequestDefinition(db, "seed-a"); err == nil {
		t.Error("endpoint written despite the name conflict")
	}
}

func TestSeedCollection_RestoreWithEndpointMovedElsewhere_RestoresTheRest(t *testing.T) {
	db := migrated(t)
	col, reqs, cfgs := seedFixture()
	if _, err := store.SeedCollection(db, col, reqs, cfgs, false); err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateCollection(db, "Mine", "")
	if err != nil {
		t.Fatal(err)
	}
	moved := reqs[0]
	moved.Name = "Moved by user"
	moved.CollectionID = other.ID
	if err := store.SaveRequestDefinition(db, moved); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DeleteCollection(db, "col-seed"); err != nil {
		t.Fatal(err)
	}

	res, err := store.SeedCollection(db, col, reqs, cfgs, true)

	if err != nil || res != store.SeedCreated {
		t.Fatalf("restore: %v, %v", res, err)
	}
	got, _ := store.GetRequestDefinition(db, "seed-a")
	if got.CollectionID != other.ID || got.Name != "Moved by user" {
		t.Errorf("moved endpoint was disturbed: %+v", got)
	}
	back, err := store.GetRequestDefinition(db, "seed-b")
	if err != nil || back.CollectionID != "col-seed" {
		t.Errorf("other endpoint not restored: %+v, %v", back, err)
	}
}
