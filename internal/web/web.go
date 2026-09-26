package web

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Horac3/Primus/internal/engine"
	"github.com/Horac3/Primus/internal/importer"
	"github.com/Horac3/Primus/internal/model"
	"github.com/Horac3/Primus/internal/store"
)

// templateFuncs are helpers available inside every template.
var templateFuncs = template.FuncMap{
	"mul": func(a, b float64) float64 { return a * b },
}

// LoadTemplates parses all HTML templates from the given directory path.
// Pass "web/templates" when running from the module root.
func LoadTemplates(dir string) (*template.Template, error) {
	tmpl := template.New("").Funcs(templateFuncs)
	tmpl, err := tmpl.ParseGlob(dir + "/*.html")
	if err != nil {
		return nil, err
	}
	return tmpl, nil
}

// NewServer wires all routes and returns a ready-to-serve *http.ServeMux.
// templateDir is the path to the web/templates directory (e.g. "web/templates").
func NewServer(db *sql.DB) *http.ServeMux {
	return NewServerWithTemplateDir(db, "web/templates")
}

// NewServerWithTemplateDir allows tests to override the template directory.
func NewServerWithTemplateDir(db *sql.DB, templateDir string) *http.ServeMux {
	tmpl, err := LoadTemplates(templateDir)
	if err != nil {
		// Fall back to an empty template set so the server still starts;
		// handlers will log render errors but won't panic.
		log.Printf("web: load templates from %q: %v", templateDir, err)
		tmpl = template.New("").Funcs(templateFuncs)
	}
	mux := http.NewServeMux()

	mux.HandleFunc("/static/", func(w http.ResponseWriter, r *http.Request) {
		http.StripPrefix("/static/", http.FileServer(http.Dir("web/static"))).ServeHTTP(w, r)
	})

	mux.HandleFunc("GET /{$}", dashboardHandler(db, tmpl))
	mux.HandleFunc("POST /import", importHandler(db, tmpl))
	mux.HandleFunc("GET /requests/{id}/run", runFormHandler(db, tmpl))
	mux.HandleFunc("POST /runs", postRunHandler(db, tmpl))
	mux.HandleFunc("GET /runs", listRunsHandler(db, tmpl))
	mux.HandleFunc("GET /runs/{id}", getRunHandler(db, tmpl))

	return mux
}

// ---- dashboard -------------------------------------------------------------

type dashboardData struct {
	Requests    []model.RequestDefinition
	ImportError string
}

func dashboardHandler(db *sql.DB, tmpl *template.Template) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defs, err := store.ListRequestDefinitions(db)
		if err != nil {
			http.Error(w, "failed to load definitions: "+err.Error(), http.StatusInternalServerError)
			return
		}
		renderTemplate(w, tmpl, "dashboard.html", dashboardData{Requests: defs})
	}
}

// ---- POST /import ----------------------------------------------------------

func importHandler(db *sql.DB, tmpl *template.Template) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		specURL := strings.TrimSpace(r.FormValue("spec_url"))
		if specURL == "" {
			http.Error(w, "spec_url is required", http.StatusBadRequest)
			return
		}
		// Basic sanity check — catch obviously bad URLs before a network call.
		if !strings.HasPrefix(specURL, "http://") && !strings.HasPrefix(specURL, "https://") {
			http.Error(w, "spec_url must start with http:// or https://", http.StatusBadRequest)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()

		defs, err := importer.ImportFromURL(ctx, specURL)
		if err != nil {
			http.Error(w, "import failed: "+err.Error(), http.StatusBadRequest)
			return
		}

		for _, d := range defs {
			if err := store.SaveRequestDefinition(db, d); err != nil {
				http.Error(w, "save failed: "+err.Error(), http.StatusInternalServerError)
				return
			}
		}

		// Return the updated request-list fragment for htmx swap.
		all, err := store.ListRequestDefinitions(db)
		if err != nil {
			http.Error(w, "list failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		renderTemplate(w, tmpl, "request-list.html", dashboardData{Requests: all})
	}
}

// ---- GET /requests/{id}/run ------------------------------------------------

func runFormHandler(db *sql.DB, tmpl *template.Template) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		def, err := store.GetRequestDefinition(db, id)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		renderTemplate(w, tmpl, "run-form.html", struct{ Request model.RequestDefinition }{def})
	}
}

// ---- POST /runs ------------------------------------------------------------

