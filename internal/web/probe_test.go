package web_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/Horac3/Primus/internal/model"
	"github.com/Horac3/Primus/internal/store"
	"github.com/Horac3/Primus/internal/web"
)

// ============================================================
// Auth probe fields: attempts, max_rate, auth_source (FR-PROBE-01..04)
// ============================================================

func probeForm(lt *loginTarget, extra url.Values) url.Values {
	form := url.Values{
		"method": {"GET"}, "url": {lt.srv.URL + "/me"}, "mode": {"auth_probe"},
		"concurrency": {"2"}, "total_requests": {"4"},
	}
	for k, v := range extra {
		form[k] = v
	}
	return form
}

// probeWithActivePool stores global auth and an active, configured pool.
func probeWithActivePool(t *testing.T) (http.Handler, *loginTarget) {
	t.Helper()
	db := newTestDB(t)
	mux := web.NewServer(db)
	lt := newLoginTarget(t)
	if err := store.SaveGlobalAuth(db, model.AuthConfig{Type: model.AuthTypeBearer, Token: "global-tok"}); err != nil {
		t.Fatal(err)
	}
	id := importPool(t, db, mux, poolCSV())
	configurePool(t, mux, id, lt, nil)
	postForm(t, mux, "/pools/active", url.Values{"pool_id": {id}})
	return mux, lt
}

func TestRunsHandler_AuthProbeGlobalSourceIgnoresActivePool(t *testing.T) {
	// Arrange
	mux, lt := probeWithActivePool(t)

	// Act
	rr := postForm(t, mux, "/runs", probeForm(lt, url.Values{"auth_source": {"global"}}))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("POST /runs = %d: %s", rr.Code, rr.Body.String())
	}
	pollDone(t, mux, runIDFrom(t, rr.Body.String()))

	// Assert
	if !strings.Contains(rr.Body.String(), "global auth") {
		t.Errorf("running detail should name the identity (global auth):\n%s", rr.Body.String())
	}
	headers := lt.headers()
	if headers["Bearer global-tok"] != 4 || len(headers) != 1 {
		t.Errorf("target saw %v; want only the global token, 4 times", headers)
	}
}

func TestRunsHandler_AuthProbeNoneSendsNoAuth(t *testing.T) {
	// Arrange
	mux, lt := probeWithActivePool(t)

	// Act
	rr := postForm(t, mux, "/runs", probeForm(lt, url.Values{"auth_source": {"none"}}))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("POST /runs = %d: %s", rr.Code, rr.Body.String())
	}
	pollDone(t, mux, runIDFrom(t, rr.Body.String()))

	// Assert
	if !strings.Contains(rr.Body.String(), "no auth") {
		t.Errorf("running detail should say no auth:\n%s", rr.Body.String())
	}
	if headers := lt.headers(); headers[""] != 4 || len(headers) != 1 {
		t.Errorf("target saw %v; want 4 attempts without an Authorization header", headers)
	}
}

func TestRunsHandler_AuthProbePoolSourceWithoutPool_Returns400(t *testing.T) {
	// Arrange
	db := newTestDB(t)
	mux := web.NewServer(db)
	lt := newLoginTarget(t)

	// Act
	rr := postForm(t, mux, "/runs", probeForm(lt, url.Values{"auth_source": {"pool"}}))

	// Assert
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "credential pool") {
		t.Errorf("status %d, want 400 mentioning credential pool: %s", rr.Code, rr.Body.String())
	}
	if len(lt.headers()) != 0 {
		t.Errorf("target was hit %v; nothing may be dispatched", lt.headers())
	}
}

func TestRunsHandler_AuthProbeFieldValidation(t *testing.T) {
	cases := []struct {
		name  string
		extra url.Values
		want  string
	}{
		{"junk max_rate", url.Values{"max_rate": {"fast"}}, "max_rate"},
		{"negative max_rate", url.Values{"max_rate": {"-2"}}, "max_rate"},
		{"unknown auth_source", url.Values{"auth_source": {"admin"}}, "auth_source"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := newTestDB(t)
			mux := web.NewServer(db)
			lt := newLoginTarget(t)

			rr := postForm(t, mux, "/runs", probeForm(lt, tc.extra))

			if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), tc.want) {
				t.Errorf("status %d, want 400 mentioning %q: %s", rr.Code, tc.want, rr.Body.String())
			}
		})
	}
}

