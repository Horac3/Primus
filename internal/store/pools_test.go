package store_test

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Horac3/Primus/internal/model"
	"github.com/Horac3/Primus/internal/store"
)

func migratedDB(t *testing.T) *sql.DB {
	t.Helper()
	db, cleanup := tempDB(t)
	t.Cleanup(cleanup)
	if err := store.Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return db
}

func samplePool() model.CredentialPool {
	return model.CredentialPool{
		ID:   "pool-1",
		Name: "shop users",
		Credentials: []model.Credential{
			{"username": "alice", "password": "pw-alice"},
			{"username": "bob", "password": "pw-bob"},
		},
		Login: model.LoginSpec{
			Method:     "POST",
			URL:        "http://target/login",
			Headers:    map[string]string{"Content-Type": "application/json"},
			Body:       `{"u":"{{username}}","p":"{{password}}"}`,
			Extraction: model.Extraction{From: model.ExtractFromBody, Path: "data.token"},
		},
		Apply:            model.ApplySpec{Header: "Authorization", Format: "Bearer {{token}}"},
		LoginConcurrency: 5,
		ReloginOn401:     true,
	}
}

func TestCredentialPool_RoundTripWithSecrets(t *testing.T) {
	// Arrange
	db := migratedDB(t)
	pool := samplePool()

	// Act
	if err := store.SaveCredentialPool(db, pool); err != nil {
		t.Fatalf("SaveCredentialPool: %v", err)
	}
	got, err := store.GetCredentialPoolWithSecrets(db, pool.ID)

	// Assert
	if err != nil {
		t.Fatalf("GetCredentialPoolWithSecrets: %v", err)
	}
	wantJSON, _ := json.Marshal(pool)
	gotJSON, _ := json.Marshal(got)
	if string(gotJSON) != string(wantJSON) {
		t.Errorf("round trip:\n got %s\nwant %s", gotJSON, wantJSON)
	}
}

func TestCredentialPool_MaskedReadHidesPasswords(t *testing.T) {
	// Arrange
	db := migratedDB(t)
	if err := store.SaveCredentialPool(db, samplePool()); err != nil {
		t.Fatalf("SaveCredentialPool: %v", err)
	}

	// Act
	got, err := store.GetCredentialPoolMasked(db, "pool-1")

	// Assert
	if err != nil {
		t.Fatalf("GetCredentialPoolMasked: %v", err)
	}
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), "pw-alice") || strings.Contains(string(raw), "pw-bob") {
		t.Errorf("masked read leaks a password: %s", raw)
	}
	if got.Credentials[0]["username"] != "alice" {
		t.Errorf("username = %q, want it readable", got.Credentials[0]["username"])
	}
}

func TestCredentialPool_MaskedReadFollowsStoredFlags(t *testing.T) {
	// Arrange
	db := migratedDB(t)
	pool := samplePool()
	pool.Credentials = []model.Credential{{"username": "alice", "password": "pw-alice", "nickname": "ally"}}
	pool.MaskedFields = []string{"nickname"}
	if err := store.SaveCredentialPool(db, pool); err != nil {
		t.Fatalf("SaveCredentialPool: %v", err)
	}

	// Act
	masked, err := store.GetCredentialPoolMasked(db, pool.ID)
	if err != nil {
		t.Fatalf("GetCredentialPoolMasked: %v", err)
	}
	real, err := store.GetCredentialPoolWithSecrets(db, pool.ID)
	if err != nil {
		t.Fatalf("GetCredentialPoolWithSecrets: %v", err)
	}

	// Assert
	if masked.Credentials[0]["nickname"] == "ally" {
		t.Error("flagged nickname is readable in the masked read")
	}
	if masked.Credentials[0]["password"] != "pw-alice" {
		t.Errorf("unflagged password = %q, want it readable", masked.Credentials[0]["password"])
	}
	if real.Credentials[0]["nickname"] != "ally" {
		t.Errorf("dispatch read nickname = %q, want the real value", real.Credentials[0]["nickname"])
	}
	if strings.Join(masked.MaskedFields, ",") != "nickname" {
		t.Errorf("MaskedFields = %v, want [nickname]", masked.MaskedFields)
	}
}

