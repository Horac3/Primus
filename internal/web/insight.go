package web

import (
	"context"
	"database/sql"
	"errors"
	"html/template"
	"log"
	"net/http"

	"github.com/Horac3/Primus/internal/insight"
	"github.com/Horac3/Primus/internal/model"
	"github.com/Horac3/Primus/internal/store"
)

type insightFragmentData struct {
	RunID   string
	Insight *store.InsightRecord
}

func loadInsightView(db *sql.DB, runID string) insightFragmentData {
	view := insightFragmentData{RunID: runID}
	record, err := store.GetInsight(db, runID)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			log.Printf("web: load insight for run %s: %v", runID, err)
		}
		return view
	}
	view.Insight = &record
	return view
}

func getInsightHandler(db *sql.DB, tmpl *template.Template) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if _, err := store.GetRun(db, id); err != nil {
			http.NotFound(w, r)
			return
		}
		renderTemplate(w, tmpl, "insight-fragment", loadInsightView(db, id))
	}
}

func postInsightHandler(db *sql.DB, tmpl *template.Template, analyzer insight.Analyzer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		run, err := store.GetRun(db, id)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		modelName := analyzerModelName(analyzer)
		record, started, err := store.StartInsight(db, id, analyzer.Name(), modelName)
		if err != nil {
			http.Error(w, "start insight: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if started {
			go generateRunInsight(db, run, analyzer)
		}
		renderTemplate(w, tmpl, "insight-fragment", insightFragmentData{RunID: id, Insight: &record})
	}
}

func generateRunInsight(db *sql.DB, run model.Run, analyzer insight.Analyzer) {
	modelName := analyzerModelName(analyzer)
	summary := insight.BuildSummary(run)
	findings := insight.Evaluate(summary)
	report, err := analyzer.Analyze(context.Background(), summary, findings)
	if err != nil {
		log.Printf("web: analyzer %s failed for run %s; falling back to rules: %v", analyzer.Name(), run.ID, err)
		report, err = (insight.RulesAnalyzer{}).Analyze(context.Background(), summary, findings)
	}
	if err != nil {
		if updateErr := store.FailInsight(db, run.ID, analyzer.Name(), modelName, err.Error()); updateErr != nil {
			log.Printf("web: persist failed insight for run %s: %v", run.ID, updateErr)
		}
		return
	}
	if err := store.CompleteInsight(db, run.ID, report.AnalyzerName, report.ModelName, report.Text); err != nil {
		log.Printf("web: persist completed insight for run %s: %v", run.ID, err)
		// A pending row would keep the fragment polling "Generating..." until restart; end it as failed.
		if updateErr := store.FailInsight(db, run.ID, report.AnalyzerName, report.ModelName, "the generated insight could not be saved"); updateErr != nil {
			log.Printf("web: persist failed insight for run %s: %v", run.ID, updateErr)
		}
	}
}

func analyzerModelName(analyzer insight.Analyzer) string {
	if modelNamer, ok := analyzer.(interface{ ModelName() string }); ok {
		return modelNamer.ModelName()
	}
	return analyzer.Name()
}
