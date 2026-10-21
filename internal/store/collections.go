package store

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/Horac3/Primus/internal/model"
)

// DefaultCollectionID is the collection that holds endpoints created without an explicit one and
// everything that existed before collections (FR-COLL-02).
const (
	DefaultCollectionID   = "col-my-endpoints"
	DefaultCollectionName = "My endpoints"
)

// migrateCollections adds requests.collection_id when missing and files every unassigned endpoint
// under "My endpoints". Idempotent: a second run finds nothing to do.
func migrateCollections(db *sql.DB) error {
	has, err := columnExists(db, "requests", "collection_id")
	if err != nil {
		return err
	}
	if !has {
		if _, err := db.Exec(`ALTER TABLE requests ADD COLUMN collection_id TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("migrate collections: add column: %w", err)
		}
	}
	var orphans int
	if err := db.QueryRow(`SELECT COUNT(*) FROM requests WHERE collection_id = ''`).Scan(&orphans); err != nil {
		return fmt.Errorf("migrate collections: count: %w", err)
	}
	if orphans == 0 {
		return nil
	}
	id, err := ensureDefaultCollection(db)
	if err != nil {
		return err
	}
	if _, err := db.Exec(`UPDATE requests SET collection_id = ? WHERE collection_id = ''`, id); err != nil {
		return fmt.Errorf("migrate collections: assign: %w", err)
	}
	return nil
}

func columnExists(db *sql.DB, table, column string) (bool, error) {
	rows, err := db.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return false, fmt.Errorf("table_info %s: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

// ensureDefaultCollection creates "My endpoints" if it does not exist and returns its ID.
func ensureDefaultCollection(db *sql.DB) (string, error) {
	_, err := db.Exec(
		`INSERT OR IGNORE INTO collections (id, name, created_at) VALUES (?,?,?)`,
		DefaultCollectionID, DefaultCollectionName, time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		return "", fmt.Errorf("ensure default collection: %w", err)
	}
	return DefaultCollectionID, nil
}

// ListCollections returns all collections ordered by name.
func ListCollections(db *sql.DB) ([]model.Collection, error) {
	rows, err := db.Query(`SELECT id, name, base_url, seed_id, seed_version FROM collections ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, fmt.Errorf("ListCollections: %w", err)
	}
	defer rows.Close()
	var out []model.Collection
	for rows.Next() {
		var c model.Collection
		if err := rows.Scan(&c.ID, &c.Name, &c.BaseURL, &c.SeedID, &c.SeedVersion); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Sentinels callers act on to show a message next to the form field.
var (
	ErrEmptyName        = errors.New("collection name is required")
	ErrDuplicateName    = errors.New("a collection with that name already exists")
	ErrInvalidBaseURL   = errors.New("base URL must be an absolute http or https URL")
	ErrNoSuchCollection = errors.New("no such collection")
)

func cleanCollectionInput(name, baseURL string) (string, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", "", ErrEmptyName
	}
	baseURL = strings.TrimSpace(baseURL)
	if baseURL != "" {
		u, err := url.Parse(baseURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return "", "", ErrInvalidBaseURL
		}
	}
	return name, baseURL, nil
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// CreateCollection adds a user collection. The name is trimmed and unique ignoring case; baseURL is
// empty or an absolute http(s) URL.
func CreateCollection(db *sql.DB, name, baseURL string) (model.Collection, error) {
	name, baseURL, err := cleanCollectionInput(name, baseURL)
	if err != nil {
		return model.Collection{}, err
	}
	c := model.Collection{ID: fmt.Sprintf("col-%d", time.Now().UnixNano()), Name: name, BaseURL: baseURL}
	_, err = db.Exec(`INSERT INTO collections (id, name, base_url, created_at) VALUES (?,?,?,?)`,
		c.ID, c.Name, c.BaseURL, time.Now().UTC().Format(time.RFC3339))
	if isUniqueViolation(err) {
		return model.Collection{}, ErrDuplicateName
	}
	if err != nil {
		return model.Collection{}, fmt.Errorf("CreateCollection: %w", err)
	}
	return c, nil
}

// GetCollection retrieves a collection by ID; the error wraps ErrNoSuchCollection when absent.
func GetCollection(db *sql.DB, id string) (model.Collection, error) {
	var c model.Collection
	err := db.QueryRow(`SELECT id, name, base_url, seed_id, seed_version FROM collections WHERE id = ?`, id).
		Scan(&c.ID, &c.Name, &c.BaseURL, &c.SeedID, &c.SeedVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Collection{}, fmt.Errorf("GetCollection %q: %w", id, ErrNoSuchCollection)
	}
	if err != nil {
		return model.Collection{}, fmt.Errorf("GetCollection %q: %w", id, err)
	}
	return c, nil
}

// UpdateCollection renames a collection and sets its base URL.
func UpdateCollection(db *sql.DB, id, name, baseURL string) error {
	name, baseURL, err := cleanCollectionInput(name, baseURL)
	if err != nil {
		return err
	}
	res, err := db.Exec(`UPDATE collections SET name = ?, base_url = ? WHERE id = ?`, name, baseURL, id)
	if isUniqueViolation(err) {
		return ErrDuplicateName
	}
	if err != nil {
		return fmt.Errorf("UpdateCollection %q: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("UpdateCollection %q: %w", id, ErrNoSuchCollection)
	}
	return nil
}

// ErrNoBaseURL means an endpoint uses {{base_url}} but its collection has none set.
var ErrNoBaseURL = errors.New("collection has no base URL")

// ResolveURL returns a copy of def with a leading {{base_url}} replaced by its collection's base
// URL, ready for Probe or Run. The stored definition keeps the template. A template without the
// placeholder is returned as-is without touching the database.
func ResolveURL(db *sql.DB, def model.RequestDefinition) (model.RequestDefinition, error) {
	if !model.UsesBaseURL(def.URLTemplate) {
		return def, nil
	}
	if def.CollectionID == "" {
		return model.RequestDefinition{}, fmt.Errorf("%w: pick a collection that has one", ErrNoBaseURL)
	}
	c, err := GetCollection(db, def.CollectionID)
	if err != nil {
		return model.RequestDefinition{}, err
	}
	if c.BaseURL == "" {
		return model.RequestDefinition{}, fmt.Errorf("%w: set one on %q", ErrNoBaseURL, c.Name)
	}
	def.URLTemplate = model.ExpandBaseURL(def.URLTemplate, c.BaseURL)
	return def, nil
}

// ScopeKind says how wide a delete or clear reaches.
type ScopeKind int

const (
	ScopeAll ScopeKind = iota
	ScopeCollection
	ScopeEndpoint
)

// Scope names the endpoints an action covers. ID is the collection or endpoint ID; unused for ScopeAll.
type Scope struct {
	Kind ScopeKind
	ID   string
}

// Counts is what a scope holds, shown in confirmation prompts and returned by deletes.
type Counts struct{ Endpoints, Runs int }

type queryer interface {
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
	Exec(query string, args ...any) (sql.Result, error)
}

// requestSelect returns a SQL subquery selecting the endpoint IDs in scope, with its arguments.
func requestSelect(s Scope) (string, []any) {
	switch s.Kind {
	case ScopeCollection:
		return `SELECT id FROM requests WHERE collection_id = ?`, []any{s.ID}
	case ScopeEndpoint:
		return `SELECT id FROM requests WHERE id = ?`, []any{s.ID}
	default:
		return `SELECT id FROM requests`, nil
	}
}

// runSelect returns a SQL subquery selecting the run IDs reached through the scope's test configs.
func runSelect(s Scope) (string, []any) {
	reqSel, args := requestSelect(s)
	return `SELECT id FROM runs WHERE test_config_id IN (SELECT id FROM test_configs WHERE request_id IN (` + reqSel + `))`, args
}

func countScope(q queryer, s Scope) (Counts, error) {
	var c Counts
	reqSel, args := requestSelect(s)
	if err := q.QueryRow(`SELECT COUNT(*) FROM (`+reqSel+`)`, args...).Scan(&c.Endpoints); err != nil {
		return Counts{}, fmt.Errorf("count endpoints: %w", err)
	}
	// "all" counts every run, including any whose endpoint is already gone.
	if s.Kind == ScopeAll {
		if err := q.QueryRow(`SELECT COUNT(*) FROM runs`).Scan(&c.Runs); err != nil {
			return Counts{}, fmt.Errorf("count runs: %w", err)
		}
		return c, nil
	}
	runSel, args := runSelect(s)
	if err := q.QueryRow(`SELECT COUNT(*) FROM (`+runSel+`)`, args...).Scan(&c.Runs); err != nil {
		return Counts{}, fmt.Errorf("count runs: %w", err)
	}
	return c, nil
}

// CountScope reports how many endpoints and runs a scope holds.
func CountScope(db *sql.DB, s Scope) (Counts, error) { return countScope(db, s) }

// ScopeRequestIDs lists the endpoint IDs in scope, for the active-run check.
func ScopeRequestIDs(db *sql.DB, s Scope) ([]string, error) {
	sel, args := requestSelect(s)
	rows, err := db.Query(sel, args...)
	if err != nil {
		return nil, fmt.Errorf("ScopeRequestIDs: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// clearHistory deletes the runs and insights in scope. Endpoints and configs stay.
func clearHistory(q queryer, s Scope) (int, error) {
	counts, err := countScope(q, s)
	if err != nil {
		return 0, err
	}
	if s.Kind == ScopeAll {
		if _, err := q.Exec(`DELETE FROM insights`); err != nil {
			return 0, fmt.Errorf("clear insights: %w", err)
		}
		if _, err := q.Exec(`DELETE FROM runs`); err != nil {
			return 0, fmt.Errorf("clear runs: %w", err)
		}
		return counts.Runs, nil
	}
	runSel, args := runSelect(s)
	if _, err := q.Exec(`DELETE FROM insights WHERE run_id IN (`+runSel+`)`, args...); err != nil {
		return 0, fmt.Errorf("clear insights: %w", err)
	}
	if _, err := q.Exec(`DELETE FROM runs WHERE id IN (`+runSel+`)`, args...); err != nil {
		return 0, fmt.Errorf("clear runs: %w", err)
	}
	return counts.Runs, nil
}

// ClearHistory removes runs and insights in scope in one transaction and returns how many runs went.
// Endpoints and saved test configs remain.
func ClearHistory(db *sql.DB, s Scope) (int, error) {
	tx, err := db.Begin()
	if err != nil {
		return 0, fmt.Errorf("ClearHistory: %w", err)
	}
	defer tx.Rollback()
	n, err := clearHistory(tx, s)
	if err != nil {
		return 0, err
	}
	return n, tx.Commit()
}

// DeleteCollection removes a collection with its endpoints, their configs, runs and insights in one
// transaction, and returns what went. Deleting a built-in collection records a tombstone so the
// seeder does not bring it back. Credential pools are not per-endpoint and are untouched.
func DeleteCollection(db *sql.DB, id string) (Counts, error) {
	tx, err := db.Begin()
	if err != nil {
		return Counts{}, fmt.Errorf("DeleteCollection: %w", err)
	}
	defer tx.Rollback()

	var seedID string
	if err := tx.QueryRow(`SELECT seed_id FROM collections WHERE id = ?`, id).Scan(&seedID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Counts{}, fmt.Errorf("DeleteCollection %q: %w", id, ErrNoSuchCollection)
		}
		return Counts{}, fmt.Errorf("DeleteCollection %q: %w", id, err)
	}
	scope := Scope{Kind: ScopeCollection, ID: id}
	counts, err := countScope(tx, scope)
	if err != nil {
		return Counts{}, err
	}
	if _, err := clearHistory(tx, scope); err != nil {
		return Counts{}, err
	}
	reqSel, args := requestSelect(scope)
	if _, err := tx.Exec(`DELETE FROM test_configs WHERE request_id IN (`+reqSel+`)`, args...); err != nil {
		return Counts{}, fmt.Errorf("delete configs: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM requests WHERE collection_id = ?`, id); err != nil {
		return Counts{}, fmt.Errorf("delete endpoints: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM collections WHERE id = ?`, id); err != nil {
		return Counts{}, fmt.Errorf("delete collection: %w", err)
	}
	if seedID != "" {
		if _, err := tx.Exec(`INSERT OR REPLACE INTO seed_tombstones (seed_id, deleted_at) VALUES (?,?)`,
			seedID, time.Now().UTC().Format(time.RFC3339)); err != nil {
			return Counts{}, fmt.Errorf("write tombstone: %w", err)
		}
	}
	return counts, tx.Commit()
}

// Tombstoned reports whether the user deleted the built-in collection with this seed ID.
func Tombstoned(db *sql.DB, seedID string) (bool, error) {
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM seed_tombstones WHERE seed_id = ?`, seedID).Scan(&n); err != nil {
		return false, fmt.Errorf("Tombstoned: %w", err)
	}
	return n > 0, nil
}
