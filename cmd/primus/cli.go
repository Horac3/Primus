package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Horac3/Primus/internal/engine"
	"github.com/Horac3/Primus/internal/importer"
	"github.com/Horac3/Primus/internal/model"
	"github.com/Horac3/Primus/internal/store"
)

// isSubcommand reports whether name is a headless subcommand rather than a server flag.
func isSubcommand(name string) bool {
	switch name {
	case "run", "import", "list":
		return true
	}
	return false
}

// subcommand runs one headless subcommand and returns its exit code: 0 success (or a passing run),
// 1 a failed run, 2 a usage or config error. Flags may come before or after positional arguments.
func subcommand(ctx context.Context, name string, args []string, stdout io.Writer) int {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dbPath := fs.String("db", "primus.db", "SQLite database file path")
	var (
		collection, reportDir, allowHost *string
		colName, baseURL, defaultConfig  *string
	)
	switch name {
	case "run":
		collection = fs.String("collection", "", "run every endpoint of this collection (ID or name) with its newest saved config")
		reportDir = fs.String("report-dir", "", "write each run's JSON, Markdown report and series CSV to this directory")
		allowHost = fs.String("allow-protected-host", "", "exact hostname a heavy run may reach although it is in PRIMUS_PROTECTED_HOSTS")
	case "import":
		colName = fs.String("name", "", "collection name (default: the spec's title)")
		baseURL = fs.String("base-url", "", "collection base URL; imported paths become {{base_url}}/path")
		defaultConfig = fs.String("default-config", "", "TestConfig JSON saved for every imported endpoint (id and request_id are filled in)")
	case "list":
		collection = fs.String("collection", "", "list only this collection (ID or name)")
	}
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return 2
	}

	db, err := store.OpenDB(*dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "primus %s: open db: %v\n", name, err)
		return 2
	}
	defer db.Close()
	if err := store.Migrate(db); err != nil {
		fmt.Fprintf(os.Stderr, "primus %s: migrate: %v\n", name, err)
		return 2
	}

	switch name {
	case "run":
		if len(pos) > 1 {
			fmt.Fprintln(os.Stderr, "primus run: one config file at a time")
			return 2
		}
		policy, err := engine.LoadTargetPolicy(os.Getenv)
		if err != nil {
			fmt.Fprintf(os.Stderr, "primus run: %v\n", err)
			return 2
		}
		o := runOptions{Collection: *collection, ReportDir: *reportDir, AllowProtectedHost: *allowHost, Policy: policy}
		if len(pos) == 1 {
			o.ConfigPath = pos[0]
		}
		return runWith(ctx, o, stdout, db)
	case "import":
		if len(pos) != 1 {
			fmt.Fprintln(os.Stderr, "usage: primus import <spec file or http(s) URL> [--name <collection>] [--base-url <url>] [--default-config <config.json>] [--db <path>]")
			return 2
		}
		return importCmd(ctx, db, importOptions{Spec: pos[0], Name: *colName, BaseURL: *baseURL, DefaultConfig: *defaultConfig}, stdout)
	default:
		if len(pos) != 0 {
			fmt.Fprintln(os.Stderr, "usage: primus list [--collection <id or name>] [--db <path>]")
			return 2
		}
		return listCmd(db, *collection, stdout)
	}
}

// parseInterspersed parses flags that may appear before, between or after positional arguments,
// so "primus run cfg.json --db x.db" honours --db. The flag package alone stops at the first positional.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return pos, nil
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}

type importOptions struct {
	Spec          string // file path or http(s) URL
	Name          string
	BaseURL       string
	DefaultConfig string
}

type importedEndpoint struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Method   string `json:"method"`
	URL      string `json:"url"`
	ConfigID string `json:"config_id,omitempty"`
}

type importOutput struct {
	Collection model.Collection   `json:"collection"`
	Endpoints  []importedEndpoint `json:"endpoints"`
}

