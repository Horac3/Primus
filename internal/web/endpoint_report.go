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

// trendRunLimit is how many of an endpoint's runs the report's trend table shows (FR-REP-01).
const trendRunLimit = 10

// trendRowView is one run's line in the trend table.
type trendRowView struct {
	Run     model.Run
	Started string
	ErrPct  string
	Verdict string
}

type endpointReportView struct {
	Endpoint   model.RequestDefinition
	URL        string
	Collection string
	Latest     *trendRowView
	Rows       []trendRowView
	Insight    insightFragmentData
}

func trendRow(r model.Run) trendRowView {
	return trendRowView{
		Run:     r,
		Started: r.StartedAt.UTC().Format("2006-01-02 15:04"),
		ErrPct:  fmt.Sprintf("%.1f%%", r.Summary.ErrorRate*100),
		Verdict: rowVerdict(r),
	}
}

// endpointReportHandler renders one endpoint's report: its latest run, a table of its last runs and the
// latest run's findings. Every number comes from stored runs; nothing here is written by a model.
func endpointReportHandler(db *sql.DB, tmpl *template.Template) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		def, err := store.GetRequestDefinition(db, r.PathValue("id"))
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		runs, err := store.RecentRunsByRequest(db, def.ID, trendRunLimit)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		view := endpointReportView{Endpoint: def, URL: def.URLTemplate}
		if resolved, err := store.ResolveURL(db, def); err == nil {
			view.URL = resolved.URLTemplate
		}
		view.URL = redactURLs(cutAt(view.URL, "?#"))
		if def.CollectionID != "" {
			if col, err := store.GetCollection(db, def.CollectionID); err == nil {
				view.Collection = col.Name
			}
		}
		for _, run := range runs {
			view.Rows = append(view.Rows, trendRow(run))
		}
		if len(view.Rows) > 0 {
			view.Latest = &view.Rows[0]
			view.Insight = loadInsightView(db, view.Latest.Run.ID)
		}
		renderTemplate(w, tmpl, "endpoint-report.html", view)
	}
}
