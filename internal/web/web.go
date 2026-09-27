package web

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
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

//go:embed templates static
var embeddedFS embed.FS

// last4 returns the last 4 chars of s for masked display.
func last4(s string) string {
	if len(s) <= 4 {
		return s
	}
	return s[len(s)-4:]
}

// templateFuncs are helpers available inside every template.
var templateFuncs = template.FuncMap{
	"mul": func(a, b float64) float64 { return a * b },
	"outcomeClass": func(o model.RunOutcome) string {
		switch o {
		case model.OutcomePass:
			return "badge-pass"
		case model.OutcomeNoAssertions:
			return "badge-neutral"
		default:
			return "badge-fail"
		}
	},
	"outcomeLabel": func(o model.RunOutcome) string {
		switch o {
		case model.OutcomePass:
			return "✓ PASSED"
		case model.OutcomeNoAssertions:
			return "◎ Completed — no assertions configured"
		case model.OutcomeFailNoRequests:
			return "✗ FAILED — no successful requests"
		default:
			return "✗ FAILED"
		}
	},
	"authSummary": func(a model.AuthConfig) string {
		switch a.Type {
		case model.AuthTypeBearer:
			return "Bearer ••••" + last4(a.Token)
		case model.AuthTypeAPIKey:
			h := a.Header
			if h == "" {
				h = "key"
			}
			return "API key (" + h + ")"
		case model.AuthTypeBasic:
			return "Basic (" + a.Username + ")"
		case model.AuthTypeLoginChained:
			return "Login-chained"
		default:
			return "Not set"
		}
	},
}

// loadEmbeddedTemplates parses all HTML templates from the embedded FS.
func loadEmbeddedTemplates() *template.Template {
	tmpl := template.Must(
		template.New("").Funcs(templateFuncs).ParseFS(embeddedFS, "templates/*.html"),
	)
	return tmpl
}

// NewServer wires all routes using the embedded templates and static assets.
func NewServer(db *sql.DB) *http.ServeMux {
	return newServerWithTemplates(db, loadEmbeddedTemplates())
}

// NewServerWithTemplateDir loads templates from disk — used in tests.
func NewServerWithTemplateDir(db *sql.DB, templateDir string) *http.ServeMux {
	tmpl := template.New("").Funcs(templateFuncs)
	tmpl, err := tmpl.ParseGlob(templateDir + "/*.html")
	if err != nil {
		log.Printf("web: load templates from %q: %v", templateDir, err)
		tmpl = template.New("").Funcs(templateFuncs)
	}
	return newServerWithTemplates(db, tmpl)
}

func newServerWithTemplates(db *sql.DB, tmpl *template.Template) *http.ServeMux {
	mux := http.NewServeMux()

	staticFS, err := fs.Sub(embeddedFS, "static")
	if err != nil {
		log.Printf("web: sub static FS: %v", err)
	} else {
		mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))
	}

	mux.HandleFunc("GET /{$}", indexHandler(db, tmpl))
	mux.HandleFunc("POST /auth", authHandler(db, tmpl))
	mux.HandleFunc("GET /auth-fragment", authFragmentHandler(db, tmpl))
	mux.HandleFunc("GET /sidebar-fragment", sidebarHandler(db, tmpl))
	mux.HandleFunc("GET /requests/{id}", requestPanelHandler(db, tmpl))
	mux.HandleFunc("POST /import", importHandler(db, tmpl))
	mux.HandleFunc("POST /probe", probeHandler(db, tmpl))
	mux.HandleFunc("POST /runs", postRunHandler(db, tmpl))
	mux.HandleFunc("GET /runs/{id}", getRunHandler(db, tmpl))
	mux.HandleFunc("GET /history-fragment", historyFragmentHandler(db, tmpl))

	return mux
}

// ---- GET / — single page ---------------------------------------------------

func indexHandler(db *sql.DB, tmpl *template.Template) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		renderTemplate(w, tmpl, "index.html", nil)
	}
}

// ---- auth bar --------------------------------------------------------------

type authBarData struct {
	Auth model.AuthConfig
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
		renderTemplate(w, tmpl, "auth-bar.html", authBarData{Auth: auth})
	}
}

