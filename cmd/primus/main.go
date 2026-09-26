package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/Horac3/Primus/internal/store"
	"github.com/Horac3/Primus/internal/web"
)

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	dbPath := flag.String("db", "primus.db", "SQLite database file path")
	flag.Parse()

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
