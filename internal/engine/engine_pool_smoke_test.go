package engine_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Horac3/Primus/internal/engine"
	"github.com/Horac3/Primus/internal/model"
)

// The smoke tests run 50 workers against a fixture with 50 pre-existing
// users. /me holds each request briefly and records the most concurrent
// requests seen on one token. Workers are closed-loop (one request in
// flight each), so if every token has exactly one owner that maximum is 1;
// any worker sending another worker's token pushes it to 2 or more.

const smokeWorkers = 50

func TestSmoke_FiftyWorkersHaveDistinctIdentities(t *testing.T) {
	// Arrange
	f := newAuthFixture(t, smokeWorkers)
	f.meDelay = 5 * time.Millisecond
	pool := f.pool(smokeWorkers)

	// Act
	run, err := engine.RunWithPool(context.Background(), f.meRequest(), countCfg(smokeWorkers, 2000), model.AuthConfig{}, &pool, nil)

	// Assert
	if err != nil {
		t.Fatalf("RunWithPool: %v", err)
	}
	if run.Login.Succeeded != smokeWorkers || run.Summary.StatusCounts[200] != 2000 {
		t.Fatalf("logins = %d, 200s = %d; want %d logins and 2000 x 200", run.Login.Succeeded, run.Summary.StatusCounts[200], smokeWorkers)
	}
	seen := f.snapshotSeen()
	for i := 0; i < smokeWorkers; i++ {
		if user := fmt.Sprintf("user%02d", i); seen[user] == 0 {
			t.Errorf("%s was never seen at the protected endpoint", user)
		}
	}
	if got := f.maxConcurrentPerToken(); got != 1 {
		t.Errorf("max concurrent requests on one token = %d, want 1 (a token was used by more than one worker)", got)
	}
}

func TestSmoke_SharingDetectorCatchesReuse(t *testing.T) {
	// Arrange: control case. 25 accounts shared by 50 workers must show
	// up as concurrent use of one token, proving the detector above works.
	f := newAuthFixture(t, smokeWorkers/2)
	f.meDelay = 5 * time.Millisecond
	pool := f.pool(smokeWorkers / 2)
	pool.AllowReuse = true

	// Act
	_, err := engine.RunWithPool(context.Background(), f.meRequest(), countCfg(smokeWorkers, 2000), model.AuthConfig{}, &pool, nil)

	// Assert
	if err != nil {
		t.Fatalf("RunWithPool: %v", err)
	}
	if got := f.maxConcurrentPerToken(); got < 2 {
		t.Errorf("max concurrent requests on one token = %d, want >= 2 when accounts are shared", got)
	}
}

func TestSmoke_HalfThePoolHasWrongPasswords(t *testing.T) {
	// Arrange: odd-numbered users have wrong passwords.
	f := newAuthFixture(t, smokeWorkers)
	f.meDelay = 5 * time.Millisecond
	var wrong []int
	for i := 1; i < smokeWorkers; i += 2 {
		wrong = append(wrong, i)
	}
	pool := f.pool(smokeWorkers, wrong...)

	// Act
	run, err := engine.RunWithPool(context.Background(), f.meRequest(), countCfg(smokeWorkers, 1000), model.AuthConfig{}, &pool, nil)

	// Assert
	if err != nil {
		t.Fatalf("RunWithPool: %v", err)
	}
	if run.Login.Succeeded != 25 || run.Login.Failed != 25 || !hasReason(run.Login, "HTTP 401") {
		t.Errorf("login = %d ok / %d failed (%v), want 25 / 25 with HTTP 401", run.Login.Succeeded, run.Login.Failed, run.Login.FailureReasons)
	}
	if run.Summary.TotalRequests != 1000 || run.Summary.StatusCounts[200] != 1000 {
		t.Errorf("summary = %+v, want all 1000 requests sent by the 25 logged-in workers and answered 200", run.Summary)
	}
	seen := f.snapshotSeen()
	for i := 0; i < smokeWorkers; i++ {
		user := fmt.Sprintf("user%02d", i)
		if wantSeen := i%2 == 0; (seen[user] > 0) != wantSeen {
			t.Errorf("%s seen %d times, want seen=%v", user, seen[user], wantSeen)
		}
	}
	if got := f.maxConcurrentPerToken(); got != 1 {
		t.Errorf("max concurrent requests on one token = %d, want 1", got)
	}
}
