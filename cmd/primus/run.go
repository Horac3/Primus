package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/Horac3/Primus/internal/engine"
	"github.com/Horac3/Primus/internal/model"
	"github.com/Horac3/Primus/internal/store"
	"github.com/Horac3/Primus/internal/web"
)

// runOptions are the inputs of "primus run". Exactly one of ConfigPath and Collection is set.
type runOptions struct {
	ConfigPath string
	Collection string // collection ID or name: run every endpoint with its newest saved config
	ReportDir  string // when set, each run's JSON, Markdown report and series CSV are written here
	// AllowProtectedHost fills allow_protected_host on configs that leave it empty (D-021).
	AllowProtectedHost string
	Policy             engine.TargetPolicy
}

// runCmd is the testable core of the "primus run" subcommand for a single config file.
// args[0] must be the path to a TestConfig JSON file.
// Returns exit code: 0 = passed, 1 = test failed assertions, 2 = usage/config error.
func runCmd(ctx context.Context, args []string, stdout io.Writer, db *sql.DB) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: primus run <config.json> [--db <path>]")
		return 2
	}
	policy, err := engine.LoadTargetPolicy(os.Getenv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "primus run: %v\n", err)
		return 2
	}
	return runWith(ctx, runOptions{ConfigPath: args[0], Policy: policy}, stdout, db)
}

// runOutput is what "primus run" prints for one run. It holds no URL, header, body or credential.
type runOutput struct {
	ID           string           `json:"id"`
	TestConfigID string           `json:"test_config_id"`
	StartedAt    string           `json:"started_at"`
	FinishedAt   string           `json:"finished_at"`
	Summary      model.RunSummary `json:"summary"`
	Passed       bool             `json:"passed"`
	Outcome      model.RunOutcome `json:"outcome"`
	// Login carries counts, latencies and failure classes only.
	Login *model.LoginSummary `json:"login,omitempty"`
}

func outputOf(run model.Run) runOutput {
	return runOutput{
		ID:           run.ID,
		TestConfigID: run.TestConfigID,
		StartedAt:    run.StartedAt.Format("2006-01-02T15:04:05Z"),
		FinishedAt:   run.FinishedAt.Format("2006-01-02T15:04:05Z"),
		Summary:      run.Summary,
		Passed:       run.Passed,
		Outcome:      run.Outcome,
		Login:        run.Login,
	}
}

func runWith(ctx context.Context, o runOptions, stdout io.Writer, db *sql.DB) int {
	if (o.ConfigPath == "") == (o.Collection == "") {
		fmt.Fprintln(os.Stderr, "primus run: give either <config.json> or --collection <id or name>, not both or neither")
		return 2
	}
	if o.ReportDir != "" {
		if err := os.MkdirAll(o.ReportDir, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "primus run: report dir: %v\n", err)
			return 2
		}
	}
	if o.Collection != "" {
		return runCollection(ctx, o, stdout, db)
	}

	data, err := os.ReadFile(o.ConfigPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "primus run: read config %q: %v\n", o.ConfigPath, err)
		return 2
	}
	var cfg model.TestConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		fmt.Fprintf(os.Stderr, "primus run: parse config %q: %v\n", o.ConfigPath, err)
		return 2
	}
	if err := engine.ValidateTestConfig(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "primus run: invalid config: %v\n", err)
		return 2
	}
	reqDef, err := store.GetRequestDefinition(db, cfg.RequestID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "primus run: request %q not found in DB: %v\n", cfg.RequestID, err)
		return 2
	}
	// The config is saved so the run shows its endpoint in the web UI's history and reports.
	run, err := executeRun(ctx, db, o, reqDef, cfg, true)
	if err != nil {
		fmt.Fprintf(os.Stderr, "primus run: %v\n", err)
		return 2
	}
	out := outputOf(run)
	if code := writeReports(db, o.ReportDir, run, out); code != 0 {
		return code
	}
	if err := encodeJSON(stdout, out); err != nil {
		fmt.Fprintf(os.Stderr, "primus run: encode output: %v\n", err)
		return 2
	}
	if run.Passed {
		return 0
	}
	return 1
}

// executeRun resolves the endpoint URL, applies the protected host policy, picks the identity,
// runs the engine and saves the run. Any error is a usage/config error (exit 2).
func executeRun(ctx context.Context, db *sql.DB, o runOptions, reqDef model.RequestDefinition, cfg model.TestConfig, saveConfig bool) (model.Run, error) {
	if cfg.AllowProtectedHost == "" {
		cfg.AllowProtectedHost = o.AllowProtectedHost
	}
	// {{base_url}} resolves from the endpoint's collection, as in the web handler.
	resolved, err := store.ResolveURL(db, reqDef)
	if err != nil {
		return model.Run{}, fmt.Errorf("request %q: %w", cfg.RequestID, err)
	}
	if err := o.Policy.Check(cfg, resolved.URLTemplate); err != nil {
		return model.Run{}, fmt.Errorf("refused: %w", err)
	}

	// Global auth, applied to every dispatched request.
	auth, err := store.GetGlobalAuth(db)
	if err != nil {
		return model.Run{}, fmt.Errorf("load global auth: %w", err)
	}
	// A credential pool, when the config names one, overrides global auth.
	var pool *model.CredentialPool
	if cfg.CredentialPoolID != "" && cfg.AuthSource.UsesPool() {
		p, err := store.GetCredentialPoolWithSecrets(db, cfg.CredentialPoolID)
		if err != nil {
			return model.Run{}, fmt.Errorf("credential pool %q not found in DB", cfg.CredentialPoolID)
		}
		pool = &p
	}
	// auth_source picks global auth, the pool, or no auth at all.
	auth, pool, err = engine.ResolveAuth(cfg, auth, pool)
	if err != nil {
		return model.Run{}, err
	}
	if pool == nil {
		cfg.CredentialPoolID = "" // the saved run names only a pool it used
	}
	if saveConfig {
		if err := store.SaveTestConfig(db, cfg); err != nil {
			return model.Run{}, fmt.Errorf("save config: %w", err)
		}
	}

	// CLI stays synchronous: it calls the engine directly and waits. A cancelled ctx (Ctrl-C,
	// SIGTERM) stops the run like the Stop button: in-flight requests are counted as cancelled.
	run, err := engine.RunWithPool(ctx, resolved, cfg, auth, pool, nil)
	if err != nil {
		return model.Run{}, fmt.Errorf("engine error: %w", err)
	}
	// Persist the run so it appears in the web UI's history.
	if err := store.SaveRun(db, run); err != nil {
		return model.Run{}, fmt.Errorf("save run: %w", err)
	}
	return run, nil
}