func TestCredentialPool_NilFlagsMaskLikeTheMigration(t *testing.T) {
	// Arrange: no stored flags; the next Migrate would apply the old rule.
	db := migratedDB(t)
	pool := samplePool()
	pool.Credentials = []model.Credential{{"username": "alice", "shipping": "addr-1"}}
	if err := store.SaveCredentialPool(db, pool); err != nil {
		t.Fatal(err)
	}

	// Act
	got, err := store.GetCredentialPoolMasked(db, pool.ID)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if got.Credentials[0]["shipping"] == "addr-1" {
		t.Error("shipping is readable before migration but masked after it; want the same masking both times")
	}
}

func TestMigrate_KeepsOldRuleMaskingForExistingPools(t *testing.T) {
	// Arrange: a pool stored before per-field flags (payload has no masked_fields).
	db := migratedDB(t)
	if _, err := db.Exec(`INSERT INTO credential_pools (id, name, payload) VALUES ('old', 'old', '{"id":"old","name":"old"}')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO credentials (pool_id, idx, fields) VALUES ('old', 0, '{"username":"u","password":"p","shipping":"s"}')`); err != nil {
		t.Fatal(err)
	}
	empty := samplePool()
	empty.ID, empty.MaskedFields = "none-masked", []string{}
	if err := store.SaveCredentialPool(db, empty); err != nil {
		t.Fatal(err)
	}

	// Act: Migrate twice, as two restarts would.
	for range 2 {
		if err := store.Migrate(db); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
	}

	// Assert
	old, err := store.GetCredentialPoolWithSecrets(db, "old")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(old.MaskedFields, ","); got != "password,shipping" {
		t.Errorf("migrated MaskedFields = %q, want password,shipping (the old substring rule)", got)
	}
	kept, err := store.GetCredentialPoolWithSecrets(db, "none-masked")
	if err != nil {
		t.Fatal(err)
	}
	if kept.MaskedFields == nil || len(kept.MaskedFields) != 0 {
		t.Errorf("MaskedFields = %#v, want the stored empty list kept", kept.MaskedFields)
	}
}

func TestCredentialPool_SaveReplacesCredentials(t *testing.T) {
	// Arrange
	db := migratedDB(t)
	pool := samplePool()
	if err := store.SaveCredentialPool(db, pool); err != nil {
		t.Fatalf("SaveCredentialPool: %v", err)
	}
	pool.Credentials = []model.Credential{{"username": "carol", "password": "pw-carol"}}

	// Act
	if err := store.SaveCredentialPool(db, pool); err != nil {
		t.Fatalf("SaveCredentialPool (replace): %v", err)
	}
	got, err := store.GetCredentialPoolWithSecrets(db, pool.ID)

	// Assert
	if err != nil || len(got.Credentials) != 1 || got.Credentials[0]["username"] != "carol" {
		t.Errorf("credentials = %v, err %v; want only carol", got.Credentials, err)
	}
}

func TestCredentialPool_SaveSpecKeepsCredentials(t *testing.T) {
	// Arrange
	db := migratedDB(t)
	pool := samplePool()
	if err := store.SaveCredentialPool(db, pool); err != nil {
		t.Fatalf("SaveCredentialPool: %v", err)
	}
	edited := pool
	edited.Credentials = nil
	edited.Login.URL = "http://target/v2/login"

	// Act
	if err := store.SaveCredentialPoolSpec(db, edited); err != nil {
		t.Fatalf("SaveCredentialPoolSpec: %v", err)
	}
	got, err := store.GetCredentialPoolWithSecrets(db, pool.ID)

	// Assert
	if err != nil || got.Login.URL != "http://target/v2/login" || len(got.Credentials) != 2 {
		t.Errorf("got url %q with %d credentials (err %v); want v2 url and 2 credentials",
			got.Login.URL, len(got.Credentials), err)
	}
}

func TestCredentialPool_SaveRejectsOverMaxRows(t *testing.T) {
	// Arrange
	db := migratedDB(t)
	pool := samplePool()
	pool.Credentials = make([]model.Credential, model.MaxCredentialPoolRows+1)
	for i := range pool.Credentials {
		pool.Credentials[i] = model.Credential{"username": "u"}
	}

	// Act
	err := store.SaveCredentialPool(db, pool)

	// Assert
	if err == nil {
		t.Error("SaveCredentialPool: want an error above the row cap")
	}
}

func TestListCredentialPools_CountsWithoutCredentials(t *testing.T) {
	// Arrange
	db := migratedDB(t)
	if err := store.SaveCredentialPool(db, samplePool()); err != nil {
		t.Fatalf("SaveCredentialPool: %v", err)
	}

	// Act
	pools, err := store.ListCredentialPools(db)

	// Assert
	if err != nil || len(pools) != 1 || pools[0].Name != "shop users" || pools[0].Count != 2 {
		t.Errorf("ListCredentialPools = %+v, err %v; want one pool with 2 credentials", pools, err)
	}
}