func postRunHandler(db *sql.DB, tmpl *template.Template) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		reqID := r.FormValue("request_id")
		if reqID == "" {
			http.Error(w, "request_id is required", http.StatusBadRequest)
			return
		}
		def, err := store.GetRequestDefinition(db, reqID)
		if err != nil {
			http.Error(w, "request not found: "+reqID, http.StatusBadRequest)
			return
		}

		cfg, err := parseTestConfig(r, def.ID)
		if err != nil {
			http.Error(w, "invalid config: "+err.Error(), http.StatusBadRequest)
			return
		}

		if err := store.SaveTestConfig(db, cfg); err != nil {
			http.Error(w, "save config failed: "+err.Error(), http.StatusInternalServerError)
			return
		}

		var run model.Run
		switch cfg.Mode {
		case model.ModeBurst:
			run, err = engine.RunBurst(r.Context(), def, cfg)
		default:
			run, err = engine.RunLoad(r.Context(), def, cfg)
		}
		if err != nil {
			http.Error(w, "run failed: "+err.Error(), http.StatusInternalServerError)
			return
		}

		if err := store.SaveRun(db, run); err != nil {
			http.Error(w, "save run failed: "+err.Error(), http.StatusInternalServerError)
			return
		}

		http.Redirect(w, r, "/runs/"+run.ID, http.StatusSeeOther)
	}
}

// parseTestConfig builds a TestConfig from form values.
func parseTestConfig(r *http.Request, requestID string) (model.TestConfig, error) {
	cfg := model.TestConfig{
		ID:        fmt.Sprintf("cfg-%d", time.Now().UnixNano()),
		RequestID: requestID,
	}

	switch r.FormValue("mode") {
	case "burst":
		cfg.Mode = model.ModeBurst
	default:
		cfg.Mode = model.ModeLoad
	}

	var err error
	cfg.Concurrency, err = parseIntField(r, "concurrency", 1)
	if err != nil {
		return cfg, err
	}
	cfg.TotalRequests, err = parseIntField(r, "total_requests", 1)
	if err != nil {
		return cfg, err
	}

	if cfg.Mode == model.ModeBurst {
		count, err := parseIntField(r, "burst_count", 1)
		if err != nil {
			return cfg, err
		}
		idle, err := parseIntField(r, "burst_idle_ms", 0)
		if err != nil {
			return cfg, err
		}
		cfg.BurstPattern = &model.BurstPattern{Count: count, Idle: idle}
	}

	// Assertions
	if raw := strings.TrimSpace(r.FormValue("expected_status")); raw != "" {
		for _, part := range strings.Split(raw, ",") {
			code, err := strconv.Atoi(strings.TrimSpace(part))
			if err != nil {
				return cfg, fmt.Errorf("invalid status code %q", part)
			}
			cfg.Assertions.ExpectedStatus = append(cfg.Assertions.ExpectedStatus, code)
		}
	}
	if raw := strings.TrimSpace(r.FormValue("max_p95_ms")); raw != "" && raw != "0" {
		v, err := strconv.Atoi(raw)
		if err != nil {
			return cfg, fmt.Errorf("invalid max_p95_ms: %w", err)
		}
		cfg.Assertions.MaxP95Millis = v
	}
	if raw := strings.TrimSpace(r.FormValue("max_error_rate")); raw != "" && raw != "0" {
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return cfg, fmt.Errorf("invalid max_error_rate: %w", err)
		}
		cfg.Assertions.MaxErrorRate = v
	}

	return cfg, nil
}

func parseIntField(r *http.Request, name string, min int) (int, error) {
	raw := strings.TrimSpace(r.FormValue(name))
	if raw == "" {
		return min, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", name, err)
	}
	if v < min {
		return 0, fmt.Errorf("%s must be >= %d", name, min)
	}
	return v, nil
}

// ---- GET /runs -------------------------------------------------------------

func listRunsHandler(db *sql.DB, tmpl *template.Template) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		runs, err := store.ListAllRuns(db)
		if err != nil {
			http.Error(w, "failed to list runs: "+err.Error(), http.StatusInternalServerError)
			return
		}
		renderTemplate(w, tmpl, "run-history.html", struct{ Runs []model.Run }{runs})
	}
}

// ---- GET /runs/{id} --------------------------------------------------------

func getRunHandler(db *sql.DB, tmpl *template.Template) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		run, err := store.GetRun(db, id)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		renderTemplate(w, tmpl, "run-result.html", struct{ Run model.Run }{run})
	}
}

// ---- helpers ---------------------------------------------------------------

func renderTemplate(w http.ResponseWriter, tmpl *template.Template, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, name, data); err != nil {
		// Headers already sent; log only.
		log.Printf("template %q: %v", name, err)
	}
}

// sentinel so the package compiles even if embed path needs adjusting
var _ = errors.New
