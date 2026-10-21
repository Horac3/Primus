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
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Horac3/Primus/internal/model"
	"github.com/Horac3/Primus/internal/store"
	"github.com/Horac3/Primus/internal/web"
)

// provTarget is a target where accounts do not exist yet: POST /signup
// creates {email,password}; POST /login then accepts exactly those; GET /me
// needs the issued bearer token. After blockAfter sign-ups (when > 0), the
// next ones hang until the client gives up.
type provTarget struct {
	srv        *httptest.Server
	blockAfter int32

	hits atomic.Int32
	mu   sync.Mutex
	pw   map[string]string // email -> password
	// reject answers 409 for these emails.
	reject map[string]bool
}

func newProvTarget(t *testing.T) *provTarget {
	t.Helper()
	p := &provTarget{pw: map[string]string{}, reject: map[string]bool{}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /signup", func(w http.ResponseWriter, r *http.Request) {
		n := p.hits.Add(1)
		var in struct{ Email, Password string }
		_ = json.NewDecoder(r.Body).Decode(&in)
		if p.blockAfter > 0 && n > p.blockAfter {
			<-r.Context().Done()
			return
		}
		if p.reject[in.Email] {
			http.Error(w, `{"error":"exists"}`, http.StatusConflict)
			return
		}
		p.mu.Lock()
		p.pw[in.Email] = in.Password
		p.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"id":"u-%d"}`, n)
	})
	mux.HandleFunc("POST /login", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Email, Password string }
		_ = json.NewDecoder(r.Body).Decode(&in)
		p.mu.Lock()
		want, ok := p.pw[in.Email]
		p.mu.Unlock()
		if !ok || want != in.Password {
			http.Error(w, "nope", http.StatusUnauthorized)
			return
		}
		fmt.Fprintf(w, `{"data":{"token":"tok-%s"}}`, in.Email)
	})
	mux.HandleFunc("GET /me", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer tok-") {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	})
	p.srv = httptest.NewServer(mux)
	t.Cleanup(p.srv.Close)
	return p
}

func (p *provTarget) form(count string, extra url.Values) url.Values {
	form := url.Values{
		"count":            {count},
		"fields":           {"email=u{{seq}}@x.test\npassword={{secret}}"},
		"signup_method":    {"POST"},
		"signup_url":       {p.srv.URL + "/signup"},
		"signup_headers":   {`{"Content-Type":"application/json"}`},
		"signup_body":      {`{"email":"{{email}}","password":"{{password}}"}`},
		"name":             {"fresh users"},
		"concurrency":      {"1"},
		"extract_field":    {""},
		"extract_from":     {"body"},
		"extract_path":     {""},
		"extract_prefix":   {""},
		"existing_pool_id": {""},
	}
	for k, v := range extra {
		form[k] = v
	}
	return form
}

var provStatusURL = regexp.MustCompile(`/pools/provision/([^/"]+)/(status|stop)\?pool=([^"&]+)`)

// provJob returns the job and pool ids from a response that polls the job.
func provJob(t *testing.T, body string) (job, pool string) {
	t.Helper()
	m := provStatusURL.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("response has no provisioning poll link:\n%s", body)
	}
	return m[1], m[3]
}

// pollProvision polls the job's status until the panel stops polling.
func pollProvision(t *testing.T, mux http.Handler, job, pool string) string {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		rr := getPath(t, mux, "/pools/provision/"+job+"/status?pool="+pool)
		if rr.Code != http.StatusOK {
			t.Fatalf("status poll = %d: %s", rr.Code, rr.Body.String())
		}
		if !strings.Contains(rr.Body.String(), "provision-running") {
			return rr.Body.String()
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("provisioning did not finish")
	return ""
}

func startProvision(t *testing.T, mux http.Handler, form url.Values) (job, pool, body string) {
	t.Helper()
	rr := postForm(t, mux, "/pools/provision", form)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("POST /pools/provision = %d: %s", rr.Code, rr.Body.String())
	}
	job, pool = provJob(t, rr.Body.String())
	return job, pool, rr.Body.String()
}

func TestProvision_InvalidInputIsRejectedBeforeAnyRequest(t *testing.T) {
	db := newTestDB(t)
	mux := web.NewServer(db)
	pt := newProvTarget(t)

	cases := map[string]url.Values{
		"zero count":          pt.form("0", nil),
		"over the row cap":    pt.form(fmt.Sprint(model.MaxCredentialPoolRows+1), nil),
		"not a number":        pt.form("many", nil),
		"no url":              pt.form("2", url.Values{"signup_url": {""}}),
		"no fields":           pt.form("2", url.Values{"fields": {""}}),
		"unknown placeholder": pt.form("2", url.Values{"signup_body": {`{"x":"{{nickname}}"}`}}),
		"field uses unknown":  pt.form("2", url.Values{"fields": {"email={{nickname}}"}}),
		"no pool or name":     pt.form("2", url.Values{"name": {""}}),
		"bad headers json":    pt.form("2", url.Values{"signup_headers": {"{"}}),
	}
	for name, form := range cases {
		t.Run(name, func(t *testing.T) {
			rr := postForm(t, mux, "/pools/provision", form)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", rr.Code, rr.Body.String())
			}
		})
	}
	if got := pt.hits.Load(); got != 0 {
		t.Fatalf("target received %d sign-ups, want 0", got)
	}
	if pools, _ := store.ListCredentialPools(db); len(pools) != 0 {
		t.Fatalf("a rejected request created %d pools", len(pools))
	}
}

func TestProvision_NewPoolEndToEnd(t *testing.T) {
	db := newTestDB(t)
	mux := web.NewServer(db)
	pt := newProvTarget(t)

	job, poolID, _ := startProvision(t, mux, pt.form("3", url.Values{
		"extract_field": {"user_id"}, "extract_path": {"id"},
	}))
	body := pollProvision(t, mux, job, poolID)

	pool, err := store.GetCredentialPoolWithSecrets(db, poolID)
	if err != nil {
		t.Fatalf("pool not stored: %v", err)
	}
	if pool.Name != "fresh users" || len(pool.Credentials) != 3 {
		t.Fatalf("pool %q has %d rows", pool.Name, len(pool.Credentials))
	}
	for _, c := range pool.Credentials {
		if pt.pw[c["email"]] != c["password"] || c["password"] == "" {
			t.Errorf("row %q does not match the account on the target", c["email"])
		}
		if !strings.HasPrefix(c["user_id"], "u-") {
			t.Errorf("user_id not extracted: %v", c["user_id"])
		}
	}
	if !strings.Contains(strings.Join(pool.MaskedFields, ","), "password") {
		t.Errorf("password must be masked, got %v", pool.MaskedFields)
	}
	if !strings.Contains(body, "3 of 3") {
		t.Errorf("panel should report 3 of 3 created:\n%s", body)
	}
	for _, c := range pool.Credentials {
		if strings.Contains(body, c["password"]) {
			t.Fatalf("panel leaks a generated password")
		}
	}
}

func TestProvision_AppendsToExistingPool(t *testing.T) {
	db := newTestDB(t)
	mux := web.NewServer(db)
	pt := newProvTarget(t)
	id := importPool(t, db, mux, "email,password\nseed@x.test,seed-pw-1\n")

	job, poolID, _ := startProvision(t, mux, pt.form("2", url.Values{
		"existing_pool_id": {id}, "name": {""},
	}))
	pollProvision(t, mux, job, poolID)

	pool, _ := store.GetCredentialPoolWithSecrets(db, id)
	if len(pool.Credentials) != 3 || pool.Credentials[0]["email"] != "seed@x.test" {
		t.Fatalf("rows = %v", pool.Credentials)
	}
	if pool.Credentials[1]["email"] != "u2@x.test" || pool.Credentials[2]["email"] != "u3@x.test" {
		t.Errorf("seq should continue after the existing rows: %v", pool.Credentials)
	}
	masked, _ := store.GetCredentialPoolMasked(db, id)
	for _, c := range masked.Credentials {
		if strings.Contains(c["password"], "seed-pw") || c["password"] == pool.Credentials[1]["password"] {
			t.Errorf("masked read leaks a password: %v", c)
		}
	}
}

func TestProvision_CountBeyondRemainingCapIsRejected(t *testing.T) {
	db := newTestDB(t)
	mux := web.NewServer(db)
	pt := newProvTarget(t)
	id := importPool(t, db, mux, "email,password\nseed@x.test,pw\n")

	rr := postForm(t, mux, "/pools/provision", pt.form(fmt.Sprint(model.MaxCredentialPoolRows), url.Values{
		"existing_pool_id": {id}, "name": {""},
	}))
	if rr.Code != http.StatusBadRequest || pt.hits.Load() != 0 {
		t.Fatalf("status %d, hits %d: want 400 and no requests", rr.Code, pt.hits.Load())
	}
}

func TestProvision_StopKeepsFinishedRowsAndCountsRestCancelled(t *testing.T) {
	db := newTestDB(t)
	mux := web.NewServer(db)
	pt := newProvTarget(t)
	pt.blockAfter = 1 // the 2nd sign-up hangs until Stop

	job, poolID, _ := startProvision(t, mux, pt.form("5", nil))
	deadline := time.Now().Add(5 * time.Second)
	for pt.hits.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if rr := postForm(t, mux, "/pools/provision/"+job+"/stop?pool="+poolID, nil); rr.Code != http.StatusOK {
		t.Fatalf("stop = %d: %s", rr.Code, rr.Body.String())
	}
	body := pollProvision(t, mux, job, poolID)

	pool, err := store.GetCredentialPoolWithSecrets(db, poolID)
	if err != nil || len(pool.Credentials) != 1 {
		t.Fatalf("want the 1 finished account kept (err %v, pool %+v)", err, pool)
	}
	if !strings.Contains(body, "4 cancelled") || !strings.Contains(body, "0 failed") {
		t.Errorf("stopped sign-ups must count as cancelled, not failed:\n%s", body)
	}
}

func TestProvision_UnknownJobDoesNotCrash(t *testing.T) {
	mux := web.NewServer(newTestDB(t))
	rr := getPath(t, mux, "/pools/provision/nope/status?pool=x")
	if rr.Code != http.StatusOK || strings.Contains(rr.Body.String(), "provision-running") {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
}

func TestProvision_FailuresAreReportedByReasonOnly(t *testing.T) {
	db := newTestDB(t)
	mux := web.NewServer(db)
	pt := newProvTarget(t)
	pt.srv.Close() // every sign-up is a connection error

	job, poolID, _ := startProvision(t, mux, pt.form("2", nil))
	body := pollProvision(t, mux, job, poolID)

	if !strings.Contains(body, "connection error") || !strings.Contains(body, "0 of 2") {
		t.Errorf("want failure reason and counts:\n%s", body)
	}
	if strings.Contains(body, pt.srv.URL) || strings.Contains(body, "127.0.0.1") {
		t.Errorf("panel leaks the target URL:\n%s", body)
	}
	if pools, _ := store.ListCredentialPools(db); len(pools) != 0 {
		t.Errorf("a provisioning with no successes created %d pools", len(pools))
	}
}

func TestProvision_ProvisionedPoolRunsLikeAnImportedOne(t *testing.T) {
	db := newTestDB(t)
	mux := web.NewServer(db)
	pt := newProvTarget(t)

	job, poolID, _ := startProvision(t, mux, pt.form("3", nil))
	pollProvision(t, mux, job, poolID)
	rr := postForm(t, mux, "/pools/"+poolID+"/spec", url.Values{
		"login_method": {"POST"}, "login_url": {pt.srv.URL + "/login"},
		"login_headers_json": {`{"Content-Type":"application/json"}`},
		"login_body":         {`{"email":"{{email}}","password":"{{password}}"}`},
		"extract_from":       {"body"}, "extract_path": {"data.token"},
		"apply_header": {"Authorization"}, "apply_format": {"Bearer {{token}}"},
		"login_concurrency": {"3"},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("spec = %d: %s", rr.Code, rr.Body.String())
	}
	postForm(t, mux, "/pools/active", url.Values{"pool_id": {poolID}})

	runID := postRuns(t, mux, url.Values{
		"method": {"GET"}, "url": {pt.srv.URL + "/me"}, "mode": {"burst"},
		"concurrency": {"3"}, "total_requests": {"9"}, "burst_count": {"9"},
	})
	body := pollDone(t, mux, runID)
	if !strings.Contains(body, "3 / 3 succeeded") {
		t.Errorf("provisioned accounts should all log in:\n%s", body)
	}
}

func TestProvision_SecretsNeverInLogsPagesOrExports(t *testing.T) {
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	db := newTestDB(t)
	mux := web.NewServer(db)
	pt := newProvTarget(t)

	job, poolID, started := startProvision(t, mux, pt.form("3", nil))
	done := pollProvision(t, mux, job, poolID)
	fragment := getPath(t, mux, "/pools-fragment?id="+poolID).Body.String()
	pool, _ := store.GetCredentialPoolWithSecrets(db, poolID)
	var specOnly sql.NullString
	if err := db.QueryRow(`SELECT payload FROM credential_pools WHERE id = ?`, poolID).Scan(&specOnly); err != nil {
		t.Fatal(err)
	}

	for name, captured := range map[string]string{
		"server log": logs.String(), "start response": started,
		"done panel": done, "pool fragment": fragment, "stored spec": specOnly.String,
	} {
		for _, c := range pool.Credentials {
			if strings.Contains(captured, c["password"]) {
				t.Errorf("%s contains a generated password", name)
			}
		}
	}
}

func TestProvision_SecondJobForTheSamePoolIsRejectedWhileOneRuns(t *testing.T) {
	db := newTestDB(t)
	mux := web.NewServer(db)
	pt := newProvTarget(t)
	id := importPool(t, db, mux, "email,password\nseed@x.test,pw\n")

	hang := newProvTarget(t)
	hang.blockAfter = 1 // the 2nd sign-up hangs, keeping job 1 running
	job, poolID, _ := startProvision(t, mux, hang.form("3", url.Values{"existing_pool_id": {id}, "name": {""}}))
	deadline := time.Now().Add(5 * time.Second)
	for hang.hits.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	rr := postForm(t, mux, "/pools/provision", pt.form("2", url.Values{"existing_pool_id": {id}, "name": {""}}))
	if rr.Code != http.StatusConflict {
		t.Fatalf("second job status = %d, want 409: %s", rr.Code, rr.Body.String())
	}
	if pt.hits.Load() != 0 {
		t.Fatalf("the rejected job sent %d sign-ups", pt.hits.Load())
	}

	postForm(t, mux, "/pools/provision/"+job+"/stop?pool="+poolID, nil)
	pollProvision(t, mux, job, poolID)
	// the pool is free again
	job2, pool2, _ := startProvision(t, mux, pt.form("1", url.Values{"existing_pool_id": {id}, "name": {""}}))
	pollProvision(t, mux, job2, pool2)
}

func TestProvision_SeqContinuesPastGapsLeftByFailedSignups(t *testing.T) {
	db := newTestDB(t)
	mux := web.NewServer(db)
	pt := newProvTarget(t)
	pt.reject["u2@x.test"] = true

	job, poolID, _ := startProvision(t, mux, pt.form("5", nil))
	pollProvision(t, mux, job, poolID)
	job2, pool2, _ := startProvision(t, mux, pt.form("2", url.Values{"existing_pool_id": {poolID}, "name": {""}}))
	pollProvision(t, mux, job2, pool2)

	pool, _ := store.GetCredentialPoolWithSecrets(db, poolID)
	var emails []string
	for _, c := range pool.Credentials {
		emails = append(emails, c["email"])
	}
	want := "u1@x.test,u3@x.test,u4@x.test,u5@x.test,u6@x.test,u7@x.test"
	if got := strings.Join(emails, ","); got != want {
		t.Fatalf("emails = %s, want %s (seq 2 failed, so the next run must start at 6)", got, want)
	}
}

func TestProvision_ConcurrencyAboveThePoolLimitIsRejected(t *testing.T) {
	db := newTestDB(t)
	mux := web.NewServer(db)
	pt := newProvTarget(t)
	id := importPool(t, db, mux, "email,password\nseed@x.test,pw\n") // login concurrency 20

	rr := postForm(t, mux, "/pools/provision", pt.form("2", url.Values{
		"existing_pool_id": {id}, "name": {""}, "concurrency": {"21"},
	}))
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "at most 20") {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	rr = postForm(t, mux, "/pools/provision", pt.form("2", url.Values{"concurrency": {"21"}}))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("new pool: status %d, want 400", rr.Code)
	}
	if pt.hits.Load() != 0 {
		t.Fatalf("target received %d sign-ups, want 0", pt.hits.Load())
	}
}

func TestProvision_ExtractedFieldCannotOverwriteAGeneratedField(t *testing.T) {
	mux := web.NewServer(newTestDB(t))
	pt := newProvTarget(t)
	for name, field := range map[string]string{"existing field": "password", "bad name": "a b"} {
		t.Run(name, func(t *testing.T) {
			rr := postForm(t, mux, "/pools/provision", pt.form("1", url.Values{
				"extract_field": {field}, "extract_path": {"id"},
			}))
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status %d, want 400: %s", rr.Code, rr.Body.String())
			}
		})
	}
	if pt.hits.Load() != 0 {
		t.Fatalf("target received %d sign-ups, want 0", pt.hits.Load())
	}
}
