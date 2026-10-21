package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Horac3/Primus/internal/credential"
	"github.com/Horac3/Primus/internal/model"
)

// PoolSummary lists a pool without its credentials.
type PoolSummary struct {
	ID    string
	Name  string
	Count int
}

// poolSpec is the persisted pool without its credentials, which live one
// per row in the credentials table.
func poolSpec(p model.CredentialPool) ([]byte, error) {
	p.Credentials = nil
	b, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("marshal credential pool: %w", err)
	}
	return b, nil
}

// SaveCredentialPool stores the pool's spec and replaces its credentials,
// atomically. Credential values are stored as given (passwords included);
// masking happens on the read APIs.
func SaveCredentialPool(db *sql.DB, p model.CredentialPool) error {
	if len(p.Credentials) > model.MaxCredentialPoolRows {
		return fmt.Errorf("SaveCredentialPool: %d credentials exceeds the %d-row limit",
			len(p.Credentials), model.MaxCredentialPoolRows)
	}
	spec, err := poolSpec(p)
	if err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("SaveCredentialPool: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT OR REPLACE INTO credential_pools (id, name, payload) VALUES (?,?,?)`,
		p.ID, p.Name, string(spec)); err != nil {
		return fmt.Errorf("SaveCredentialPool: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM credentials WHERE pool_id = ?`, p.ID); err != nil {
		return fmt.Errorf("SaveCredentialPool: clear credentials: %w", err)
	}
	stmt, err := tx.Prepare(`INSERT INTO credentials (pool_id, idx, fields) VALUES (?,?,?)`)
	if err != nil {
		return fmt.Errorf("SaveCredentialPool: %w", err)
	}
	defer stmt.Close()
	for i, c := range p.Credentials {
		fields, err := json.Marshal(c)
		if err != nil {
			return fmt.Errorf("SaveCredentialPool: marshal row %d: %w", i+1, err)
		}
		if _, err := stmt.Exec(p.ID, i, string(fields)); err != nil {
			return fmt.Errorf("SaveCredentialPool: insert row %d: %w", i+1, err)
		}
	}
	return tx.Commit()
}

// legacySecretMarkers is the substring rule masking used before per-field
// flags. migrateMaskedFields uses it so existing pools keep what they hid.
var legacySecretMarkers = []string{"pass", "secret", "token", "key", "pin", "otp"}

func legacySecretField(name string) bool {
	lower := strings.ToLower(name)
	for _, marker := range legacySecretMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// EffectiveMaskedFields is the pool's stored list or, for a pool with no
// stored list (nil), the old substring rule's fields, sorted: the same set
// migrateMaskedFields will store, so masking does not change on restart.
func EffectiveMaskedFields(p model.CredentialPool) []string {
	if p.MaskedFields != nil {
		return p.MaskedFields
	}
	masked := []string{}
	for _, name := range fieldNames(p.Credentials) {
		if legacySecretField(name) {
			masked = append(masked, name)
		}
	}
	sort.Strings(masked)
	return masked
}

// migrateMaskedFields gives every pool stored without masked_fields the
// fields the old substring rule masked. Pools that have a list are skipped,
// so it runs once per pool.
func migrateMaskedFields(db *sql.DB) error {
	rows, err := db.Query(`SELECT id FROM credential_pools WHERE json_type(payload, '$.masked_fields') IS NOT 'array'`)
	if err != nil {
		return fmt.Errorf("migrate masked fields: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("migrate masked fields: %w", err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("migrate masked fields: %w", err)
	}
	for _, id := range ids {
		p, err := GetCredentialPoolWithSecrets(db, id)
		if err != nil {
			return fmt.Errorf("migrate masked fields: %w", err)
		}
		p.MaskedFields = EffectiveMaskedFields(p) // nil here: no stored list
		if err := SaveCredentialPoolSpec(db, p); err != nil {
			return fmt.Errorf("migrate masked fields: %w", err)
		}
	}
	return nil
}

// fieldNames lists each distinct field name across creds, in first-seen order.
func fieldNames(creds []model.Credential) []string {
	seen := map[string]bool{}
	var names []string
	for _, c := range creds {
		for name := range c {
			if !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
	}
	return names
}

// SaveCredentialPoolSpec updates the pool's name, login and apply settings,
// keeping its stored credentials. The pool must already exist.
func SaveCredentialPoolSpec(db *sql.DB, p model.CredentialPool) error {
	spec, err := poolSpec(p)
	if err != nil {
		return err
	}
	res, err := db.Exec(`UPDATE credential_pools SET name = ?, payload = ? WHERE id = ?`, p.Name, string(spec), p.ID)
	if err != nil {
		return fmt.Errorf("SaveCredentialPoolSpec: %w", err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return fmt.Errorf("SaveCredentialPoolSpec %q: %w", p.ID, sql.ErrNoRows)
	}
	return nil
}

// GetCredentialPoolWithSecrets loads a pool with real credential values.
// Only the engine (and test-login) should call it; UI reads use
// GetCredentialPoolMasked.
func GetCredentialPoolWithSecrets(db *sql.DB, id string) (model.CredentialPool, error) {
	var payload string
	if err := db.QueryRow(`SELECT payload FROM credential_pools WHERE id = ?`, id).Scan(&payload); err != nil {
		return model.CredentialPool{}, fmt.Errorf("GetCredentialPool %q: %w", id, err)
	}
	var p model.CredentialPool
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		return model.CredentialPool{}, fmt.Errorf("unmarshal credential pool: %w", err)
	}
	rows, err := db.Query(`SELECT fields FROM credentials WHERE pool_id = ? ORDER BY idx`, id)
	if err != nil {
		return model.CredentialPool{}, fmt.Errorf("GetCredentialPool %q credentials: %w", id, err)
	}
	defer rows.Close()
	for rows.Next() {
		var fields string
		if err := rows.Scan(&fields); err != nil {
			return model.CredentialPool{}, err
		}
		var c model.Credential
		if err := json.Unmarshal([]byte(fields), &c); err != nil {
			return model.CredentialPool{}, fmt.Errorf("unmarshal credential: %w", err)
		}
		p.Credentials = append(p.Credentials, c)
	}
	return p, rows.Err()
}

// GetCredentialPoolMasked loads a pool with its flagged fields masked. A pool
// with no stored flags (nil) masks the fields whose names look secret.
func GetCredentialPoolMasked(db *sql.DB, id string) (model.CredentialPool, error) {
	p, err := GetCredentialPoolWithSecrets(db, id)
	if err != nil {
		return model.CredentialPool{}, err
	}
	masked := EffectiveMaskedFields(p)
	for i, c := range p.Credentials {
		p.Credentials[i] = credential.Mask(c, masked)
	}
	return p, nil
}

// ListCredentialPools lists every pool without credentials, by name.
func ListCredentialPools(db *sql.DB) ([]PoolSummary, error) {
	rows, err := db.Query(`SELECT p.id, p.name, COUNT(c.idx)
		FROM credential_pools p LEFT JOIN credentials c ON c.pool_id = p.id
		GROUP BY p.id, p.name ORDER BY p.name, p.id`)
	if err != nil {
		return nil, fmt.Errorf("ListCredentialPools: %w", err)
	}
	defer rows.Close()
	var out []PoolSummary
	for rows.Next() {
		var s PoolSummary
		if err := rows.Scan(&s.ID, &s.Name, &s.Count); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// SetActiveCredentialPool selects the pool runs use instead of global auth.
// An empty id clears it, so runs fall back to global auth.
func SetActiveCredentialPool(db *sql.DB, id string) error {
	var err error
	if id == "" {
		_, err = db.Exec(`DELETE FROM active_credential_pool WHERE id = 1`)
	} else {
		_, err = db.Exec(`INSERT OR REPLACE INTO active_credential_pool (id, pool_id) VALUES (1, ?)`, id)
	}
	if err != nil {
		return fmt.Errorf("SetActiveCredentialPool: %w", err)
	}
	return nil
}

// GetActiveCredentialPool returns the active pool id, or "" for global auth.
func GetActiveCredentialPool(db *sql.DB) (string, error) {
	var id string
	err := db.QueryRow(`SELECT pool_id FROM active_credential_pool WHERE id = 1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("GetActiveCredentialPool: %w", err)
	}
	return id, nil
}
