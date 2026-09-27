package web_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Horac3/Primus/internal/model"
	"github.com/Horac3/Primus/internal/web"
)

// ---- helpers ---------------------------------------------------------------

func cannedRun(id string) model.Run {
	now := time.Now().UTC()
	return model.Run{
		ID:           id,
		TestConfigID: "cfg-x",
		StartedAt:    now,
		FinishedAt:   now,
		Summary:      model.RunSummary{StatusCounts: map[int]int{200: 5}},
		Outcome:      model.OutcomePass,
		Passed:       true,
	}
}

// ============================================================
// Start → running → done → gone (state transition)
// ============================================================

func TestRegistry_PollTransitions(t *testing.T) {
	// Arrange — fn blocks until the test releases it
	release := make(chan struct{})
	var persisted atomic.Int64
	reg := web.NewRegistry(func(r model.Run) error {
		persisted.Add(1)
		return nil
	})
	reg.Start(context.Background(), "run-1", func(ctx context.Context) (model.Run, error) {
		<-release
		return cannedRun("run-1"), nil
	})

	// Act — poll while running
	status, result, ok := reg.Poll("run-1")

	// Assert — running, no result yet
	if !ok || status != "running" || result != nil {
		t.Fatalf("poll = (%q, %v, %v), want (running, nil, true)", status, result, ok)
	}

	// Act — release, wait, poll the finished run
	close(release)
	var doneRun *model.Run
	for i := 0; i < 100; i++ {
		time.Sleep(20 * time.Millisecond)
		if s, r, ok := reg.Poll("run-1"); ok && s == "done" {
			doneRun = r
			break
		}
	}

	// Assert — done with the result, persisted exactly once
	if doneRun == nil || doneRun.ID != "run-1" {
		t.Fatalf("finished poll = %+v, want run-1", doneRun)
	}
	if persisted.Load() != 1 {
		t.Errorf("persist calls = %d, want 1", persisted.Load())
	}

	// Act — poll again after the result was delivered
	_, _, ok = reg.Poll("run-1")

	// Assert — entry removed after its result was polled once
	if ok {
		t.Error("poll after delivery should report unknown (entry removed)")
	}
}

func TestRegistry_PollUnknown_ReturnsNotOK(t *testing.T) {
	// Arrange
	reg := web.NewRegistry(func(r model.Run) error { return nil })

	// Act
	_, _, ok := reg.Poll("nope")

	// Assert
	if ok {
		t.Error("Poll(unknown) = true, want false")
	}
}

// ============================================================
// Stop cancels mid-run; stored result is partial, persisted once
// ============================================================

func TestRegistry_Stop_StopsPromptlyWithPartialResult(t *testing.T) {
	// Arrange — fn simulates a long run, honouring ctx cancellation
	var persisted atomic.Int64
	var saved atomic.Value
	reg := web.NewRegistry(func(r model.Run) error {
		persisted.Add(1)
		saved.Store(r)
		return nil
	})
	reg.Start(context.Background(), "run-long", func(ctx context.Context) (model.Run, error) {
		sent := 0
		for {
			select {
			case <-ctx.Done():
				run := cannedRun("run-long")
				run.Summary = model.RunSummary{StatusCounts: map[int]int{200: sent}}
				return run, nil
			case <-time.After(50 * time.Millisecond):
				sent++
				if sent >= 1000 {
					return cannedRun("run-long"), nil
				}
			}
		}
	})

	// Act — stop after a short delay
	time.Sleep(150 * time.Millisecond)
	if !reg.Stop("run-long") {
		t.Fatal("Stop = false, want true for a running run")
	}
	var doneRun *model.Run
	for i := 0; i < 100; i++ {
		time.Sleep(20 * time.Millisecond)
		if s, r, ok := reg.Poll("run-long"); ok && s == "done" {
			doneRun = r
			break
		}
	}

	// Assert — stopped promptly with a partial (not full) result
	if doneRun == nil {
		t.Fatal("run never finished after Stop")
	}
	if got := doneRun.Summary.StatusCounts[200]; got >= 1000 {
		t.Errorf("recorded = %d, want partial (< 1000)", got)
	}
	if persisted.Load() != 1 {
		t.Errorf("persist calls = %d, want 1", persisted.Load())
	}
}

// ============================================================
// Cancel racing natural completion: no panic, exactly one persist
// ============================================================

func TestRegistry_CancelAtCompletion_PersistsExactlyOnce(t *testing.T) {
	// Arrange — fn finishes after ~200ms on its own
	var persisted atomic.Int64
	reg := web.NewRegistry(func(r model.Run) error {
		persisted.Add(1)
		return nil
	})
	reg.Start(context.Background(), "run-race", func(ctx context.Context) (model.Run, error) {
		select {
		case <-ctx.Done():
			return cannedRun("run-race"), nil
		case <-time.After(200 * time.Millisecond):
			return cannedRun("run-race"), nil
		}
	})

	// Act — fire Stop at approximately natural completion
	time.Sleep(200 * time.Millisecond)
	reg.Stop("run-race") // result ignored: may or may not still be running
	for i := 0; i < 100; i++ {
		time.Sleep(20 * time.Millisecond)
		if s, _, ok := reg.Poll("run-race"); ok && s == "done" {
			break
		}
	}
	reg.Poll("run-race") // drain delivery so the entry is removed

	// Assert — exactly one final state, no panic, no double-write
	if persisted.Load() != 1 {
		t.Errorf("persist calls = %d, want exactly 1", persisted.Load())
	}
}
