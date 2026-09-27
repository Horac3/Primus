package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Horac3/Primus/internal/model"
	_ "modernc.org/sqlite"
)

// OpenDB opens (or creates) a SQLite database file at path.
func OpenDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %q: %w", path, err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping sqlite %q: %w", path, err)
	}
	return db, nil
}

// Migrate creates all required tables if they do not already exist.
// Safe to call multiple times on the same database (idempotent).
func Migrate(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS requests (
			id          TEXT PRIMARY KEY,
			name        TEXT NOT NULL,
			method      TEXT NOT NULL,
			url_template TEXT NOT NULL,
			payload     TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS test_configs (
			id         TEXT PRIMARY KEY,
			request_id TEXT NOT NULL,
			payload    TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS runs (
			id             TEXT PRIMARY KEY,
			test_config_id TEXT NOT NULL,
			started_at     TEXT NOT NULL,
			finished_at    TEXT NOT NULL,
			payload        TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS global_auth (
			id          INTEGER PRIMARY KEY CHECK (id = 1),
			auth_type   TEXT NOT NULL,
			token       TEXT NOT NULL DEFAULT '',
			header_name TEXT NOT NULL DEFAULT '',
			header_value TEXT NOT NULL DEFAULT '',
			username    TEXT NOT NULL DEFAULT '',
			password    TEXT NOT NULL DEFAULT ''
		)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	return nil
}

// SaveRequestDefinition inserts or replaces a RequestDefinition.
func SaveRequestDefinition(db *sql.DB, r model.RequestDefinition) error {
	b, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("marshal RequestDefinition: %w", err)
	}
	_, err = db.Exec(
		`INSERT OR REPLACE INTO requests (id, name, method, url_template, payload) VALUES (?,?,?,?,?)`,
		r.ID, r.Name, r.Method, r.URLTemplate, string(b),
	)
	return err
}

// GetRequestDefinition retrieves a RequestDefinition by ID.
func GetRequestDefinition(db *sql.DB, id string) (model.RequestDefinition, error) {
	row := db.QueryRow(`SELECT payload FROM requests WHERE id = ?`, id)
	var payload string
	if err := row.Scan(&payload); err != nil {
		return model.RequestDefinition{}, fmt.Errorf("GetRequestDefinition %q: %w", id, err)
	}
	var r model.RequestDefinition
	if err := json.Unmarshal([]byte(payload), &r); err != nil {
		return model.RequestDefinition{}, fmt.Errorf("unmarshal RequestDefinition: %w", err)
	}
	return r, nil
}

// ListRequestDefinitions returns all stored RequestDefinitions.
func ListRequestDefinitions(db *sql.DB) ([]model.RequestDefinition, error) {
	rows, err := db.Query(`SELECT payload FROM requests`)
	if err != nil {
		return nil, fmt.Errorf("ListRequestDefinitions: %w", err)
	}
	defer rows.Close()
	var out []model.RequestDefinition
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var r model.RequestDefinition
		if err := json.Unmarshal([]byte(payload), &r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SaveGlobalAuth inserts or replaces the single global auth row (id=1).
func SaveGlobalAuth(db *sql.DB, a model.AuthConfig) error {
	_, err := db.Exec(
		`INSERT OR REPLACE INTO global_auth (id, auth_type, token, header_name, header_value, username, password)
		 VALUES (1, ?, ?, ?, ?, ?, ?)`,
		string(a.Type), a.Token, a.Header, a.Value, a.Username, a.Password,
	)
	if err != nil {
		return fmt.Errorf("SaveGlobalAuth: %w", err)
	}
	return nil
}

// GetGlobalAuth retrieves the global auth config.
// Returns the zero AuthConfig (no error) when none has been saved yet.
func GetGlobalAuth(db *sql.DB) (model.AuthConfig, error) {
	row := db.QueryRow(`SELECT auth_type, token, header_name, header_value, username, password FROM global_auth WHERE id = 1`)
	var a model.AuthConfig
	var authType string
	if err := row.Scan(&authType, &a.Token, &a.Header, &a.Value, &a.Username, &a.Password); err != nil {
		if err == sql.ErrNoRows {
			return model.AuthConfig{}, nil
		}
		return model.AuthConfig{}, fmt.Errorf("GetGlobalAuth: %w", err)
	}
	a.Type = model.AuthType(authType)
	return a, nil
}

// SaveTestConfig inserts or replaces a TestConfig.
func SaveTestConfig(db *sql.DB, c model.TestConfig) error {
	b, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshal TestConfig: %w", err)
	}
	_, err = db.Exec(
		`INSERT OR REPLACE INTO test_configs (id, request_id, payload) VALUES (?,?,?)`,
		c.ID, c.RequestID, string(b),
	)
	return err
}

// GetTestConfig retrieves a TestConfig by ID.
func GetTestConfig(db *sql.DB, id string) (model.TestConfig, error) {
	row := db.QueryRow(`SELECT payload FROM test_configs WHERE id = ?`, id)
	var payload string
	if err := row.Scan(&payload); err != nil {
		return model.TestConfig{}, fmt.Errorf("GetTestConfig %q: %w", id, err)
	}
	var c model.TestConfig
	if err := json.Unmarshal([]byte(payload), &c); err != nil {
		return model.TestConfig{}, fmt.Errorf("unmarshal TestConfig: %w", err)
	}
	return c, nil
}

// SaveRun inserts or replaces a Run.
func SaveRun(db *sql.DB, r model.Run) error {
	b, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("marshal Run: %w", err)
	}
	_, err = db.Exec(
		`INSERT OR REPLACE INTO runs (id, test_config_id, started_at, finished_at, payload) VALUES (?,?,?,?,?)`,
		r.ID, r.TestConfigID,
		r.StartedAt.UTC().Format(time.RFC3339),
		r.FinishedAt.UTC().Format(time.RFC3339),
		string(b),
	)
	return err
}

// GetRun retrieves a Run by ID.
func GetRun(db *sql.DB, id string) (model.Run, error) {
	row := db.QueryRow(`SELECT payload FROM runs WHERE id = ?`, id)
	var payload string
	if err := row.Scan(&payload); err != nil {
		return model.Run{}, fmt.Errorf("GetRun %q: %w", id, err)
	}
	var r model.Run
	if err := json.Unmarshal([]byte(payload), &r); err != nil {
		return model.Run{}, fmt.Errorf("unmarshal Run: %w", err)
	}
	return r, nil
}

// ListAllRuns returns every Run regardless of TestConfig ID.
func ListAllRuns(db *sql.DB) ([]model.Run, error) {
	rows, err := db.Query(`SELECT payload FROM runs`)
	if err != nil {
		return nil, fmt.Errorf("ListAllRuns: %w", err)
	}
	defer rows.Close()
	var out []model.Run
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var r model.Run
		if err := json.Unmarshal([]byte(payload), &r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListRuns returns all Runs for a given TestConfig ID.
func ListRuns(db *sql.DB, testConfigID string) ([]model.Run, error) {
	rows, err := db.Query(`SELECT payload FROM runs WHERE test_config_id = ?`, testConfigID)
	if err != nil {
		return nil, fmt.Errorf("ListRuns: %w", err)
	}
	defer rows.Close()
	var out []model.Run
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var r model.Run
		if err := json.Unmarshal([]byte(payload), &r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
