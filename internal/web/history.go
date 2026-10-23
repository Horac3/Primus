package web

import (
	"database/sql"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Horac3/Primus/internal/insight"
	"github.com/Horac3/Primus/internal/model"
	"github.com/Horac3/Primus/internal/store"
)

// historyView is the history fragment's data. Query is empty for the plain list.
type historyView struct {
	Runs   []model.Run
	Query  string
	Result insight.QueryResult
}

func historyFragmentHandler(db *sql.DB, tmpl *template.Template, interpreter insight.QueryInterpreter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		question := strings.TrimSpace(r.URL.Query().Get("q"))
		if utf8.RuneCountInString(question) > insight.MaxQuestionLength {
			http.Error(w, fmt.Sprintf("question is longer than %d characters", insight.MaxQuestionLength), http.StatusBadRequest)
			return
		}
		runs, err := store.ListAllRuns(db)
		if err != nil {
			http.Error(w, "list runs: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if question == "" {
			renderTemplate(w, tmpl, "history-fragment.html", historyView{Runs: runs})
			return
		}
		configs, err := store.ListTestConfigs(db)
		if err != nil {
			http.Error(w, "list test configs: "+err.Error(), http.StatusInternalServerError)
			return
		}
		requests, err := store.ListRequestDefinitions(db)
		if err != nil {
			http.Error(w, "list requests: "+err.Error(), http.StatusInternalServerError)
			return
		}
		now := time.Now()
		result := insight.InterpretQuery(r.Context(), interpreter, insight.DefaultQueryTimeout, question, now)
		renderTemplate(w, tmpl, "history-fragment.html", historyView{
			Runs:   result.Filter.Apply(runs, configs, requests, now),
			Query:  question,
			Result: result,
		})
	}
}
