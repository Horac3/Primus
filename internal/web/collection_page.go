package web

import (
	"database/sql"
	"errors"
	"fmt"
	"html/template"
	"net/http"

	"github.com/Horac3/Primus/internal/model"
	"github.com/Horac3/Primus/internal/store"
)

// collectionRowView is one endpoint's line on the collection page. A never-run endpoint has no
// RunID and every column shows "-".
type collectionRowView struct {
	Endpoint model.RequestDefinition
	RunID    string
	Verdict  string
	P95      string
	ErrPct   string
	LastRun  string
}

type collectionPageView struct {
	Collection model.Collection
	Rows       []collectionRowView
}

// rowVerdict names a run's verdict; runs saved before outcomes existed fall back to Passed.
func rowVerdict(r model.Run) string {
	if r.Outcome != "" {
		return outcomeLabel(r.Outcome)
	}
	if r.Passed {
		return outcomeLabel(model.OutcomePass)
	}
	return outcomeLabel(model.OutcomeFail)
}

func collectionRows(defs []model.RequestDefinition, latest map[string]model.Run) []collectionRowView {
	rows := make([]collectionRowView, 0, len(defs))
	for _, d := range defs {
		row := collectionRowView{Endpoint: d, Verdict: "-", P95: "-", ErrPct: "-", LastRun: "-"}
		if r, ok := latest[d.ID]; ok {
			row.RunID = r.ID
			row.Verdict = rowVerdict(r)
			row.P95 = fmt.Sprintf("%d ms", r.Summary.P95Millis)
			row.ErrPct = fmt.Sprintf("%.1f%%", r.Summary.ErrorRate*100)
			row.LastRun = r.StartedAt.UTC().Format("2006-01-02 15:04")
		}
		rows = append(rows, row)
	}
	return rows
}

// collectionPageHandler renders a collection's endpoints with the verdict, p95, error rate and time
// of each one's latest run, linking to that run (FR-REP-03).
func collectionPageHandler(db *sql.DB, tmpl *template.Template) http.HandlerFunc {
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
		ids := make([]string, len(defs))
		for i, d := range defs {
			ids[i] = d.ID
		}
		latest, err := store.LatestRunsByRequest(db, ids)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		renderTemplate(w, tmpl, "collection-page.html", collectionPageView{Collection: col, Rows: collectionRows(defs, latest)})
	}
}
