package web_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Horac3/Primus/internal/model"
	"github.com/Horac3/Primus/internal/store"
	"github.com/Horac3/Primus/internal/web"
)

func getExport(t *testing.T, mux http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
	return rr
}

func TestSeriesCSV(t *testing.T) {
	// Arrange
	db := newTestDB(t)
	if err := store.SaveRun(db, model.Run{
		ID: "run-csv",
		TimeSeries: []model.Point{
			{ElapsedMs: 1000, AvgLatencyMs: 12.5, RPS: 80, ErrorRate: 0},
			{ElapsedMs: 2000, AvgLatencyMs: 20, RPS: 75.5, ErrorRate: 0.02},
		},
	}); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}
	if err := store.SaveRun(db, model.Run{ID: "run-empty"}); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}
	mux := web.NewServer(db)

	t.Run("rows follow the header", func(t *testing.T) {
		// Act
		rr := getExport(t, mux, "/runs/run-csv/series.csv")

		// Assert
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body.String())
		}
		if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
			t.Errorf("Content-Type = %q, want text/csv", ct)
		}
		if cd := rr.Header().Get("Content-Disposition"); cd != `attachment; filename="primus-run-run-csv.csv"` {
			t.Errorf("Content-Disposition = %q", cd)
		}
		want := "elapsed_ms,avg_latency_ms,rps,error_rate\n1000,12.5,80,0\n2000,20,75.5,0.02\n"
		if got := strings.ReplaceAll(rr.Body.String(), "\r\n", "\n"); got != want {
			t.Errorf("body = %q, want %q", got, want)
		}
	})

	t.Run("empty series is header only", func(t *testing.T) {
		rr := getExport(t, mux, "/runs/run-empty/series.csv")
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rr.Code)
		}
		if got := strings.TrimSpace(rr.Body.String()); got != "elapsed_ms,avg_latency_ms,rps,error_rate" {
			t.Errorf("body = %q, want header only", got)
		}
	})

	t.Run("unknown run is 404", func(t *testing.T) {
		if rr := getExport(t, mux, "/runs/nope/series.csv"); rr.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", rr.Code)
		}
	})
}
