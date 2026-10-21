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