func authFragmentHandler(db *sql.DB, tmpl *template.Template) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth, err := store.GetGlobalAuth(db)
		if err != nil {
			http.Error(w, "load auth: "+err.Error(), http.StatusInternalServerError)
			return
		}
		renderTemplate(w, tmpl, "auth-bar.html", authBarData{Auth: auth})
	}
}

// ---- sidebar ---------------------------------------------------------------

func sidebarHandler(db *sql.DB, tmpl *template.Template) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defs, err := store.ListRequestDefinitions(db)
		if err != nil {
			http.Error(w, "list endpoints: "+err.Error(), http.StatusInternalServerError)
			return
		}
		renderTemplate(w, tmpl, "sidebar.html", defs)
	}
}

// ---- GET /requests/{id} — main-panel fragment ------------------------------

type panelData struct {
	Def     model.RequestDefinition
	BodyStr string
	IsNew   bool
}

func panelDataFor(def model.RequestDefinition, isNew bool) panelData {
	var body string
	if def.Body != nil {
		body = string(def.Body.Raw)
	}
	return panelData{Def: def, BodyStr: body, IsNew: isNew}
}

func requestPanelHandler(db *sql.DB, tmpl *template.Template) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "new" {
			renderTemplate(w, tmpl, "endpoint-panel.html", panelDataFor(model.RequestDefinition{Method: "GET"}, true))
			return
		}
		def, err := store.GetRequestDefinition(db, id)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		renderTemplate(w, tmpl, "endpoint-panel.html", panelDataFor(def, false))
	}
}

// ---- POST /import ----------------------------------------------------------

func importHandler(db *sql.DB, tmpl *template.Template) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		specURL := strings.TrimSpace(r.FormValue("spec_url"))
		specText := strings.TrimSpace(r.FormValue("spec_text"))
		if specURL == "" && specText == "" {
			http.Error(w, "spec_url or spec_text is required", http.StatusBadRequest)
			return
		}

		var defs []model.RequestDefinition
		var err error
		if specURL != "" {
			if !strings.HasPrefix(specURL, "http://") && !strings.HasPrefix(specURL, "https://") {
				http.Error(w, "spec_url must start with http:// or https://", http.StatusBadRequest)
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
			defer cancel()
			defs, err = importer.ImportFromURL(ctx, specURL)
		} else {
			defs, err = importer.ImportFromBytes([]byte(specText))
		}
		if err != nil {
			http.Error(w, "import failed: "+err.Error(), http.StatusBadRequest)
			return
		}

		for i := range defs {
			if err := store.SaveRequestDefinition(db, defs[i]); err != nil {
				http.Error(w, "save failed: "+err.Error(), http.StatusInternalServerError)
				return
			}
		}

		all, err := store.ListRequestDefinitions(db)
		if err != nil {
			http.Error(w, "list failed: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// Sidebar refresh, plus the first imported endpoint loaded into the
		// main panel via an out-of-band swap.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := tmpl.ExecuteTemplate(w, "sidebar.html", all); err != nil {
			log.Printf("template sidebar.html: %v", err)
			return
		}
		if len(defs) > 0 {
			fmt.Fprint(w, `<div id="main-panel" hx-swap-oob="true">`)
			if err := tmpl.ExecuteTemplate(w, "endpoint-panel.html", panelDataFor(defs[0], false)); err != nil {
				log.Printf("template endpoint-panel.html: %v", err)
				return
			}
			fmt.Fprint(w, `</div>`)
		}
	}
}

// ---- POST /probe -----------------------------------------------------------

func probeHandler(db *sql.DB, tmpl *template.Template) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		def := requestDefFromForm(r)
		auth, err := store.GetGlobalAuth(db)
		if err != nil {
			http.Error(w, "load auth: "+err.Error(), http.StatusInternalServerError)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()

		result, _ := engine.Probe(ctx, def, auth) // Probe never errors; failures in result
		renderTemplate(w, tmpl, "probe-result.html", result)
	}
}

// ---- POST /runs — inline fragment ------------------------------------------

