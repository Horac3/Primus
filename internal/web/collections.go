package web

import (
	"database/sql"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"sync"

	"github.com/Horac3/Primus/internal/model"
	"github.com/Horac3/Primus/internal/seed"
	"github.com/Horac3/Primus/internal/store"
)

// scopeGuard makes "start a run" and "delete or clear its scope" mutually exclusive. The registry
// knows which endpoints have runs in flight, the store does not, so a delete first checks the
// registry and then deletes; without the guard a run could start between the two steps.
type scopeGuard struct{ mu sync.Mutex }

type collectionRow struct {
	model.Collection
	Counts store.Counts
}

type confirmField struct{ Name, Value string }

// confirmView is a two-step prompt: the first POST shows it, Confirm re-posts with confirm=1.
type confirmView struct {
	Prompt string
	Action string
	Target string // htmx target for the confirmed response
	Fields []confirmField
}

type collectionsView struct {
	Rows    []collectionRow
	Message string
	Error   string
	Confirm *confirmView
}

func renderCollectionsPanel(w http.ResponseWriter, db *sql.DB, tmpl *template.Template, view collectionsView) {
	cols, err := store.ListCollections(db)
	if err != nil {
		http.Error(w, "list collections: "+err.Error(), http.StatusInternalServerError)
		return
	}
	for _, c := range cols {
		counts, err := store.CountScope(db, store.Scope{Kind: store.ScopeCollection, ID: c.ID})
		if err != nil {
			http.Error(w, "count collection: "+err.Error(), http.StatusInternalServerError)
			return
		}
		view.Rows = append(view.Rows, collectionRow{Collection: c, Counts: counts})
	}
	renderTemplate(w, tmpl, "collections-panel.html", view)
}

func collectionsPanelHandler(db *sql.DB, tmpl *template.Template) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		renderCollectionsPanel(w, db, tmpl, collectionsView{})
	}
}

// formError reports whether err is a user-fixable input problem to show beside the form.
func formError(err error) bool {
	return errors.Is(err, store.ErrEmptyName) || errors.Is(err, store.ErrDuplicateName) ||
		errors.Is(err, store.ErrInvalidBaseURL) || errors.Is(err, store.ErrNoSuchCollection)
}

func createCollectionHandler(db *sql.DB, tmpl *template.Template) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := store.CreateCollection(db, r.FormValue("name"), r.FormValue("base_url"))
		if err != nil {
			respondCollectionsError(w, db, tmpl, err)
			return
		}
		w.Header().Set("HX-Trigger", "collections-changed")
		renderCollectionsPanel(w, db, tmpl, collectionsView{Message: fmt.Sprintf("Created %s.", c.Name)})
	}
}

func updateCollectionHandler(db *sql.DB, tmpl *template.Template) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		err := store.UpdateCollection(db, r.PathValue("id"), r.FormValue("name"), r.FormValue("base_url"))
		if err != nil {
			respondCollectionsError(w, db, tmpl, err)
			return
		}
		w.Header().Set("HX-Trigger", "collections-changed")
		renderCollectionsPanel(w, db, tmpl, collectionsView{Message: "Saved."})
	}
}

// restoreExamplesHandler brings back the built-in collection the user deleted. It only inserts new rows,
// so it does not need the scope guard.
func restoreExamplesHandler(db *sql.DB, tmpl *template.Template) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		res, err := seed.Restore(db)
		if errors.Is(err, store.ErrDuplicateName) {
			renderCollectionsPanel(w, db, tmpl, collectionsView{
				Error: "A collection named JSONPlaceholder already exists. Rename it, then restore the examples."})
			return
		}
		if err != nil {
			respondCollectionsError(w, db, tmpl, err)
			return
		}
		if res == store.SeedAlreadyPresent {
			renderCollectionsPanel(w, db, tmpl, collectionsView{Message: "The examples are already present; nothing changed."})
			return
		}
		w.Header().Set("HX-Trigger", "collections-changed")
		renderCollectionsPanel(w, db, tmpl, collectionsView{Message: "JSONPlaceholder examples restored."})
	}
}

