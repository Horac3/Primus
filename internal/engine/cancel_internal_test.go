package engine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Horac3/Primus/internal/model"
)

// A request's own deadline expiring surfaces as a context error too, but the
// run is still live: that is a target timeout, so it must count as an error.
// The engine has no per-request timeout setting, so this drives the worker
// and the aggregator directly with a client that has one.
func TestRunWorkers_RequestTimeoutWhileRunLive_IsErrorNotCancelled(t *testing.T) {
	// Arrange
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer srv.Close()
	defer close(release)
	client := &http.Client{Timeout: 50 * time.Millisecond}
	jobs := make(chan struct{}, 1)
	jobs <- struct{}{}
	close(jobs)
	results := make(chan result, 1)
	req := model.RequestDefinition{Method: http.MethodPost, URLTemplate: srv.URL}

	// Act
	runWorkers(context.Background(), req, []workerAuth{{}}, nil, jobs, results, client)
	summary, _, _ := aggregate(results, model.Assertions{}, time.Now(), nil)

	// Assert
	if summary.ErrorCount != 1 || summary.TotalRequests != 1 {
		t.Errorf("errors = %d of %d, want 1 of 1: a request timeout is the target's fault",
			summary.ErrorCount, summary.TotalRequests)
	}
	if summary.Cancelled != 0 {
		t.Errorf("Cancelled = %d, want 0: the run was never cancelled", summary.Cancelled)
	}
}
