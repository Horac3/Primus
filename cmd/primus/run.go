package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/Horac3/Primus/internal/engine"
	"github.com/Horac3/Primus/internal/model"
	"github.com/Horac3/Primus/internal/store"
)

// runCmd is the testable core of the "primus run" subcommand.
// args[0] must be the path to a TestConfig JSON file.
// Returns exit code: 0 = passed, 1 = test failed assertions, 2 = usage/config error.
func runCmd(ctx context.Context, args []string, stdout io.Writer, db *sql.DB) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: primus run <config.json> [--db <path>]")
		return 2
	}
	cfgPath := args[0]

	// Load TestConfig from JSON file.
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "primus run: read config %q: %v\n", cfgPath, err)
		return 2
	}
	var cfg model.TestConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		fmt.Fprintf(os.Stderr, "primus run: parse config %q: %v\n", cfgPath, err)
		return 2
	}
	if err := engine.ValidateTestConfig(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "primus run: invalid config: %v\n", err)
		return 2
	}

	// Load the RequestDefinition from the store.
	reqDef, err := store.GetRequestDefinition(db, cfg.RequestID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "primus run: request %q not found in DB: %v\n", cfg.RequestID, err)
		return 2
	}
	// {{base_url}} resolves from the endpoint's collection, as in the web handler.
	reqDef, err = store.ResolveURL(db, reqDef)
	if err != nil {
		fmt.Fprintf(os.Stderr, "primus run: request %q: %v\n", cfg.RequestID, err)
		return 2
	}

	// Global auth, applied to every dispatched request.
	auth, err := store.GetGlobalAuth(db)
	if err != nil {
		fmt.Fprintf(os.Stderr, "primus run: load global auth: %v\n", err)
		return 2
	}

	// A credential pool, when the config names one, overrides global auth.
	var pool *model.CredentialPool
	if cfg.CredentialPoolID != "" && cfg.AuthSource.UsesPool() {
		p, err := store.GetCredentialPoolWithSecrets(db, cfg.CredentialPoolID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "primus run: credential pool %q not found in DB\n", cfg.CredentialPoolID)
			return 2
		}
		pool = &p
	}
	// auth_source picks global auth, the pool, or no auth at all.
	auth, pool, err = engine.ResolveAuth(cfg, auth, pool)
	if err != nil {
		fmt.Fprintf(os.Stderr, "primus run: %v\n", err)
		return 2
	}
	if pool == nil {
		cfg.CredentialPoolID = "" // the saved run names only a pool it used
	}

	// Execute the run using the same engine as the web handler.
	// CLI stays synchronous: it calls the engine directly and waits.
	run, err := engine.RunWithPool(ctx, reqDef, cfg, auth, pool, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "primus run: engine error: %v\n", err)
		return 2
	}

	// Persist the run so it appears in the web UI's history.
	if err := store.SaveRun(db, run); err != nil {
		fmt.Fprintf(os.Stderr, "primus run: save run: %v\n", err)
		return 2
	}

	// Print the summary as JSON to stdout for CI consumption.
	out := struct {
		ID           string           `json:"id"`
		TestConfigID string           `json:"test_config_id"`
		StartedAt    string           `json:"started_at"`
		FinishedAt   string           `json:"finished_at"`
		Summary      model.RunSummary `json:"summary"`
		Passed       bool             `json:"passed"`
		Outcome      model.RunOutcome `json:"outcome"`
		// Login carries counts, latencies and failure classes only.
		Login *model.LoginSummary `json:"login,omitempty"`
	}{
		ID:           run.ID,
		TestConfigID: run.TestConfigID,
		StartedAt:    run.StartedAt.Format("2006-01-02T15:04:05Z"),
		FinishedAt:   run.FinishedAt.Format("2006-01-02T15:04:05Z"),
		Summary:      run.Summary,
		Passed:       run.Passed,
		Outcome:      run.Outcome,
		Login:        run.Login,
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		fmt.Fprintf(os.Stderr, "primus run: encode output: %v\n", err)
		return 2
	}

	if run.Passed {
		return 0
	}
	return 1
}
