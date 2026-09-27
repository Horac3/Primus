package web

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/Horac3/Primus/internal/model"
)

// runHandle tracks one in-flight run: its cancellation func and, once
// finished, its result. finish is guarded by once so a Stop racing natural
// completion persists exactly one final state.
type runHandle struct {
	cancel     context.CancelFunc
	done       chan struct{}
	result     *model.Run
	finishedAt time.Time
	once       sync.Once
}

// Registry tracks in-flight runs for async execution + polling.
// Finished runs live in SQLite (via persist); the registry only holds
// running ones plus results not yet polled.
type Registry struct {
	mu      sync.Mutex
	runs    map[string]*runHandle
	persist func(model.Run) error

	// TTL bounds how long a finished-but-unpolled result stays available.
	TTL time.Duration
}

// NewRegistry builds a Registry that persists finished runs via persist.
func NewRegistry(persist func(model.Run) error) *Registry {
	return &Registry{
		runs:    make(map[string]*runHandle),
		persist: persist,
		TTL:     5 * time.Minute,
	}
}

// Start launches fn in a goroutine under a cancellable context and returns
// immediately. Exactly one final state is persisted per id.
func (r *Registry) Start(parent context.Context, id string, fn func(ctx context.Context) (model.Run, error)) {
	ctx, cancel := context.WithCancel(parent)
	h := &runHandle{cancel: cancel, done: make(chan struct{})}

	r.mu.Lock()
	r.runs[id] = h
	// Opportunistic sweep: drop finished results older than the TTL so the
	// registry can't grow unbounded across a long session.
	for otherID, other := range r.runs {
		if otherID != id && other.finishedAt.IsZero() {
			continue
		}
		select {
		case <-other.done:
			if time.Since(other.finishedAt) > r.TTL {
				delete(r.runs, otherID)
			}
		default:
		}
	}
	r.mu.Unlock()

	go func() {
		run, err := fn(ctx)
		r.finish(id, h, run, err)
	}()
}

func (r *Registry) finish(id string, h *runHandle, run model.Run, err error) {
	h.once.Do(func() {
		if err == nil {
			if perr := r.persist(run); perr != nil {
				log.Printf("registry: persist run %s: %v", id, perr)
			}
			h.result = &run
		}
		h.finishedAt = time.Now()
		close(h.done)
	})
}

// Poll reports a run's state: "running" (result nil) or "done" (result set).
// ok is false for unknown ids. A finished result is delivered once — the
// entry is removed, so a second poll reports unknown.
func (r *Registry) Poll(id string) (status string, result *model.Run, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	h, found := r.runs[id]
	if !found {
		return "", nil, false
	}
	select {
	case <-h.done:
		delete(r.runs, id)
		return "done", h.result, true
	default:
		return "running", nil, true
	}
}

// Stop cancels an in-flight run. It reports false for unknown or already
// finished ids. Cancellation racing natural completion is a harmless no-op:
// finish is once-guarded, so at most one result is ever persisted.
func (r *Registry) Stop(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	h, found := r.runs[id]
	if !found {
		return false
	}
	select {
	case <-h.done:
		return false
	default:
		h.cancel()
		return true
	}
}