func TestRunsHandler_AuthProbeMaxRateIsSaved(t *testing.T) {
	// Arrange
	db := newTestDB(t)
	mux := web.NewServer(db)
	lt := newLoginTarget(t)

	// Act
	runID := postRuns(t, mux, probeForm(lt, url.Values{"max_rate": {"50"}}))
	pollDone(t, mux, runID)

	// Assert
	run, err := store.GetRun(db, runID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	cfg, err := store.GetTestConfig(db, run.TestConfigID)
	if err != nil {
		t.Fatalf("GetTestConfig: %v", err)
	}
	if cfg.MaxRate != 50 {
		t.Errorf("saved max_rate = %g, want 50", cfg.MaxRate)
	}
}

func TestRunsHandler_MaxRateOutsideAuthProbe_Returns400(t *testing.T) {
	// Arrange
	db := newTestDB(t)
	mux := web.NewServer(db)
	lt := newLoginTarget(t)
	form := probeForm(lt, url.Values{"max_rate": {"5"}})
	form.Set("mode", "burst")
	form.Set("burst_count", "4")

	// Act
	rr := postForm(t, mux, "/runs", form)

	// Assert
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "max_rate") {
		t.Errorf("status %d, want 400 mentioning max_rate: %s", rr.Code, rr.Body.String())
	}
}

func TestRunsHandler_AuthProbePoolSourceUsesActivePool(t *testing.T) {
	// Arrange
	mux, lt := probeWithActivePool(t)

	// Act
	rr := postForm(t, mux, "/runs", probeForm(lt, url.Values{"auth_source": {"pool"}}))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("POST /runs = %d: %s", rr.Code, rr.Body.String())
	}
	pollDone(t, mux, runIDFrom(t, rr.Body.String()))

	// Assert
	if !strings.Contains(rr.Body.String(), "logging in as pool shop users") {
		t.Errorf("running detail should name the pool:\n%s", rr.Body.String())
	}
	headers := lt.headers()
	if headers["Bearer tok-alice-secret"] == 0 || headers["Bearer tok-bob-secret"] == 0 || headers["Bearer global-tok"] != 0 {
		t.Errorf("target saw %v; want both pool tokens and never the global token", headers)
	}
}

func TestRunsHandler_IdentityLabelFollowsResolvedAuth(t *testing.T) {
	cases := []struct {
		name   string
		global *model.AuthConfig
		source string
		want   string
	}{
		{"default source uses global auth", &model.AuthConfig{Type: model.AuthTypeBearer, Token: "global-tok"}, "", "global auth"},
		{"global source with no global auth set", nil, "global", "no auth"},
		{"global source with auth type none", &model.AuthConfig{Type: model.AuthTypeNone}, "global", "no auth"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := newTestDB(t)
			mux := web.NewServer(db)
			lt := newLoginTarget(t)
			if tc.global != nil {
				if err := store.SaveGlobalAuth(db, *tc.global); err != nil {
					t.Fatal(err)
				}
			}

			rr := postForm(t, mux, "/runs", probeForm(lt, url.Values{"auth_source": {tc.source}}))
			if rr.Code != http.StatusAccepted {
				t.Fatalf("POST /runs = %d: %s", rr.Code, rr.Body.String())
			}
			pollDone(t, mux, runIDFrom(t, rr.Body.String()))

			if body := rr.Body.String(); !strings.Contains(body, "· "+tc.want) {
				t.Errorf("running detail should say %q:\n%s", tc.want, body)
			}
		})
	}
}

func TestRunStatus_KeepsIdentityWhileRunning(t *testing.T) {
	// Arrange: a target slow enough that the first poll sees the run in flight.
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(release); srv.Close() })
	db := newTestDB(t)
	mux := web.NewServer(db)
	form := url.Values{
		"method": {"GET"}, "url": {srv.URL}, "mode": {"auth_probe"},
		"concurrency": {"1"}, "total_requests": {"1"}, "auth_source": {"none"},
	}

	// Act
	runID := postRuns(t, mux, form)
	body := getPath(t, mux, "/runs/"+runID+"/status").Body.String()
	postForm(t, mux, "/runs/"+runID+"/stop", nil)

	// Assert
	if !strings.Contains(body, "no auth") {
		t.Errorf("status poll while running should still name the identity:\n%s", body)
	}
}

func TestRequestPanel_HasAuthProbeFields(t *testing.T) {
	// Arrange
	db := newTestDB(t)
	mux := web.NewServer(db)

	// Act
	body := getPath(t, mux, "/requests/new").Body.String()

	// Assert
	for _, want := range []string{
		`id="fields-probe"`,
		`name="max_rate"`,
		`name="auth_source"`,
		`<option value="none"`,
		`<option value="global"`,
		`<option value="pool"`,
		`id="count-label"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("request panel missing %s", want)
		}
	}
	for _, re := range []string{
		`<input[^>]*name="max_rate"[^>]*disabled`,
		`<select[^>]*name="auth_source"[^>]*disabled`,
	} {
		if !regexp.MustCompile(re).MatchString(body) {
			t.Errorf("panel should match %s so other modes do not submit the field", re)
		}
	}
}

// runIDFrom pulls data-run-id out of the running fragment.
func runIDFrom(t *testing.T, body string) string {
	t.Helper()
	const marker = `data-run-id="`
	i := strings.Index(body, marker)
	if i < 0 {
		t.Fatalf("running fragment missing data-run-id: %s", body)
	}
	rest := body[i+len(marker):]
	return rest[:strings.Index(rest, `"`)]
}
