package web_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Horac3/Primus/internal/store"
	"github.com/Horac3/Primus/internal/web"
)

func postRun(t *testing.T, mux http.Handler, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/runs", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

// FR-GUARD-02/03: the web run form refuses a heavy run against a protected host, names the
// override, and accepts the run once allow_protected_host is the exact host.
func TestPostRun_ProtectedHost_HeavyRefusedUntilAllowed(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer target.Close()
	t.Setenv("PRIMUS_PROTECTED_HOSTS", "127.0.0.1")
	db := newTestDB(t)
	mux := web.NewServer(db)

	form := url.Values{
		"method": {"GET"}, "url": {target.URL + "/x"},
		"mode": {"load"}, "concurrency": {"5"}, "duration_seconds": {"1"},
	}
	rr := postRun(t, mux, form)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rr.Code, rr.Body.String())
	}
	if body := rr.Body.String(); !strings.Contains(body, "PRIMUS_PROTECTED_HOSTS") || !strings.Contains(body, "allow_protected_host") {
		t.Errorf("refusal should name the setting and the override: %s", body)
	}
	if runs, _ := store.ListAllRuns(db); len(runs) != 0 {
		t.Errorf("%d runs saved for a refused start, want 0", len(runs))
	}

	form.Set("allow_protected_host", "127.0.0.1")
	if rr := postRun(t, mux, form); rr.Code != http.StatusAccepted {
		t.Errorf("with the override: status = %d, want 202; body: %s", rr.Code, rr.Body.String())
	}
}

func TestPostRun_ProtectedHost_BurstIsNeverLight(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer target.Close()
	t.Setenv("PRIMUS_PROTECTED_HOSTS", "127.0.0.1")
	mux := web.NewServer(newTestDB(t))

	rr := postRun(t, mux, url.Values{
		"method": {"GET"}, "url": {target.URL + "/health"},
		"mode": {"burst"}, "concurrency": {"1"}, "total_requests": {"3"},
	})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("burst is never light: status = %d, want 400", rr.Code)
	}
}
