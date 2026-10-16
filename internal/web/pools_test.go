package web_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/Horac3/Primus/internal/insight"
	"github.com/Horac3/Primus/internal/model"
	"github.com/Horac3/Primus/internal/store"
	"github.com/Horac3/Primus/internal/web"
)

// Known secrets the leak checks grep for.
const (
	alicePassword = "pw-alice-7Q2x"
	bobPassword   = "pw-bob-9K4z"
)

// loginTarget is a target with alice and bob: POST /login (JSON) returns
// {"data":{"token":"tok-<user>-secret"}}; GET /me needs that bearer token.
type loginTarget struct {
	srv  *httptest.Server
	mu   sync.Mutex
	seen map[string]int // Authorization header -> hits
}

func newLoginTarget(t *testing.T) *loginTarget {
	t.Helper()
	lt := &loginTarget{seen: map[string]int{}}
	passwords := map[string]string{"alice": alicePassword, "bob": bobPassword}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /login", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Username, Password string }
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || passwords[in.Username] != in.Password || in.Password == "" {
			http.Error(w, "nope", http.StatusUnauthorized)
			return
		}
		fmt.Fprintf(w, `{"data":{"token":"tok-%s-secret"}}`, in.Username)
	})
	mux.HandleFunc("GET /me", func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		lt.mu.Lock()
		lt.seen[h]++
		lt.mu.Unlock()
		if !strings.HasPrefix(h, "Bearer tok-") {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	lt.srv = httptest.NewServer(mux)
	t.Cleanup(lt.srv.Close)
	return lt
}

func (lt *loginTarget) headers() map[string]int {
	lt.mu.Lock()
	defer lt.mu.Unlock()
	out := map[string]int{}
	for k, v := range lt.seen {
		out[k] = v
	}
	return out
}

func postForm(t *testing.T, mux http.Handler, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

func getPath(t *testing.T, mux http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
	return rr
}

// importPool imports a CSV pool through the UI and returns its id.
func importPool(t *testing.T, db *sql.DB, mux http.Handler, csv string) string {
	t.Helper()
	rr := postForm(t, mux, "/pools/import", url.Values{"name": {"shop users"}, "content": {csv}})
	if rr.Code != http.StatusOK {
		t.Fatalf("POST /pools/import = %d: %s", rr.Code, rr.Body.String())
	}
	pools, err := store.ListCredentialPools(db)
	if err != nil || len(pools) == 0 {
		t.Fatalf("no pool stored after import (err %v)", err)
	}
	return pools[len(pools)-1].ID
}

func configurePool(t *testing.T, mux http.Handler, id string, lt *loginTarget, extra url.Values) {
	t.Helper()
	form := url.Values{
		"login_method":       {"POST"},
		"login_url":          {lt.srv.URL + "/login"},
		"login_headers_json": {`{"Content-Type":"application/json"}`},
		"login_body":         {`{"username":"{{username}}","password":"{{password}}"}`},
		"extract_from":       {"body"},
		"extract_path":       {"data.token"},
		"apply_header":       {"Authorization"},
		"apply_format":       {"Bearer {{token}}"},
		"login_concurrency":  {"5"},
	}
	for k, v := range extra {
		form[k] = v
	}
	rr := postForm(t, mux, "/pools/"+id+"/spec", form)
	if rr.Code != http.StatusOK {
		t.Fatalf("POST /pools/%s/spec = %d: %s", id, rr.Code, rr.Body.String())
	}
}

func poolCSV() string {
	return "username,password\nalice," + alicePassword + "\nbob," + bobPassword + "\n"
}

// ---- import + masked view ---------------------------------------------------

func TestPoolImport_ReportsRowsAndMasksPasswords(t *testing.T) {
	// Arrange
	db := newTestDB(t)
	mux := web.NewServer(db)
	csv := poolCSV() + "carol\n"

	// Act
	rr := postForm(t, mux, "/pools/import", url.Values{"name": {"shop users"}, "content": {csv}})

	// Assert
	body := rr.Body.String()
	if rr.Code != http.StatusOK || !strings.Contains(body, "2 credentials") || !strings.Contains(body, "Row 4") {
		t.Errorf("status %d, body should report 2 credentials and a Row 4 error:\n%s", rr.Code, body)
	}
	if strings.Contains(body, alicePassword) || strings.Contains(body, bobPassword) {
		t.Errorf("import response leaks a password:\n%s", body)
	}
	if !strings.Contains(body, "alice") {
		t.Errorf("import response should list usernames:\n%s", body)
	}
}

func TestPoolImport_AcceptsJSON(t *testing.T) {
	// Arrange
	db := newTestDB(t)
	mux := web.NewServer(db)
	content := `[{"email":"a@x.io","password":"p1"},{"email":"b@x.io","password":"p2"}]`

	// Act
	rr := postForm(t, mux, "/pools/import", url.Values{"name": {"json users"}, "content": {content}})

	// Assert
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "2 credentials") {
		t.Errorf("status %d, want 2 credentials imported:\n%s", rr.Code, rr.Body.String())
	}
}

func TestPoolImport_OverCapIsRejected(t *testing.T) {
	// Arrange
	db := newTestDB(t)
	mux := web.NewServer(db)
	var csv strings.Builder
	csv.WriteString("username\n")
	for i := 0; i <= model.MaxCredentialPoolRows; i++ {
		fmt.Fprintf(&csv, "u%d\n", i)
	}

	// Act
	rr := postForm(t, mux, "/pools/import", url.Values{"name": {"big"}, "content": {csv.String()}})

	// Assert
	pools, _ := store.ListCredentialPools(db)
	if rr.Code != http.StatusBadRequest || len(pools) != 0 {
		t.Errorf("status %d with %d pools stored, want 400 and none", rr.Code, len(pools))
	}
}

func TestPoolsFragment_ShowsMaskedPool(t *testing.T) {
	// Arrange
	db := newTestDB(t)
	mux := web.NewServer(db)
	id := importPool(t, db, mux, poolCSV())

	// Act
	rr := getPath(t, mux, "/pools-fragment?id="+id)

	// Assert
	body := rr.Body.String()
	if rr.Code != http.StatusOK || !strings.Contains(body, "bob") {
		t.Fatalf("status %d, want the pool's usernames:\n%s", rr.Code, body)
	}
	if strings.Contains(body, alicePassword) || strings.Contains(body, bobPassword) {
		t.Errorf("pool panel leaks a password:\n%s", body)
	}
}

// ---- spec + test login --------------------------------------------------------

func TestPoolSpec_SavedAndReused(t *testing.T) {
	// Arrange
	db := newTestDB(t)
	mux := web.NewServer(db)
	lt := newLoginTarget(t)
	id := importPool(t, db, mux, poolCSV())

	// Act
	configurePool(t, mux, id, lt, url.Values{"allow_reuse": {"on"}, "relogin_on_401": {"on"}})

	// Assert
	pool, err := store.GetCredentialPoolWithSecrets(db, id)
	if err != nil {
		t.Fatalf("load pool: %v", err)
	}
	if pool.Login.URL != lt.srv.URL+"/login" || pool.Login.Extraction.Path != "data.token" ||
		!pool.AllowReuse || !pool.ReloginOn401 || pool.LoginConcurrency != 5 || len(pool.Credentials) != 2 {
		t.Errorf("saved pool = %+v", pool)
	}
}

func TestPoolSpec_RejectsUnknownPlaceholder(t *testing.T) {
	// Arrange
	db := newTestDB(t)
	mux := web.NewServer(db)
	lt := newLoginTarget(t)
	id := importPool(t, db, mux, poolCSV())
	configurePool(t, mux, id, lt, nil)

	// Act
	rr := postForm(t, mux, "/pools/"+id+"/spec", url.Values{
		"login_url": {lt.srv.URL + "/login"}, "login_body": {`{"e":"{{email}}"}`},
		"extract_from": {"body"}, "extract_path": {"data.token"},
	})

	// Assert
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "{{email}}") {
		t.Errorf("status %d, want 400 naming {{email}}: %s", rr.Code, rr.Body.String())
	}
}

