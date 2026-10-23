// Package seed holds the built-in example collections and puts them into the store.
package seed

import (
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/Horac3/Primus/internal/model"
	"github.com/Horac3/Primus/internal/store"
)

//go:embed jsonplaceholder.json
var jsonPlaceholder []byte

// Set is one built-in collection with its endpoints and one saved test config per endpoint.
type Set struct {
	Collection model.Collection
	Requests   []model.RequestDefinition
	Configs    []model.TestConfig
}

type file struct {
	SeedID    string     `json:"seed_id"`
	Version   int        `json:"version"`
	Name      string     `json:"name"`
	BaseURL   string     `json:"base_url"`
	Endpoints []endpoint `json:"endpoints"`
}

type endpoint struct {
	Slug     string            `json:"slug"`
	Name     string            `json:"name"`
	Method   string            `json:"method"`
	Path     string            `json:"path"`
	Headers  map[string]string `json:"headers"`
	Body     string            `json:"body"`
	Status   int               `json:"status"`
	MaxP95Ms int               `json:"max_p95_ms"`
	Notes    string            `json:"notes"`
}

// Every example runs as a small load test: modest, so a shared free service is not hammered.
const (
	exampleRequests    = 20
	exampleConcurrency = 2
)

// Load parses the embedded JSONPlaceholder collection. IDs are stable so a restore recreates the same rows.
func Load() (Set, error) {
	var f file
	if err := json.Unmarshal(jsonPlaceholder, &f); err != nil {
		return Set{}, fmt.Errorf("parse jsonplaceholder seed: %w", err)
	}
	set := Set{Collection: model.Collection{
		ID: "col-seed-" + f.SeedID, Name: f.Name, BaseURL: f.BaseURL, SeedID: f.SeedID, SeedVersion: f.Version,
	}}
	for _, e := range f.Endpoints {
		id := "seed-jp-" + e.Slug
		def := model.RequestDefinition{
			ID: id, Name: e.Name, Method: e.Method,
			URLTemplate: model.BaseURLPlaceholder + e.Path,
			Headers:     e.Headers, Notes: e.Notes,
		}
		if e.Body != "" {
			def.Body = &model.BodyTemplate{Raw: []byte(e.Body)}
		}
		set.Requests = append(set.Requests, def)
		set.Configs = append(set.Configs, model.TestConfig{
			ID: id + "-cfg", RequestID: id, Mode: model.ModeLoad,
			Concurrency: exampleConcurrency, TotalRequests: exampleRequests,
			Assertions: model.Assertions{ExpectedStatus: []int{e.Status}, MaxP95Millis: e.MaxP95Ms},
		})
	}
	return set, nil
}

// Ensure seeds the built-in collection on a fresh install. It does nothing when the collection exists
// or the user deleted it.
func Ensure(db *sql.DB) (store.SeedResult, error) { return apply(db, false) }

// Restore brings the built-in collection back after the user deleted it, clearing the tombstone.
// It reports SeedAlreadyPresent and changes nothing when the collection exists.
func Restore(db *sql.DB) (store.SeedResult, error) { return apply(db, true) }

func apply(db *sql.DB, restore bool) (store.SeedResult, error) {
	set, err := Load()
	if err != nil {
		return store.SeedCreated, err
	}
	return store.SeedCollection(db, set.Collection, set.Requests, set.Configs, restore)
}