func TestGetCredentialPool_UnknownIDIsAnError(t *testing.T) {
	// Arrange
	db := migratedDB(t)

	// Act
	_, err := store.GetCredentialPoolWithSecrets(db, "missing")

	// Assert
	if err == nil {
		t.Error("want an error for an unknown pool id")
	}
}

func TestActiveCredentialPool_SetGetClear(t *testing.T) {
	// Arrange
	db := migratedDB(t)
	before, err := store.GetActiveCredentialPool(db)
	if err != nil || before != "" {
		t.Fatalf("initial active pool = %q, err %v; want empty (global auth)", before, err)
	}

	// Act
	setErr := store.SetActiveCredentialPool(db, "pool-1")
	afterSet, _ := store.GetActiveCredentialPool(db)
	clearErr := store.SetActiveCredentialPool(db, "")
	afterClear, _ := store.GetActiveCredentialPool(db)

	// Assert
	if setErr != nil || clearErr != nil || afterSet != "pool-1" || afterClear != "" {
		t.Errorf("set=%q (err %v), cleared=%q (err %v); want pool-1 then empty", afterSet, setErr, afterClear, clearErr)
	}
}

func TestAppendCredentials_AppendsAfterExistingRowsAndMergesMasking(t *testing.T) {
	db := migratedDB(t)
	pool := samplePool()
	pool.MaskedFields = []string{"password"}
	if err := store.SaveCredentialPool(db, pool); err != nil {
		t.Fatalf("SaveCredentialPool: %v", err)
	}

	rows := []model.Credential{
		{"username": "carol", "password": "pw-carol", "api_token": "t-1"},
		{"username": "dave", "password": "pw-dave", "api_token": "t-2"},
	}
	if err := store.AppendCredentials(db, pool.ID, rows, []string{"password", "api_token"}); err != nil {
		t.Fatalf("AppendCredentials: %v", err)
	}

	got, err := store.GetCredentialPoolWithSecrets(db, pool.ID)
	if err != nil {
		t.Fatalf("GetCredentialPoolWithSecrets: %v", err)
	}
	if len(got.Credentials) != 4 {
		t.Fatalf("rows = %d, want 4", len(got.Credentials))
	}
	if got.Credentials[1]["username"] != "bob" || got.Credentials[2]["username"] != "carol" || got.Credentials[3]["username"] != "dave" {
		t.Errorf("order not preserved: %v", got.Credentials)
	}
	if got.Login.URL != pool.Login.URL {
		t.Errorf("spec was changed: %+v", got.Login)
	}
	if strings.Join(got.MaskedFields, ",") != "api_token,password" {
		t.Errorf("masked fields = %v, want api_token and password once each", got.MaskedFields)
	}

	masked, err := store.GetCredentialPoolMasked(db, pool.ID)
	if err != nil {
		t.Fatalf("GetCredentialPoolMasked: %v", err)
	}
	for _, c := range masked.Credentials {
		if strings.Contains(c["password"], "pw-") || strings.Contains(c["api_token"], "t-") {
			t.Errorf("masked read leaks a secret: %v", c)
		}
	}
}

func TestAppendCredentials_RejectsOverTheRowCapAndLeavesPoolUntouched(t *testing.T) {
	db := migratedDB(t)
	pool := samplePool()
	if err := store.SaveCredentialPool(db, pool); err != nil {
		t.Fatalf("SaveCredentialPool: %v", err)
	}
	rows := make([]model.Credential, model.MaxCredentialPoolRows-1) // 2 existing + this > cap
	for i := range rows {
		rows[i] = model.Credential{"username": "u", "password": "p"}
	}
	if err := store.AppendCredentials(db, pool.ID, rows, nil); err == nil {
		t.Fatal("want an error when existing + new rows exceed the cap")
	}
	got, _ := store.GetCredentialPoolWithSecrets(db, pool.ID)
	if len(got.Credentials) != 2 {
		t.Fatalf("rows = %d, want the original 2", len(got.Credentials))
	}
}

func TestAppendCredentials_UnknownPool(t *testing.T) {
	db := migratedDB(t)
	err := store.AppendCredentials(db, "nope", []model.Credential{{"a": "b"}}, nil)
	if err == nil {
		t.Fatal("want an error for an unknown pool")
	}
}
