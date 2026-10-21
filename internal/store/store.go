package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Horac3/Primus/internal/model"
	_ "modernc.org/sqlite"
)

// dsnPragmas apply to every pooled connection. busy_timeout makes a write that meets a lock wait up to 5s
// instead of failing with SQLITE_BUSY; WAL lets readers and the single writer proceed together (D-013).
const dsnPragmas = "_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"

// OpenDB opens (or creates) a SQLite database file at path.
func OpenDB(path string) (*sql.DB, error) {
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	db, err := sql.Open("sqlite", path+sep+dsnPragmas)
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
		`CREATE TABLE IF NOT EXISTS insights (
			run_id     TEXT PRIMARY KEY,
			analyzer   TEXT NOT NULL,
			model      TEXT NOT NULL DEFAULT '',
			status     TEXT NOT NULL CHECK (status IN ('pending', 'done', 'failed')),
			text       TEXT NOT NULL DEFAULT '',
			error      TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS credential_pools (
			id      TEXT PRIMARY KEY,
			name    TEXT NOT NULL,
			payload TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS credentials (
			pool_id TEXT NOT NULL,
			idx     INTEGER NOT NULL,
			fields  TEXT NOT NULL,
			PRIMARY KEY (pool_id, idx)
		)`,
		`CREATE TABLE IF NOT EXISTS active_credential_pool (
			id      INTEGER PRIMARY KEY CHECK (id = 1),
			pool_id TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS collections (
			id           TEXT PRIMARY KEY,
			name         TEXT NOT NULL,
			base_url     TEXT NOT NULL DEFAULT '',
			seed_id      TEXT NOT NULL DEFAULT '',
			seed_version INTEGER NOT NULL DEFAULT 0,
			created_at   TEXT NOT NULL
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_collections_name ON collections (name COLLATE NOCASE)`,
		`CREATE TABLE IF NOT EXISTS seed_tombstones (
			seed_id    TEXT PRIMARY KEY,
			deleted_at TEXT NOT NULL
		)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	if err := migrateCollections(db); err != nil {
		return err
	}
	return migrateMaskedFields(db)
}

// SaveRequestDefinition inserts or replaces a RequestDefinition. An empty CollectionID files the
// endpoint under "My endpoints", creating that collection if needed.
func SaveRequestDefinition(db *sql.DB, r model.RequestDefinition) error {
	if r.CollectionID == "" {
		id, err := ensureDefaultCollection(db)
		if err != nil {
			return err
		}
		r.CollectionID = id
	}
	b, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("marshal RequestDefinition: %w", err)
	}
	_, err = db.Exec(
		`INSERT OR REPLACE INTO requests (id, name, method, url_template, payload, collection_id) VALUES (?,?,?,?,?,?)`,
		r.ID, r.Name, r.Method, r.URLTemplate, string(b), r.CollectionID,
	)
	return err
}

// GetRequestDefinition retrieves a RequestDefinition by ID.
func GetRequestDefinition(db *sql.DB, id string) (model.RequestDefinition, error) {
	row := db.QueryRow(`SELECT payload, collection_id FROM requests WHERE id = ?`, id)
	var payload, collectionID string
	if err := row.Scan(&payload, &collectionID); err != nil {
		return model.RequestDefinition{}, fmt.Errorf("GetRequestDefinition %q: %w", id, err)
	}
	var r model.RequestDefinition
	if err := json.Unmarshal([]byte(payload), &r); err != nil {
		return model.RequestDefinition{}, fmt.Errorf("unmarshal RequestDefinition: %w", err)
	}
	r.CollectionID = collectionID
	return r, nil
}

// ListRequestDefinitions returns all stored RequestDefinitions.
func ListRequestDefinitions(db *sql.DB) ([]model.RequestDefinition, error) {
	rows, err := db.Query(`SELECT payload, collection_id FROM requests`)
	if err != nil {
		return nil, fmt.Errorf("ListRequestDefinitions: %w", err)
	}
	defer rows.Close()
	var out []model.RequestDefinition
	for rows.Next() {
		var payload, collectionID string
		if err := rows.Scan(&payload, &collectionID); err != nil {
			return nil, err
		}
		var r model.RequestDefinition
		if err := json.Unmarshal([]byte(payload), &r); err != nil {
			return nil, err
		}
		r.CollectionID = collectionID
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

// ListTestConfigs returns all stored TestConfigs.
func ListTestConfigs(db *sql.DB) ([]model.TestConfig, error) {
	rows, err := db.Query(`SELECT payload FROM test_configs`)
	if err != nil {
		return nil, fmt.Errorf("ListTestConfigs: %w", err)
	}
	defer rows.Close()
	var out []model.TestConfig
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var c model.TestConfig
		if err := json.Unmarshal([]byte(payload), &c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
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

const (
	InsightPending = "pending"
	InsightDone    = "done"
	InsightFailed  = "failed"
)

type InsightRecord struct {
	RunID     string
	Analyzer  string
	Model     string
	Status    string
	Text      string
	Error     string
	CreatedAt time.Time
}

// StartInsight inserts a pending row or restarts a completed/failed row.
// It reports started=false when a pending row already exists.
func StartInsight(db *sql.DB, runID, analyzer, modelName string) (InsightRecord, bool, error) {
	createdAt := time.Now().UTC()
	result, err := db.Exec(
		`INSERT INTO insights (run_id, analyzer, model, status, text, error, created_at)
		 VALUES (?, ?, ?, 'pending', '', '', ?)
		 ON CONFLICT(run_id) DO UPDATE SET
			 analyzer = excluded.analyzer,
			 model = excluded.model,
			 status = 'pending',
			 text = '',
			 error = '',
			 created_at = excluded.created_at
		 WHERE insights.status != 'pending'`,
		runID, analyzer, modelName, createdAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return InsightRecord{}, false, fmt.Errorf("StartInsight %q: %w", runID, err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return InsightRecord{}, false, fmt.Errorf("StartInsight rows affected: %w", err)
	}
	record, err := GetInsight(db, runID)
	return record, rowsAffected > 0, err
}

func GetInsight(db *sql.DB, runID string) (InsightRecord, error) {
	var record InsightRecord
	var createdAt string
	err := db.QueryRow(
		`SELECT run_id, analyzer, model, status, text, error, created_at FROM insights WHERE run_id = ?`, runID,
	).Scan(&record.RunID, &record.Analyzer, &record.Model, &record.Status, &record.Text, &record.Error, &createdAt)
	if err != nil {
		return InsightRecord{}, fmt.Errorf("GetInsight %q: %w", runID, err)
	}
	record.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return InsightRecord{}, fmt.Errorf("parse insight created_at: %w", err)
	}
	return record, nil
}

func CompleteInsight(db *sql.DB, runID, analyzer, modelName, text string) error {
	result, err := db.Exec(
		`UPDATE insights SET analyzer = ?, model = ?, status = 'done', text = ?, error = '' WHERE run_id = ? AND status = 'pending'`,
		analyzer, modelName, text, runID,
	)
	return requireInsightUpdate(result, err, runID, "complete")
}

func FailInsight(db *sql.DB, runID, analyzer, modelName, cause string) error {
	result, err := db.Exec(
		`UPDATE insights SET analyzer = ?, model = ?, status = 'failed', text = '', error = ? WHERE run_id = ? AND status = 'pending'`,
		analyzer, modelName, cause, runID,
	)
	return requireInsightUpdate(result, err, runID, "fail")
}

func requireInsightUpdate(result sql.Result, err error, runID, operation string) error {
	if err != nil {
		return fmt.Errorf("%s insight %q: %w", operation, runID, err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s insight rows affected: %w", operation, err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("%s insight %q: no pending row", operation, runID)
	}
	return nil
}

func FailPendingInsights(db *sql.DB, cause string) (int64, error) {
	result, err := db.Exec(
		`UPDATE insights SET status = 'failed', error = ? WHERE status = 'pending'`, cause,
	)
	if err != nil {
		return 0, fmt.Errorf("FailPendingInsights: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("FailPendingInsights rows affected: %w", err)
	}
	return count, nil
}
