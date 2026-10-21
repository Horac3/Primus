package store

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/Horac3/Primus/internal/model"
)

// SeedResult says what SeedCollection did.
type SeedResult int

const (
	// SeedCreated: the collection, its endpoints and configs were written.
	SeedCreated SeedResult = iota
	// SeedAlreadyPresent: a collection with this seed ID exists; nothing was touched.
	SeedAlreadyPresent
	// SeedTombstoned: the user deleted this built-in collection and restore was not requested.
	SeedTombstoned
)

// SeedCollection writes a built-in collection with its endpoints and saved configs in one transaction.
// It never overwrites: a collection with the same seed ID (even renamed or edited) is left as it is.
// A deleted built-in collection stays gone unless restore is true, which also clears the tombstone.
// A user collection that already holds the name makes it fail with ErrDuplicateName, writing nothing.
func SeedCollection(db *sql.DB, c model.Collection, reqs []model.RequestDefinition, cfgs []model.TestConfig, restore bool) (SeedResult, error) {
	result := SeedCreated
	err := inImmediateTx(db, func(q queryer) error {
		var n int
		if err := q.QueryRow(`SELECT COUNT(*) FROM collections WHERE seed_id = ?`, c.SeedID).Scan(&n); err != nil {
			return fmt.Errorf("check seeded collection: %w", err)
		}
		if n > 0 {
			result = SeedAlreadyPresent
			return nil
		}
		if err := q.QueryRow(`SELECT COUNT(*) FROM seed_tombstones WHERE seed_id = ?`, c.SeedID).Scan(&n); err != nil {
			return fmt.Errorf("check tombstone: %w", err)
		}
		if n > 0 && !restore {
			result = SeedTombstoned
			return nil
		}
		if err := q.QueryRow(`SELECT COUNT(*) FROM collections WHERE name = ? COLLATE NOCASE`, c.Name).Scan(&n); err != nil {
			return fmt.Errorf("check collection name: %w", err)
		}
		if n > 0 {
			return fmt.Errorf("seed collection %q: %w", c.SeedID, ErrDuplicateName)
		}
		if _, err := q.Exec(
			`INSERT INTO collections (id, name, base_url, seed_id, seed_version, created_at) VALUES (?,?,?,?,?,datetime('now'))`,
			c.ID, c.Name, c.BaseURL, c.SeedID, c.SeedVersion,
		); err != nil {
			return fmt.Errorf("insert seed collection: %w", err)
		}
		for _, r := range reqs {
			r.CollectionID = c.ID
			b, err := json.Marshal(r)
			if err != nil {
				return fmt.Errorf("marshal RequestDefinition: %w", err)
			}
			if _, err := q.Exec(
				`INSERT INTO requests (id, name, method, url_template, payload, collection_id) VALUES (?,?,?,?,?,?)`,
				r.ID, r.Name, r.Method, r.URLTemplate, string(b), c.ID,
			); err != nil {
				return fmt.Errorf("insert seed endpoint: %w", err)
			}
		}
		for _, tc := range cfgs {
			b, err := json.Marshal(tc)
			if err != nil {
				return fmt.Errorf("marshal TestConfig: %w", err)
			}
			if _, err := q.Exec(`INSERT INTO test_configs (id, request_id, payload) VALUES (?,?,?)`, tc.ID, tc.RequestID, string(b)); err != nil {
				return fmt.Errorf("insert seed config: %w", err)
			}
		}
		if _, err := q.Exec(`DELETE FROM seed_tombstones WHERE seed_id = ?`, c.SeedID); err != nil {
			return fmt.Errorf("clear tombstone: %w", err)
		}
		return nil
	})
	if err != nil {
		return SeedCreated, err
	}
	return result, nil
}
