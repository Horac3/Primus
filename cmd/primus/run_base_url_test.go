package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Horac3/Primus/internal/model"
	"github.com/Horac3/Primus/internal/store"
)

func TestRunCmd_ResolvesCollectionBaseURL(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			hits.Add(1)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	db := newTestDB(t)
	col, err := store.CreateCollection(db, "Local", srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRequestDefinition(db, model.RequestDefinition{
		ID: "req-bu", Name: "health", Method: "GET", URLTemplate: "{{base_url}}/health", CollectionID: col.ID,
	}); err != nil {
		t.Fatal(err)
	}
	cfgPath := writeConfigFile(t, model.TestConfig{
		ID: "cfg-bu", RequestID: "req-bu", Mode: model.ModeLoad, Concurrency: 1, TotalRequests: 3,
		Assertions: model.Assertions{ExpectedStatus: []int{200}},
	})
	var out bytes.Buffer

	code := runCmd(context.Background(), []string{cfgPath}, &out, db)

	if code != 0 {
		t.Errorf("exit code = %d, want 0; output: %s", code, out.String())
	}
	if hits.Load() != 3 {
		t.Errorf("target hits on /health = %d, want 3", hits.Load())
	}
}

func TestRunCmd_PlaceholderWithoutBaseURL_ExitsTwo(t *testing.T) {
	db := newTestDB(t)
	col, _ := store.CreateCollection(db, "Empty", "")
	if err := store.SaveRequestDefinition(db, model.RequestDefinition{
		ID: "req-nb", Name: "x", Method: "GET", URLTemplate: "{{base_url}}/x", CollectionID: col.ID,
	}); err != nil {
		t.Fatal(err)
	}
	cfgPath := writeConfigFile(t, model.TestConfig{
		ID: "cfg-nb", RequestID: "req-nb", Mode: model.ModeLoad, Concurrency: 1, TotalRequests: 1,
	})
	var out bytes.Buffer

	code := runCmd(context.Background(), []string{cfgPath}, &out, db)

	if code != 2 {
		t.Errorf("exit code = %d, want 2; output: %s", code, out.String())
	}
}
