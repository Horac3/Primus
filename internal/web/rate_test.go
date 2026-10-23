package web_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Horac3/Primus/internal/model"
	"github.com/Horac3/Primus/internal/store"
	"github.com/Horac3/Primus/internal/web"
)

// ============================================================
// Rate mode: target_rate form field and result (FR-RATE-01..03)
// ============================================================

func rateTarget(t *testing.T, delay time.Duration) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(delay):
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func rateForm(srv *httptest.Server, rate, concurrency string) url.Values {
	return url.Values{
		"method": {"GET"}, "url": {srv.URL}, "mode": {"rate"},
		"concurrency": {concurrency}, "duration_seconds": {"1"}, "target_rate": {rate},
	}
}

func TestRunsHandler_RateModeIsSavedAndReported(t *testing.T) {
	// Arrange
	db := newTestDB(t)
	mux := web.NewServer(db)
	srv := rateTarget(t, 0)

	// Act
	runID := postRuns(t, mux, rateForm(srv, "20", "5"))
	body := pollDone(t, mux, runID)

	// Assert
	run, err := store.GetRun(db, runID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	cfg, err := store.GetTestConfig(db, run.TestConfigID)
	if err != nil {
		t.Fatalf("GetTestConfig: %v", err)
	}
	if cfg.Mode != model.ModeRate || cfg.TargetRate != 20 || cfg.Duration != time.Second {
		t.Errorf("saved config = mode %s, target %g, duration %s; want rate, 20, 1s", cfg.Mode, cfg.TargetRate, cfg.Duration)
	}
	if run.Summary.TargetRate != 20 {
		t.Errorf("saved summary target_rate = %g, want 20", run.Summary.TargetRate)
	}
	for _, want := range []string{"Target rate", "Achieved rate", "Dropped"} {
		if !strings.Contains(body, want) {
			t.Errorf("result should show %q:\n%s", want, body)
		}
	}
}

func TestRunsHandler_RateNotSustained_ShowsVerdict(t *testing.T) {
	// Arrange: one slot, 300ms responses, 50 due per second.
	db := newTestDB(t)
	mux := web.NewServer(db)
	srv := rateTarget(t, 300*time.Millisecond)

	// Act
	body := pollDone(t, mux, postRuns(t, mux, rateForm(srv, "50", "1")))

	// Assert
	if !strings.Contains(body, "target rate not sustained") {
		t.Errorf("result should say the target rate was not sustained:\n%s", body)
	}
}

func TestRunsHandler_RateFieldValidation(t *testing.T) {
	srv := rateTarget(t, 0)
	cases := []struct {
		name string
		form url.Values
	}{
		{"missing target", rateForm(srv, "", "5")},
		{"not a number", rateForm(srv, "fast", "5")},
		{"zero", rateForm(srv, "0", "5")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mux := web.NewServer(newTestDB(t))
			rr := postForm(t, mux, "/runs", tc.form)
			if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "target_rate") {
				t.Errorf("status %d, want 400 mentioning target_rate: %s", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestRequestPanel_HasRateFields(t *testing.T) {
	// Arrange
	mux := web.NewServer(newTestDB(t))

	// Act
	body := getPath(t, mux, "/requests/new").Body.String()

	// Assert
	for _, want := range []string{`<option value="rate"`, `id="fields-rate"`, `name="target_rate"`} {
		if !strings.Contains(body, want) {
			t.Errorf("request panel missing %s", want)
		}
	}
	if !regexp.MustCompile(`<input[^>]*name="target_rate"[^>]*disabled`).MatchString(body) {
		t.Error("target_rate should start disabled so other modes do not submit it")
	}
}

func TestRunPage_ShowsRateFields(t *testing.T) {
	// Arrange
	db := newTestDB(t)
	mux := web.NewServer(db)
	runID := postRuns(t, mux, rateForm(rateTarget(t, 0), "20", "5"))
	pollDone(t, mux, runID)

	// Act
	body := getPath(t, mux, "/runs/"+runID).Body.String()

	// Assert
	for _, want := range []string{"Target rate", "20/s", "Achieved rate", "Dropped"} {
		if !strings.Contains(body, want) {
			t.Errorf("run page should show %q", want)
		}
	}
}
