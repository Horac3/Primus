package web

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"sync"
	"time"

	"github.com/Horac3/Primus/internal/store"
)

// Item states in a run-all sequence.
const (
	seqQueued  = "queued"
	seqRunning = "running"
	seqDone    = "done"
	seqSkipped = "skipped"
	seqStopped = "stopped"
)

// seqTTL bounds how long a finished sequence stays viewable.
const seqTTL = 5 * time.Minute

type seqItem struct {
	EndpointID string
	Name       string
	Status     string
	Reason     string // why an item was skipped
	RunID      string
}

// sequence runs a collection's endpoints one after another (FR-COLL-07). One goroutine owns it: it
// starts each run through the registry, waits for it, and exits after the last item or after Stop.
type sequence struct {
	id             string
	collectionID   string
	collectionName string
	cancel         context.CancelFunc // set once, before the goroutine starts

	mu         sync.Mutex // guards items, finished, finishedAt
	items      []seqItem
	finished   bool
	finishedAt time.Time
}

// sequenceView is a copy of a sequence's state, safe to render.
type sequenceView struct {
	ID, CollectionID, CollectionName string
	Items                            []seqItem
	Finished                         bool
}

func (s *sequence) view() sequenceView {
	s.mu.Lock()
	defer s.mu.Unlock()
	return sequenceView{ID: s.id, CollectionID: s.collectionID, CollectionName: s.collectionName,
		Items: append([]seqItem(nil), s.items...), Finished: s.finished}
}

func (s *sequence) set(i int, f func(*seqItem)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(&s.items[i])
}

// stopRemaining marks every item from i on that has not started as stopped.
func (s *sequence) stopRemaining(i int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for ; i < len(s.items); i++ {
		if s.items[i].Status == seqQueued {
			s.items[i].Status = seqStopped
		}
	}
}

func (s *sequence) finish() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finished, s.finishedAt = true, time.Now()
}

// run executes the items in order. Each iteration takes guard.mu while it re-reads the endpoint and
// registers the run, like postRunHandler, so a delete of the collection cannot land in between.
func (s *sequence) run(ctx context.Context, db *sql.DB, reg *Registry, guard *scopeGuard) {
	defer s.finish()
	items := s.view().Items
	for i, item := range items {
		if ctx.Err() != nil {
			s.stopRemaining(i)
			return
		}
		started, reason := s.start(ctx, db, reg, guard, item.EndpointID)
		if reason != "" {
			s.set(i, func(it *seqItem) { it.Status, it.Reason = seqSkipped, reason })
			continue
		}
		s.set(i, func(it *seqItem) { it.Status, it.RunID = seqRunning, started.ID })
		select {
		case <-started.Handle.done:
		case <-ctx.Done():
			reg.Stop(started.ID)
			<-started.Handle.done // the run persists before done closes
		}
		status := seqDone
		if ctx.Err() != nil {
			status = seqStopped
		}
		s.set(i, func(it *seqItem) { it.Status = status })
	}
}

// start registers the run for one endpoint with its newest saved config. A non-empty reason means
// the endpoint was skipped.
func (s *sequence) start(ctx context.Context, db *sql.DB, reg *Registry, guard *scopeGuard, endpointID string) (startedRun, string) {
	guard.mu.Lock()
	defer guard.mu.Unlock()
	def, err := store.GetRequestDefinition(db, endpointID)
	if errors.Is(err, sql.ErrNoRows) {
		return startedRun{}, "endpoint no longer exists"
	}
	if err != nil {
		return startedRun{}, "could not load the endpoint"
	}
	cfg, err := store.LatestTestConfig(db, endpointID)
	if errors.Is(err, store.ErrNoSuchConfig) {
		return startedRun{}, "no saved test config"
	}
	if err != nil {
		return startedRun{}, "could not load the test config"
	}
	resolved, err := store.ResolveURL(db, def)
	if err != nil {
		return startedRun{}, err.Error()
	}
	started, err := startConfiguredRun(ctx, db, reg, def, resolved, cfg, false)
	if err != nil {
		return startedRun{}, err.Error()
	}
	return started, ""
}

// sequenceStore holds run-all sequences by ID.
type sequenceStore struct {
	mu   sync.Mutex
	byID map[string]*sequence
}

func newSequenceStore() *sequenceStore { return &sequenceStore{byID: map[string]*sequence{}} }

func (st *sequenceStore) get(id string) *sequence {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.byID[id]
}

// startOrJoin returns the unfinished sequence of a collection if there is one, else registers a
// new sequence for items; created reports which. Finished sequences older than seqTTL are dropped.
func (st *sequenceStore) startOrJoin(collectionID, collectionName string, items []seqItem, cancel context.CancelFunc) (s *sequence, created bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	for id, other := range st.byID {
		other.mu.Lock()
		fin, at := other.finished, other.finishedAt
		other.mu.Unlock()
		switch {
		case !fin && other.collectionID == collectionID:
			return other, false
		case fin && time.Since(at) > seqTTL:
			delete(st.byID, id)
		}
	}
	s = &sequence{id: fmt.Sprintf("seq-%d", time.Now().UnixNano()), collectionID: collectionID,
		collectionName: collectionName, items: items, cancel: cancel}
	st.byID[s.id] = s
	return s, true
}

func runAllHandler(db *sql.DB, tmpl *template.Template, reg *Registry, guard *scopeGuard, seqs *sequenceStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		col, err := store.GetCollection(db, r.PathValue("id"))
		if errors.Is(err, store.ErrNoSuchCollection) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defs, err := store.ListRequestDefinitionsByCollection(db, col.ID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if len(defs) == 0 {
			http.Error(w, "this collection has no endpoints to run", http.StatusBadRequest)
			return
		}
		items := make([]seqItem, len(defs))
		for i, d := range defs {
			items[i] = seqItem{EndpointID: d.ID, Name: d.Name, Status: seqQueued}
		}
		// The request context dies when this handler returns; the sequence outlives it. Only Stop ends it.
		ctx, cancel := context.WithCancel(context.WithoutCancel(r.Context()))
		s, created := seqs.startOrJoin(col.ID, col.Name, items, cancel)
		if created {
			go s.run(ctx, db, reg, guard)
		} else {
			cancel()
		}
		w.WriteHeader(http.StatusAccepted)
		renderTemplate(w, tmpl, "sequence.html", s.view())
	}
}

func sequenceHandler(tmpl *template.Template, seqs *sequenceStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s := seqs.get(r.PathValue("id"))
		if s == nil {
			http.NotFound(w, r)
			return
		}
		renderTemplate(w, tmpl, "sequence.html", s.view())
	}
}

func sequenceStopHandler(tmpl *template.Template, seqs *sequenceStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s := seqs.get(r.PathValue("id"))
		if s == nil {
			http.NotFound(w, r)
			return
		}
		s.cancel()
		renderTemplate(w, tmpl, "sequence.html", s.view())
	}
}