func TestPoolTestLogin_ShowsStatusAndMaskedToken(t *testing.T) {
	// Arrange
	db := newTestDB(t)
	mux := web.NewServer(db)
	lt := newLoginTarget(t)
	id := importPool(t, db, mux, poolCSV())
	configurePool(t, mux, id, lt, nil)

	// Act
	rr := postForm(t, mux, "/pools/"+id+"/test-login", url.Values{"index": {"1"}})

	// Assert
	body := rr.Body.String()
	if rr.Code != http.StatusOK || !strings.Contains(body, "200") || !strings.Contains(body, "cret") {
		t.Errorf("status %d, want HTTP 200 and the token's last 4 chars:\n%s", rr.Code, body)
	}
	if strings.Contains(body, "tok-bob-secret") || strings.Contains(body, bobPassword) {
		t.Errorf("test-login shows an unmasked secret:\n%s", body)
	}
}

// ---- active pool + runs ---------------------------------------------------------

func TestActivePool_ShownInAuthBar(t *testing.T) {
	// Arrange
	db := newTestDB(t)
	mux := web.NewServer(db)
	id := importPool(t, db, mux, poolCSV())

	// Act
	activated := postForm(t, mux, "/pools/active", url.Values{"pool_id": {id}}).Body.String()
	cleared := postForm(t, mux, "/pools/active", url.Values{"pool_id": {""}}).Body.String()

	// Assert
	if !strings.Contains(activated, "Credential pool: shop users") {
		t.Errorf("auth bar after activating should name the pool:\n%s", activated)
	}
	if strings.Contains(cleared, "Credential pool: shop users") {
		t.Errorf("auth bar after clearing still names the pool:\n%s", cleared)
	}
}

