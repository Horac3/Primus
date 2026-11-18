package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Horac3/Primus/internal/store"
)

const petSpec = `{
  "openapi": "3.0.0",
  "info": {"title": "Pets", "version": "1"},
  "paths": {
    "/pets": {"get": {"operationId": "listPets", "responses": {"200": {"description": "ok"}}}},
    "/health": {"get": {"operationId": "health", "responses": {"200": {"description": "ok"}}}}
  }
}`

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func cli(t *testing.T, args ...string) (int, string) {
	t.Helper()
	var out bytes.Buffer
	code := subcommand(context.Background(), args[0], args[1:], &out)
	return code, out.String()
}

// importPets imports petSpec against target with a default config, returning the import output.
func importPets(t *testing.T, dbPath, baseURL, config string) importOutput {
	t.Helper()
	args := []string{"import", writeFile(t, "spec.json", petSpec), "--db", dbPath, "--base-url", baseURL}
	if config != "" {
		args = append(args, "--default-config", writeFile(t, "cfg.json", config))
	}
	code, out := cli(t, args...)
	if code != 0 {
		t.Fatalf("import exit %d, out: %s", code, out)
	}
	var io importOutput
	if err := json.Unmarshal([]byte(out), &io); err != nil {
		t.Fatalf("import output is not JSON: %v\n%s", err, out)
	}
	return io
}

func TestParseInterspersed_FlagsAfterPositional(t *testing.T) {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	db := fs.String("db", "primus.db", "")
	pos, err := parseInterspersed(fs, []string{"cfg.json", "--db", "other.db"})
	if err != nil {
		t.Fatal(err)
	}
	if *db != "other.db" || len(pos) != 1 || pos[0] != "cfg.json" {
		t.Errorf("db = %q, positional = %q; want other.db and [cfg.json]", *db, pos)
	}
}

// FR-CLI-01/02: import creates a collection with {{base_url}} endpoints and saves the default config.
func TestImportCmd_CreatesCollectionWithBaseURLAndConfigs(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "p.db")
	out := importPets(t, dbPath, "https://api.dev.example.com", `{"mode":"load","concurrency":1,"total_requests":3}`)

	if out.Collection.Name != "Pets" || out.Collection.BaseURL != "https://api.dev.example.com" {
		t.Errorf("collection = %+v", out.Collection)
	}
	if len(out.Endpoints) != 2 {
		t.Fatalf("endpoints = %+v, want 2", out.Endpoints)
	}
	db, err := store.OpenDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, ep := range out.Endpoints {
		if !strings.HasPrefix(ep.URL, "{{base_url}}/") {
			t.Errorf("%s url = %q, want the {{base_url}} prefix", ep.Name, ep.URL)
		}
		cfg, err := store.LatestTestConfig(db, ep.ID)
		if err != nil || cfg.ID != ep.ConfigID || cfg.TotalRequests != 3 {
			t.Errorf("%s config = %+v, %v", ep.Name, cfg, err)
		}
	}
}

func TestImportCmd_InvalidDefaultConfigExitsTwo(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "p.db")
	code, _ := cli(t, "import", writeFile(t, "spec.json", petSpec), "--db", dbPath,
		"--default-config", writeFile(t, "cfg.json", `{"mode":"load","concurrency":1}`))
	if code != 2 {
		t.Errorf("exit = %d, want 2 for a config with no stop condition", code)
	}
}

// FR-CLI-04/05: a collection run runs every configured endpoint, skips the rest, writes reports
// and exits with the worst verdict.
func TestRunCollection_RunsConfiguredEndpointsAndWritesReports(t *testing.T) {
	failPets := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failPets && r.URL.Path == "/pets" {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer target.Close()
	dbPath := filepath.Join(t.TempDir(), "p.db")
	importPets(t, dbPath, target.URL, `{"mode":"load","concurrency":1,"total_requests":3,"assertions":{"expected_status":[200]}}`)
	reports := filepath.Join(t.TempDir(), "reports")

	code, out := cli(t, "run", "--collection", "pets", "--db", dbPath, "--report-dir", reports)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; out: %s", code, out)
	}
	var res collectionOutput
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("output: %v\n%s", err, out)
	}
	if !res.Passed || len(res.Results) != 2 {
		t.Fatalf("result = %+v", res)
	}
	for _, it := range res.Results {
		if it.Status != itemPassed || it.Run == nil {
			t.Errorf("%s: %+v, want passed with a run", it.Name, it)
			continue
		}
		for _, ext := range []string{".json", ".md", ".csv"} {
			if _, err := os.Stat(filepath.Join(reports, it.Run.ID+ext)); err != nil {
				t.Errorf("report %s%s: %v", it.Run.ID, ext, err)
			}
		}
	}
	if md, _ := os.ReadFile(filepath.Join(reports, res.Results[0].Run.ID+".md")); strings.Contains(string(md), "endpoint deleted") {
		t.Errorf("markdown report should name the endpoint: %s", md)
	}

	failPets = true
	if code, out := cli(t, "run", "--collection", "Pets", "--db", dbPath); code != 1 {
		t.Errorf("with a failing endpoint: exit = %d, want 1; out: %s", code, out)
	}
}

