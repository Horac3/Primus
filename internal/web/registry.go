package web

import (
	"context"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Horac3/Primus/internal/model"
)

// handle tracks one in-flight job: its cancellation func and, once
// finished, its result. finish is guarded by once so a Stop racing natural
// completion persists exactly one final state.
type handle[T any] struct {
	cancel     context.CancelFunc
	done       chan struct{}
	result     *T
	finishedAt time.Time
	once       sync.Once
	snapshot   atomic.Pointer[[]model.Point]
	// detail is set before the run starts and only read afterwards.
	detail string
}

// registry tracks in-flight jobs for async execution + polling.
// Finished runs live in SQLite (via persist); the registry only holds
// running ones plus results not yet polled.
type registry[T any] struct {
	mu      sync.Mutex
	runs    map[string]*handle[T]
	persist func(T) error

	// TTL bounds how long a finished-but-unpolled result stays available.
	TTL time.Duration
}

// Registry tracks in-flight test runs; runHandle is one of its entries.
type (
	Registry  = registry[model.Run]
	runHandle = handle[model.Run]
)

// NewRegistry builds a Registry that persists finished runs via persist.
func NewRegistry(persist func(model.Run) error) *Registry {
	return newRegistry(persist)
}

// newRegistry builds a registry for any async job type T (test runs,
// provisioning), persisting each finished result via persist.
func newRegistry[T any](persist func(T) error) *registry[T] {
	return &registry[T]{
		runs:    make(map[string]*handle[T]),
		persist: persist,
		TTL:     5 * time.Minute,
	}
}

// Start launches fn in a goroutine under a cancellable context and returns
// immediately. The context is detached from parent cancellation (the HTTP
// request context dies when the 202 response is written — the run must
// outlive it); only Stop cancels it. Exactly one final state is persisted.
func (r *registry[T]) Start(parent context.Context, id string, fn func(ctx context.Context) (T, error)) {
	ctx, cancel := context.WithCancel(context.WithoutCancel(parent))
	h := &handle[T]{cancel: cancel, done: make(chan struct{})}

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

func (r *registry[T]) startWithHandle(parent context.Context, id string, h *handle[T], fn func(ctx context.Context, h *handle[T]) (T, error)) {
	ctx, cancel := context.WithCancel(context.WithoutCancel(parent))
	h.cancel = cancel

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
		run, err := fn(ctx, h)
		r.finish(id, h, run, err)
	}()
}

func (r *registry[T]) finish(id string, h *handle[T], run T, err error) {
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
func (r *registry[T]) Poll(id string) (status string, result *T, ok bool) {
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

// Detail returns the running line for an in-flight run, or "".
func (r *registry[T]) Detail(id string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if h := r.runs[id]; h != nil {
		return h.detail
	}
	return ""
}

func (r *registry[T]) Snapshot(id string) []model.Point {
	r.mu.Lock()
	defer r.mu.Unlock()
	h, found := r.runs[id]
	if !found || h == nil {
		return nil
	}
	if s := h.snapshot.Load(); s != nil {
		clone := append([]model.Point(nil), (*s)...)
		return clone
	}
	return nil
}

// Stop cancels an in-flight run. It reports false for unknown or already
// finished ids. Cancellation racing natural completion is a harmless no-op:
// finish is once-guarded, so at most one result is ever persisted.
func (r *registry[T]) Stop(id string) bool {
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
