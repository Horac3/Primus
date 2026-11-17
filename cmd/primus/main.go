package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/Horac3/Primus/internal/seed"
	"github.com/Horac3/Primus/internal/store"
	"github.com/Horac3/Primus/internal/web"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))

	// Headless subcommands: "primus run", "primus import", "primus list". Ctrl-C or SIGTERM stops a
	// run the way the Stop button does, so the partial run is still saved and reported.
	if len(os.Args) > 1 && isSubcommand(os.Args[1]) {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		code := subcommand(ctx, os.Args[1], os.Args[2:], os.Stdout)
		stop()
		os.Exit(code)
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

	// A seed problem (for example a user collection already named "JSONPlaceholder") must not stop the server.
	if _, err := seed.Ensure(db); err != nil {
		slog.Warn("built-in examples not seeded", "err", err)
	}

	mux := web.NewServer(db)

	log.Printf("Primus listening on %s  (db: %s)", *addr, *dbPath)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		fmt.Fprintf(os.Stderr, "primus: server: %v\n", err)
		os.Exit(1)
	}
}
