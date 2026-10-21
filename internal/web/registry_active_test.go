package web

import (
	"context"
	"testing"
	"time"

	"github.com/Horac3/Primus/internal/model"
)

func TestRegistry_ActiveRequestIDsReportsRunningEntriesOnly(t *testing.T) {
	reg := NewRegistry(func(model.Run) error { return nil })
	release := make(chan struct{})
	h := &runHandle{done: make(chan struct{}), requestID: "ep1"}
	reg.startWithHandle(context.Background(), "run-1", h, func(ctx context.Context, h *runHandle) (model.Run, error) {
		<-release
		return model.Run{ID: "run-1"}, nil
	})

	if !reg.ActiveRequestIDs()["ep1"] {
		t.Fatal("running run's endpoint not reported active")
	}
	if reg.ActiveRequestIDs()["other"] {
		t.Error("unrelated endpoint reported active")
	}

	close(release)
	select {
	case <-h.done:
	case <-time.After(2 * time.Second):
		t.Fatal("run did not finish")
	}
	if reg.ActiveRequestIDs()["ep1"] {
		t.Error("finished run still reported active (finished-but-unpolled results are not active)")
	}
}

func TestRegistry_ActiveRequestIDsIncludesStoppingRun(t *testing.T) {
	// A run that has been asked to stop is active until its goroutine finishes and persists.
	reg := NewRegistry(func(model.Run) error { return nil })
	started := make(chan struct{})
	h := &runHandle{done: make(chan struct{}), requestID: "ep2"}
	reg.startWithHandle(context.Background(), "run-2", h, func(ctx context.Context, h *runHandle) (model.Run, error) {
		close(started)
		<-ctx.Done()
		time.Sleep(100 * time.Millisecond) // still winding down after cancel
		return model.Run{ID: "run-2"}, nil
	})
	<-started

	reg.Stop("run-2")

	if !reg.ActiveRequestIDs()["ep2"] {
		t.Error("run being stopped must stay active until it has persisted")
	}
	<-h.done
}