func runForm(lt *loginTarget, concurrency string) url.Values {
	return url.Values{
		"method": {"GET"}, "url": {lt.srv.URL + "/me"}, "mode": {"burst"},
		"concurrency": {concurrency}, "total_requests": {"10"}, "burst_count": {"10"},
	}
}

func TestRunWithActivePool_UsesPerUserTokensAndShowsLogins(t *testing.T) {
	// Arrange
	db := newTestDB(t)
	mux := web.NewServer(db)
	lt := newLoginTarget(t)
	if err := store.SaveGlobalAuth(db, model.AuthConfig{Type: model.AuthTypeBearer, Token: "global-tok"}); err != nil {
		t.Fatal(err)
	}
	id := importPool(t, db, mux, poolCSV())
	configurePool(t, mux, id, lt, nil)
	postForm(t, mux, "/pools/active", url.Values{"pool_id": {id}})

	// Act
	runID := postRuns(t, mux, runForm(lt, "2"))
	body := pollDone(t, mux, runID)

	// Assert
	if !strings.Contains(body, "Logins") || !strings.Contains(body, "2 / 2 succeeded") {
		t.Errorf("result should show a Logins section with 2 / 2 succeeded:\n%s", body)
	}
	headers := lt.headers()
	if headers["Bearer tok-alice-secret"] == 0 || headers["Bearer tok-bob-secret"] == 0 || headers["Bearer global-tok"] != 0 {
		t.Errorf("target saw %v; want both pool tokens and never the global token", headers)
	}
	run, err := store.GetRun(db, runID)
	if err != nil || run.Login == nil {
		t.Fatalf("persisted run has no login report (err %v)", err)
	}
}

func TestRunWithActivePool_ZeroLoginsShowsFailedVerdict(t *testing.T) {
	// Arrange
	db := newTestDB(t)
	mux := web.NewServer(db)
	lt := newLoginTarget(t)
	id := importPool(t, db, mux, "username,password\nalice,wrong\nbob,wrong\n")
	configurePool(t, mux, id, lt, nil)
	postForm(t, mux, "/pools/active", url.Values{"pool_id": {id}})

	// Act
	runID := postRuns(t, mux, runForm(lt, "2"))
	body := pollDone(t, mux, runID)

	// Assert
	if !strings.Contains(body, "FAILED — no successful logins") || !strings.Contains(body, "HTTP 401") {
		t.Errorf("result should fail with no successful logins and show HTTP 401:\n%s", body)
	}
	if len(lt.headers()) != 0 {
		t.Errorf("target /me was hit %v; nothing may be dispatched", lt.headers())
	}
}

func TestRunWithActivePool_TooFewCredentialsRejected(t *testing.T) {
	// Arrange
	db := newTestDB(t)
	mux := web.NewServer(db)
	lt := newLoginTarget(t)
	id := importPool(t, db, mux, poolCSV())
	configurePool(t, mux, id, lt, nil)
	postForm(t, mux, "/pools/active", url.Values{"pool_id": {id}})

	// Act
	rr := postForm(t, mux, "/runs", runForm(lt, "5"))

	// Assert
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "allow credential reuse") {
		t.Errorf("status %d, want 400 mentioning allow credential reuse: %s", rr.Code, rr.Body.String())
	}
}

// ---- secrets never leak ------------------------------------------------------------

func TestPoolSecrets_NeverInLogsExportsOrAnalyzerInput(t *testing.T) {
	// Arrange: capture the server log for the whole flow, including a
	// failing login (bob's password is wrong at the target).
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	db := newTestDB(t)
	mux := web.NewServer(db)
	lt := newLoginTarget(t)
	csv := "username,password\nalice," + alicePassword + "\nbob,wrong-" + bobPassword + "\n"
	id := importPool(t, db, mux, csv)
	configurePool(t, mux, id, lt, nil)
	postForm(t, mux, "/pools/active", url.Values{"pool_id": {id}})
	postForm(t, mux, "/pools/"+id+"/test-login", url.Values{"index": {"1"}})

	// Act
	runID := postRuns(t, mux, runForm(lt, "2"))
	page := pollDone(t, mux, runID)
	run, err := store.GetRun(db, runID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	export, _ := json.Marshal(run)
	summary := insight.BuildSummary(run)
	analyzerInput, _ := json.Marshal(struct {
		Summary  insight.Summary   `json:"summary"`
		Findings []insight.Finding `json:"findings"`
	}{summary, insight.Evaluate(summary)})

	// Assert
	secrets := []string{alicePassword, bobPassword, "tok-alice-secret"}
	for name, captured := range map[string]string{
		"server log": logs.String(), "run export": string(export),
		"analyzer input": string(analyzerInput), "result page": page,
	} {
		for _, secret := range secrets {
			if strings.Contains(captured, secret) {
				t.Errorf("%s contains secret %q", name, secret)
			}
		}
	}
	if summary.Logins == nil || summary.Logins.Failed != 1 {
		t.Errorf("analyzer summary logins = %+v, want counts with 1 failure", summary.Logins)
	}
}
