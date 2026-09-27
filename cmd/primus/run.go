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

	// Global auth, applied to every dispatched request.
	auth, err := store.GetGlobalAuth(db)
	if err != nil {
		fmt.Fprintf(os.Stderr, "primus run: load global auth: %v\n", err)
		return 2
	}

	// Execute the run using the same engine as the web handler.
	// CLI stays synchronous: it calls engine.Run directly and waits.
	run, err := engine.Run(ctx, reqDef, cfg, auth)
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
	}{
		ID:           run.ID,
		TestConfigID: run.TestConfigID,
		StartedAt:    run.StartedAt.Format("2006-01-02T15:04:05Z"),
		FinishedAt:   run.FinishedAt.Format("2006-01-02T15:04:05Z"),
		Summary:      run.Summary,
		Passed:       run.Passed,
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
