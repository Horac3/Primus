package engine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Horac3/Primus/internal/model"
)

// A rate-mode job carries its intended send time. Its latency runs from
// that time, not from when the worker fired, so a late release shows up
// as latency instead of hiding as coordinated omission (FR-RATE-03).
func TestRunScheduledWorkers_LatencyRunsFromIntendedSendTime(t *testing.T) {
	// Arrange: a fast target and a job that was due 200ms ago.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	jobs := make(chan time.Time, 1)
	jobs <- time.Now().Add(-200 * time.Millisecond)
	close(jobs)
	slots := make(chan struct{}, 1)
	slots <- struct{}{}
	results := make(chan result, 1)
	req := model.RequestDefinition{Method: http.MethodPost, URLTemplate: srv.URL}

	// Act
	runScheduledWorkers(context.Background(), req, []workerAuth{{}}, nil, jobs, slots, results, srv.Client())
	r := <-results

	// Assert
	if r.err != nil {
		t.Fatalf("request failed: %v", r.err)
	}
	if r.latency < 200*time.Millisecond {
		t.Errorf("latency = %s, want >= 200ms (measured from the intended send time)", r.latency)
	}
}