// importCmd imports an OpenAPI 2/3 spec into a new collection (FR-CLI-01). Relative paths get the
// {{base_url}} prefix so the collection's base URL decides the host, and --default-config saves the
// same test config for every endpoint so "primus run --collection" can run them (FR-CLI-02).
func importCmd(ctx context.Context, db *sql.DB, o importOptions, stdout io.Writer) int {
	var tmpl *model.TestConfig
	if o.DefaultConfig != "" {
		data, err := os.ReadFile(o.DefaultConfig)
		if err != nil {
			fmt.Fprintf(os.Stderr, "primus import: read default config: %v\n", err)
			return 2
		}
		tmpl = &model.TestConfig{}
		if err := json.Unmarshal(data, tmpl); err != nil {
			fmt.Fprintf(os.Stderr, "primus import: parse default config: %v\n", err)
			return 2
		}
		if err := engine.ValidateTestConfig(*tmpl); err != nil {
			fmt.Fprintf(os.Stderr, "primus import: invalid default config: %v\n", err)
			return 2
		}
	}

	var spec importer.Spec
	var err error
	if strings.HasPrefix(o.Spec, "http://") || strings.HasPrefix(o.Spec, "https://") {
		fctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		spec, err = importer.FetchSpec(fctx, o.Spec)
		cancel()
	} else {
		var data []byte
		if data, err = os.ReadFile(o.Spec); err == nil {
			spec, err = importer.ParseSpec(data)
		}
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "primus import: %v\n", err)
		return 2
	}
	if len(spec.Defs) == 0 {
		fmt.Fprintln(os.Stderr, "primus import: the spec has no operations")
		return 2
	}
	for i := range spec.Defs {
		if strings.HasPrefix(spec.Defs[i].URLTemplate, "/") {
			spec.Defs[i].URLTemplate = model.BaseURLPlaceholder + spec.Defs[i].URLTemplate
		}
	}
	name := spec.Title
	if strings.TrimSpace(o.Name) != "" {
		name = o.Name
	}
	col, defs, err := store.ImportDefinitions(db, name, o.BaseURL, spec.Defs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "primus import: %v\n", err)
		return 2
	}

	out := importOutput{Collection: col}
	for _, d := range defs {
		ep := importedEndpoint{ID: d.ID, Name: d.Name, Method: d.Method, URL: d.URLTemplate}
		if tmpl != nil {
			cfg := *tmpl
			cfg.ID, cfg.RequestID = "cfg-"+d.ID, d.ID
			if err := store.SaveTestConfig(db, cfg); err != nil {
				fmt.Fprintf(os.Stderr, "primus import: save config for %s: %v\n", d.Name, err)
				return 2
			}
			ep.ConfigID = cfg.ID
		}
		out.Endpoints = append(out.Endpoints, ep)
	}
	if err := encodeJSON(stdout, out); err != nil {
		fmt.Fprintf(os.Stderr, "primus import: encode output: %v\n", err)
		return 2
	}
	return 0
}

type lastRun struct {
	ID        string           `json:"id"`
	StartedAt string           `json:"started_at"`
	Passed    bool             `json:"passed"`
	Outcome   model.RunOutcome `json:"outcome"`
	P95Millis int              `json:"p95_millis"`
	ErrorRate float64          `json:"error_rate"`
}

type listedEndpoint struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Method   string   `json:"method"`
	URL      string   `json:"url"`
	ConfigID string   `json:"config_id,omitempty"`
	LastRun  *lastRun `json:"last_run,omitempty"`
}

type listedCollection struct {
	ID        string           `json:"id"`
	Name      string           `json:"name"`
	BaseURL   string           `json:"base_url,omitempty"`
	Endpoints []listedEndpoint `json:"endpoints"`
}

// listCmd prints collections, their endpoints, each endpoint's newest saved config and its latest
// run as JSON (FR-CLI-03). URLs are printed without query strings or userinfo.
func listCmd(db *sql.DB, ref string, stdout io.Writer) int {
	var cols []model.Collection
	if ref != "" {
		c, err := store.FindCollection(db, ref)
		if err != nil {
			fmt.Fprintf(os.Stderr, "primus list: %v\n", err)
			return 2
		}
		cols = []model.Collection{c}
	} else {
		var err error
		if cols, err = store.ListCollections(db); err != nil {
			fmt.Fprintf(os.Stderr, "primus list: %v\n", err)
			return 2
		}
	}
	out := []listedCollection{}
	for _, c := range cols {
		defs, err := store.ListRequestDefinitionsByCollection(db, c.ID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "primus list: %v\n", err)
			return 2
		}
		ids := make([]string, len(defs))
		for i, d := range defs {
			ids[i] = d.ID
		}
		latest, err := store.LatestRunsByRequest(db, ids)
		if err != nil {
			fmt.Fprintf(os.Stderr, "primus list: %v\n", err)
			return 2
		}
		lc := listedCollection{ID: c.ID, Name: c.Name, BaseURL: c.BaseURL, Endpoints: []listedEndpoint{}}
		for _, d := range defs {
			ep := listedEndpoint{ID: d.ID, Name: d.Name, Method: d.Method, URL: safeURL(d.URLTemplate)}
			if cfg, err := store.LatestTestConfig(db, d.ID); err == nil {
				ep.ConfigID = cfg.ID
			} else if !errors.Is(err, store.ErrNoSuchConfig) {
				fmt.Fprintf(os.Stderr, "primus list: %v\n", err)
				return 2
			}
			if r, ok := latest[d.ID]; ok {
				ep.LastRun = &lastRun{ID: r.ID, StartedAt: r.StartedAt.UTC().Format(time.RFC3339), Passed: r.Passed,
					Outcome: r.Outcome, P95Millis: r.Summary.P95Millis, ErrorRate: r.Summary.ErrorRate}
			}
			lc.Endpoints = append(lc.Endpoints, ep)
		}
		out = append(out, lc)
	}
	if err := encodeJSON(stdout, out); err != nil {
		fmt.Fprintf(os.Stderr, "primus list: encode output: %v\n", err)
		return 2
	}
	return 0
}

// safeURL drops the query, fragment and any userinfo, which may hold secrets.
func safeURL(s string) string {
	if i := strings.IndexAny(s, "?#"); i >= 0 {
		s = s[:i]
	}
	if scheme, rest, ok := strings.Cut(s, "://"); ok {
		if at := strings.Index(rest, "@"); at >= 0 && at < strings.IndexAny(rest+"/", "/") {
			rest = rest[at+1:]
		}
		s = scheme + "://" + rest
	}
	return s
}