func postRunHandler(db *sql.DB, tmpl *template.Template) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		def := requestDefFromForm(r)
		if def.ID == "" {
			def.ID = fmt.Sprintf("ad-hoc-%d", time.Now().UnixNano())
		}
		if def.Name == "" {
			def.Name = def.Method + " " + def.URLTemplate
		}

		if err := store.SaveRequestDefinition(db, def); err != nil {
			http.Error(w, "save definition: "+err.Error(), http.StatusInternalServerError)
			return
		}

		cfg, err := parseTestConfig(r, def.ID)
		if err != nil {
			http.Error(w, "invalid config: "+err.Error(), http.StatusBadRequest)
			return
		}
		if err := store.SaveTestConfig(db, cfg); err != nil {
			http.Error(w, "save config: "+err.Error(), http.StatusInternalServerError)
			return
		}

		auth, err := store.GetGlobalAuth(db)
		if err != nil {
			http.Error(w, "load auth: "+err.Error(), http.StatusInternalServerError)
			return
		}

		var run model.Run
		switch cfg.Mode {
		case model.ModeBurst:
			run, err = engine.RunBurst(r.Context(), def, cfg, auth)
		case model.ModeSoak:
			run, err = engine.RunSoak(r.Context(), def, cfg, auth)
		default:
			run, err = engine.RunLoad(r.Context(), def, cfg, auth)
		}
		if err != nil {
			http.Error(w, "run failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if err := store.SaveRun(db, run); err != nil {
			http.Error(w, "save run: "+err.Error(), http.StatusInternalServerError)
			return
		}

		renderTemplate(w, tmpl, "run-result-fragment.html", run)
	}
}

// ---- GET /runs/{id} — shareable page ---------------------------------------

func getRunHandler(db *sql.DB, tmpl *template.Template) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		run, err := store.GetRun(db, id)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		renderTemplate(w, tmpl, "run-page.html", run)
	}
}

// ---- GET /history-fragment -------------------------------------------------

func historyFragmentHandler(db *sql.DB, tmpl *template.Template) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		runs, err := store.ListAllRuns(db)
		if err != nil {
			http.Error(w, "list runs: "+err.Error(), http.StatusInternalServerError)
			return
		}
		renderTemplate(w, tmpl, "history-fragment.html", runs)
	}
}

// ---- helpers ---------------------------------------------------------------

// requestDefFromForm builds a RequestDefinition from POST form fields.
// Authentication is global now — no auth fields are read here.
func requestDefFromForm(r *http.Request) model.RequestDefinition {
	def := model.RequestDefinition{
		ID:          strings.TrimSpace(r.FormValue("request_id")),
		Method:      strings.ToUpper(strings.TrimSpace(r.FormValue("method"))),
		URLTemplate: strings.TrimSpace(r.FormValue("url")),
	}
	if def.Method == "" {
		def.Method = "GET"
	}
	// Body
	body := strings.TrimSpace(r.FormValue("body"))
	if body != "" {
		def.Body = &model.BodyTemplate{Raw: []byte(body)}
		if def.Headers == nil {
			def.Headers = map[string]string{"Content-Type": "application/json"}
		}
	}
	return def
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
	case "soak":
		cfg.Mode = model.ModeSoak
	case "auth_probe":
		cfg.Mode = model.ModeAuthProbe
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

	if cfg.Mode == model.ModeSoak {
		raw := strings.TrimSpace(r.FormValue("soak_seconds"))
		if raw == "" {
			cfg.Duration = 30 * time.Second
		} else {
			secs, err := strconv.Atoi(raw)
			if err != nil || secs <= 0 {
				return cfg, fmt.Errorf("soak_seconds must be a positive integer")
			}
			cfg.Duration = time.Duration(secs) * time.Second
		}
	}

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
		v, _ := strconv.Atoi(raw)
		cfg.Assertions.MaxP95Millis = v
	}
	if raw := strings.TrimSpace(r.FormValue("max_error_rate")); raw != "" && raw != "0" {
		v, _ := strconv.ParseFloat(raw, 64)
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

func renderTemplate(w http.ResponseWriter, tmpl *template.Template, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("template %q: %v", name, err)
	}
}
