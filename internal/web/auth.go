package web

import (
	"database/sql"
	"html/template"
	"net/http"
	"strings"

	"github.com/Horac3/Primus/internal/model"
	"github.com/Horac3/Primus/internal/store"
)

type authBarData struct {
	Auth       model.AuthConfig
	Pools      []store.PoolSummary
	ActivePool *store.PoolSummary // non-nil: this pool overrides global auth for runs
}

// authFromForm builds a global AuthConfig from the auth-bar form fields.
func authFromForm(r *http.Request) model.AuthConfig {
	var a model.AuthConfig
	switch r.FormValue("auth_type") {
	case "bearer":
		a.Type = model.AuthTypeBearer
		a.Token = strings.TrimSpace(r.FormValue("auth_token"))
	case "api_key":
		a.Type = model.AuthTypeAPIKey
		a.Header = strings.TrimSpace(r.FormValue("auth_header"))
		if a.Header == "" {
			a.Header = "X-API-Key"
		}
		a.Value = strings.TrimSpace(r.FormValue("auth_token"))
	case "basic":
		a.Type = model.AuthTypeBasic
		a.Username = strings.TrimSpace(r.FormValue("auth_username"))
		a.Password = r.FormValue("auth_password")
	default:
		a.Type = model.AuthTypeNone
	}
	return a
}

func authHandler(db *sql.DB, tmpl *template.Template) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := authFromForm(r)
		if err := store.SaveGlobalAuth(db, auth); err != nil {
			http.Error(w, "save auth: "+err.Error(), http.StatusInternalServerError)
			return
		}
		data, err := loadAuthBar(db)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		renderTemplate(w, tmpl, "auth-bar.html", data)
	}
}

func authFragmentHandler(db *sql.DB, tmpl *template.Template) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := loadAuthBar(db)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		renderTemplate(w, tmpl, "auth-bar.html", data)
	}
}
