package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"

	"github.com/Horac3/Primus/internal/store"
	"github.com/Horac3/Primus/internal/web"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))

	if len(os.Args) > 1 && os.Args[1] == "run" {
		// "primus run <config.json> [--db <path>]"
		fs := flag.NewFlagSet("run", flag.ExitOnError)
		dbPath := fs.String("db", "primus.db", "SQLite database file path")
		fs.Parse(os.Args[2:]) //nolint:errcheck // ExitOnError handles errors

		db, err := store.OpenDB(*dbPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "primus run: open db: %v\n", err)
			os.Exit(2)
		}
		defer db.Close()
		if err := store.Migrate(db); err != nil {
			fmt.Fprintf(os.Stderr, "primus run: migrate: %v\n", err)
			os.Exit(2)
		}

		os.Exit(runCmd(context.Background(), fs.Args(), os.Stdout, db))
	}

	// Default: start the web server.
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", ":8080", "HTTP listen address")
	dbPath := fs.String("db", "primus.db", "SQLite database file path")
	fs.Parse(os.Args[1:]) //nolint:errcheck

	db, err := store.OpenDB(*dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "primus: open db: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := store.Migrate(db); err != nil {
		fmt.Fprintf(os.Stderr, "primus: migrate: %v\n", err)
		os.Exit(1)
	}

	mux := web.NewServer(db)

	log.Printf("Primus listening on %s  (db: %s)", *addr, *dbPath)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		fmt.Fprintf(os.Stderr, "primus: server: %v\n", err)
		os.Exit(1)
	}
}