func TestRunCollection_NothingConfiguredExitsTwo(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "p.db")
	importPets(t, dbPath, "http://127.0.0.1:1", "")
	code, out := cli(t, "run", "--collection", "Pets", "--db", dbPath)
	if code != 2 || !strings.Contains(out, "no saved test config") {
		t.Errorf("exit = %d, want 2 with every endpoint skipped; out: %s", code, out)
	}
	if code, _ := cli(t, "run", "--collection", "nope", "--db", dbPath); code != 2 {
		t.Errorf("unknown collection: exit = %d, want 2", code)
	}
}

// FR-GUARD-02/03 through the CLI: a heavy run on a protected host exits 2 and saves nothing; the
// flag or a light config lets it through.
func TestRun_ProtectedHost(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer target.Close()
	t.Setenv("PRIMUS_PROTECTED_HOSTS", "127.0.0.1")
	dbPath := filepath.Join(t.TempDir(), "p.db")
	importPets(t, dbPath, target.URL, `{"mode":"burst","concurrency":2,"total_requests":4,"burst_pattern":{"count":2,"idle":0},"assertions":{"expected_status":[200]}}`)

	if code, _ := cli(t, "run", "--collection", "Pets", "--db", dbPath); code != 2 {
		t.Errorf("heavy run on a protected host: exit = %d, want 2", code)
	}
	db, err := store.OpenDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	runs, _ := store.ListAllRuns(db)
	db.Close()
	if len(runs) != 0 {
		t.Errorf("%d runs saved for refused starts, want 0", len(runs))
	}
	if code, out := cli(t, "run", "--collection", "Pets", "--db", dbPath, "--allow-protected-host", "127.0.0.1"); code != 0 {
		t.Errorf("with --allow-protected-host: exit = %d, want 0; out: %s", code, out)
	}

	light := filepath.Join(t.TempDir(), "light.db")
	importPets(t, light, target.URL, `{"mode":"load","concurrency":1,"total_requests":2,"assertions":{"expected_status":[200]}}`)
	if code, out := cli(t, "run", "--collection", "Pets", "--db", light); code != 0 {
		t.Errorf("light run on a protected host: exit = %d, want 0; out: %s", code, out)
	}
}

// FR-CLI-03: list shows endpoints with their config and latest run.
func TestListCmd_ShowsConfigAndLastRun(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer target.Close()
	dbPath := filepath.Join(t.TempDir(), "p.db")
	importPets(t, dbPath, target.URL, `{"mode":"load","concurrency":1,"total_requests":2,"assertions":{"expected_status":[200]}}`)
	if code, out := cli(t, "run", "--collection", "Pets", "--db", dbPath); code != 0 {
		t.Fatalf("run exit %d: %s", code, out)
	}

	code, out := cli(t, "list", "--collection", "Pets", "--db", dbPath)
	if code != 0 {
		t.Fatalf("list exit %d: %s", code, out)
	}
	var cols []listedCollection
	if err := json.Unmarshal([]byte(out), &cols); err != nil {
		t.Fatalf("output: %v\n%s", err, out)
	}
	if len(cols) != 1 || len(cols[0].Endpoints) != 2 {
		t.Fatalf("list = %+v", cols)
	}
	for _, ep := range cols[0].Endpoints {
		if ep.ConfigID == "" || ep.LastRun == nil || !ep.LastRun.Passed {
			t.Errorf("%s: %+v, want a config and a passing last run", ep.Name, ep)
		}
	}
}

func TestSafeURL_DropsQueryAndUserinfo(t *testing.T) {
	cases := map[string]string{
		"https://user:pw@api.example.com/a?token=x#f": "https://api.example.com/a",
		"{{base_url}}/pets?limit=1":                   "{{base_url}}/pets",
		"https://api.example.com/users/me@x":          "https://api.example.com/users/me@x",
	}
	for in, want := range cases {
		if got := safeURL(in); got != want {
			t.Errorf("safeURL(%q) = %q, want %q", in, got, want)
		}
	}
}