// Item states in a collection run's output.
const (
	itemPassed = "passed"
	itemFailed = "failed"
	// itemNoAssertions completed but checked nothing. Like a single run, it does not count as a pass.
	itemNoAssertions = "no_assertions"
	itemSkipped      = "skipped"
	itemError        = "error"
)

type collectionItem struct {
	RequestID string     `json:"request_id"`
	Name      string     `json:"name"`
	Status    string     `json:"status"`
	Reason    string     `json:"reason,omitempty"`
	Run       *runOutput `json:"run,omitempty"`
}

type collectionOutput struct {
	Collection struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"collection"`
	Passed  bool             `json:"passed"`
	Results []collectionItem `json:"results"`
}

// runCollection runs every endpoint of a collection one after another with its newest saved config,
// like Run all in the web UI. An endpoint with no saved config is skipped. The exit code is the worst
// of the runs: 2 if any could not start (or none ran), else 1 if any failed, else 0.
func runCollection(ctx context.Context, o runOptions, stdout io.Writer, db *sql.DB) int {
	col, err := store.FindCollection(db, o.Collection)
	if err != nil {
		fmt.Fprintf(os.Stderr, "primus run: %v\n", err)
		return 2
	}
	defs, err := store.ListRequestDefinitionsByCollection(db, col.ID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "primus run: list endpoints: %v\n", err)
		return 2
	}
	var out collectionOutput
	out.Collection.ID, out.Collection.Name = col.ID, col.Name
	ran, code := 0, 0
	for _, def := range defs {
		item := collectionItem{RequestID: def.ID, Name: def.Name}
		switch {
		case ctx.Err() != nil:
			item.Status, item.Reason = itemSkipped, "stopped"
		default:
			cfg, err := store.LatestTestConfig(db, def.ID)
			if errors.Is(err, store.ErrNoSuchConfig) {
				item.Status, item.Reason = itemSkipped, "no saved test config"
				break
			}
			if err == nil {
				err = engine.ValidateTestConfig(cfg)
			}
			var run model.Run
			if err == nil {
				run, err = executeRun(ctx, db, o, def, cfg, false)
			}
			if err != nil {
				item.Status, item.Reason, code = itemError, err.Error(), 2
				fmt.Fprintf(os.Stderr, "primus run: %s: %v\n", def.Name, err)
				break
			}
			ran++
			ro := outputOf(run)
			item.Run = &ro
			if c := writeReports(db, o.ReportDir, run, ro); c != 0 {
				code = 2
			}
			switch {
			case run.Passed:
				item.Status = itemPassed
			case run.Outcome == model.OutcomeNoAssertions:
				item.Status = itemNoAssertions
				code = max(code, 1)
			default:
				item.Status = itemFailed
				code = max(code, 1)
			}
		}
		out.Results = append(out.Results, item)
	}
	if ran == 0 {
		fmt.Fprintf(os.Stderr, "primus run: no endpoint in collection %q ran (save a test config for each one first)\n", col.Name)
		code = 2
	}
	out.Passed = code == 0
	if o.ReportDir != "" {
		name := filepath.Join(o.ReportDir, fmt.Sprintf("collection-%d.json", time.Now().UnixNano()))
		if err := writeJSONFile(name, out); err != nil {
			fmt.Fprintf(os.Stderr, "primus run: %v\n", err)
			code = 2
		}
	}
	if err := encodeJSON(stdout, out); err != nil {
		fmt.Fprintf(os.Stderr, "primus run: encode output: %v\n", err)
		return 2
	}
	return code
}

// writeReports writes <run-id>.json, .md and .csv under dir. An empty dir writes nothing.
func writeReports(db *sql.DB, dir string, run model.Run, out runOutput) int {
	if dir == "" {
		return 0
	}
	base := filepath.Join(dir, run.ID)
	if err := writeJSONFile(base+".json", out); err != nil {
		fmt.Fprintf(os.Stderr, "primus run: %v\n", err)
		return 2
	}
	if err := os.WriteFile(base+".md", []byte(web.RunReportMarkdown(db, run)), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "primus run: write report: %v\n", err)
		return 2
	}
	f, err := os.Create(base + ".csv")
	if err != nil {
		fmt.Fprintf(os.Stderr, "primus run: write series: %v\n", err)
		return 2
	}
	werr := web.WriteSeriesCSV(f, run)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		fmt.Fprintf(os.Stderr, "primus run: write series: %v\n", werr)
		return 2
	}
	return 0
}

func encodeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func writeJSONFile(path string, v any) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	werr := encodeJSON(f, v)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return fmt.Errorf("write %s: %w", path, werr)
	}
	return nil
}
