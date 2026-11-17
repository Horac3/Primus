package store_test

import (
	"errors"
	"testing"

	"github.com/Horac3/Primus/internal/model"
	"github.com/Horac3/Primus/internal/store"
)

// FR-CLI-01: an import lands in a fresh collection, and a second import of the same spec does not
// overwrite the first.
func TestImportDefinitions_FreshCollectionEachTime(t *testing.T) {
	db := migratedDB(t)
	defs := []model.RequestDefinition{{ID: "listPets", Name: "listPets", Method: "GET", URLTemplate: "{{base_url}}/pets"}}

	first, saved, err := store.ImportDefinitions(db, "Pets", "https://api.dev.example.com", defs)
	if err != nil {
		t.Fatalf("ImportDefinitions: %v", err)
	}
	if first.BaseURL != "https://api.dev.example.com" || saved[0].ID != first.ID+"-listPets" || saved[0].CollectionID != first.ID {
		t.Errorf("collection %+v, saved %+v", first, saved[0])
	}
	second, _, err := store.ImportDefinitions(db, "Pets", "", defs)
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if second.ID == first.ID || second.Name != "Pets 2" {
		t.Errorf("second import = %+v, want a new collection named Pets 2", second)
	}
	if _, err := store.GetRequestDefinition(db, first.ID+"-listPets"); err != nil {
		t.Errorf("first import's endpoint is gone: %v", err)
	}
}

func TestFindCollection_ByIDOrName(t *testing.T) {
	db := migratedDB(t)
	c, err := store.CreateCollection(db, "Orders API", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{c.ID, "orders api", " Orders API "} {
		got, err := store.FindCollection(db, ref)
		if err != nil || got.ID != c.ID {
			t.Errorf("FindCollection(%q) = %+v, %v", ref, got, err)
		}
	}
	if _, err := store.FindCollection(db, "nope"); !errors.Is(err, store.ErrNoSuchCollection) {
		t.Errorf("unknown ref: %v, want ErrNoSuchCollection", err)
	}
}