// respondCollectionsError shows input problems in the panel (htmx does not swap 4xx responses) and
// anything else as a 500.
func respondCollectionsError(w http.ResponseWriter, db *sql.DB, tmpl *template.Template, err error) {
	if !formError(err) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	renderCollectionsPanel(w, db, tmpl, collectionsView{Error: capitalize(err.Error())})
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// activeRunIn reports whether any run in flight belongs to an endpoint in scope.
func activeRunIn(db *sql.DB, reg *Registry, scope store.Scope) (bool, error) {
	ids, err := store.ScopeRequestIDs(db, scope)
	if err != nil {
		return false, err
	}
	active := reg.ActiveRequestIDs()
	for _, id := range ids {
		if active[id] {
			return true, nil
		}
	}
	return false, nil
}

func activeMessage(label string) string {
	return fmt.Sprintf("A run in %s is still active. Stop it first; nothing was changed.", label)
}

func deleteCollectionHandler(db *sql.DB, tmpl *template.Template, reg *Registry, guard *scopeGuard) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		guard.mu.Lock()
		unlock := sync.OnceFunc(guard.mu.Unlock)
		defer unlock()

		col, err := store.GetCollection(db, id)
		if err != nil {
			unlock()
			respondCollectionsError(w, db, tmpl, err)
			return
		}
		scope := store.Scope{Kind: store.ScopeCollection, ID: id}
		active, err := activeRunIn(db, reg, scope)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if active {
			unlock()
			renderCollectionsPanel(w, db, tmpl, collectionsView{Error: activeMessage(col.Name)})
			return
		}
		if r.FormValue("confirm") != "1" {
			counts, err := store.CountScope(db, scope)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			prompt := fmt.Sprintf("Delete %s: %d endpoints, %d runs? This cannot be undone.", col.Name, counts.Endpoints, counts.Runs)
			if col.BuiltIn() {
				prompt += " It is built in and stays deleted until you restore the examples."
			}
			unlock()
			renderCollectionsPanel(w, db, tmpl, collectionsView{Confirm: &confirmView{
				Prompt: prompt, Action: "/collections/" + id + "/delete", Target: "#collections-panel",
			}})
			return
		}
		counts, err := store.DeleteCollection(db, id)
		if err != nil {
			unlock()
			respondCollectionsError(w, db, tmpl, err)
			return
		}
		w.Header().Set("HX-Trigger", "collections-changed, history-changed")
		unlock()
		renderCollectionsPanel(w, db, tmpl, collectionsView{
			Message: fmt.Sprintf("Deleted %s (%d endpoints, %d runs).", col.Name, counts.Endpoints, counts.Runs),
		})
	}
}

func clearHistoryHandler(db *sql.DB, tmpl *template.Template, reg *Registry, guard *scopeGuard) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.FormValue("id"))
		scopeName := r.FormValue("scope")
		var scope store.Scope
		var label string
		switch scopeName {
		case "all":
			scope, label = store.Scope{Kind: store.ScopeAll}, "all endpoints"
		case "collection":
			scope, label = store.Scope{Kind: store.ScopeCollection, ID: id}, "this collection"
			if c, err := store.GetCollection(db, id); err == nil {
				label = c.Name
			}
		case "endpoint":
			scope, label = store.Scope{Kind: store.ScopeEndpoint, ID: id}, "this endpoint"
			if d, err := store.GetRequestDefinition(db, id); err == nil {
				label = d.Name
			}
		default:
			http.Error(w, "scope must be all, collection or endpoint", http.StatusBadRequest)
			return
		}

		guard.mu.Lock()
		unlock := sync.OnceFunc(guard.mu.Unlock)
		defer unlock()

		render := func(v collectionsView) { renderTemplate(w, tmpl, "confirm-fragment.html", v) }
		active, err := activeRunIn(db, reg, scope)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if active {
			unlock()
			render(collectionsView{Error: activeMessage(label)})
			return
		}
		counts, err := store.CountScope(db, scope)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if counts.Runs == 0 {
			unlock()
			render(collectionsView{Message: "There is no history to clear for " + label + "."})
			return
		}
		if r.FormValue("confirm") != "1" {
			unlock()
			render(collectionsView{Confirm: &confirmView{
				Prompt: fmt.Sprintf("Clear %d runs and their insights for %s? Endpoints and saved test configs are kept.", counts.Runs, label),
				Action: "/history/clear", Target: "closest .slot",
				Fields: []confirmField{{"scope", scopeName}, {"id", id}},
			}})
			return
		}
		n, err := store.ClearHistory(db, scope)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("HX-Trigger", "history-changed")
		unlock()
		render(collectionsView{Message: fmt.Sprintf("Cleared %d runs for %s.", n, label)})
	}
}
