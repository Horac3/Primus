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
	"sort"
	"strconv"
	"strings"
	"time"

	primusdocs "github.com/Horac3/Primus/docs"
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
	"bucketLabel": func(i int) string {
		return bucketLabel(i)
	},
	"requestsPerSec": func(r model.Run) string {
		secs := r.FinishedAt.Sub(r.StartedAt).Seconds()
		if secs <= 0 {
			secs = 0.001
		}
		return strconv.FormatFloat(float64(r.Summary.TotalRequests)/secs, 'f', 1, 64)
	},
	"histogramBars": func(buckets []int) []bucketBar {
		max := 0
		for _, c := range buckets {
			if c > max {
				max = c
			}
		}
		out := make([]bucketBar, 0, len(buckets))
		for i, c := range buckets {
			h := 0
			if max > 0 {
				h = (c*100 + max/2) / max
			}
			out = append(out, bucketBar{Label: bucketLabel(i), Height: h, Count: c})
		}
		return out
	},
	"latencyPolyline": func(points []model.Point) string {
		return polylineForSeries(points, func(p model.Point) float64 { return p.AvgLatencyMs }, 280, 60)
	},
	"rpsPolyline": func(points []model.Point) string {
		return polylineForSeries(points, func(p model.Point) float64 { return p.RPS }, 280, 60)
	},
	"latencyAxis": func(points []model.Point) chartAxis {
		return seriesAxis(points, func(p model.Point) float64 { return p.AvgLatencyMs })
	},
	"rpsAxis": func(points []model.Point) chartAxis {
		return seriesAxis(points, func(p model.Point) float64 { return p.RPS })
	},
	"elapsedTicks": elapsedTicks,
}

// bucketLabel names bucket i of the fixed latency edges ("0–49", …, "≥600").
func bucketLabel(i int) string {
	edges := model.LatencyBucketEdges
	if i < 0 || i > len(edges) {
		return "?"
	}
	if i == len(edges) {
		return "≥" + strconv.Itoa(edges[len(edges)-1])
	}
	lo := 0
	if i > 0 {
		lo = edges[i-1]
	}
	return strconv.Itoa(lo) + "–" + strconv.Itoa(edges[i]-1)
}

type chartAxis struct {
	Max string
	Min string
}

type elapsedAxis struct {
	Start string
	Mid   string
	End   string
}

func seriesAxis(points []model.Point, value func(model.Point) float64) chartAxis {
	if len(points) == 0 {
		return chartAxis{}
	}
	min, max := value(points[0]), value(points[0])
	for _, point := range points[1:] {
		v := value(point)
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}
	if max == min {
		if max == 0 {
			max = 1
		} else {
			min = max - 1
		}
	}
	return chartAxis{Max: formatAxisValue(max), Min: formatAxisValue(min)}
}

func elapsedTicks(points []model.Point) elapsedAxis {
	if len(points) == 0 {
		return elapsedAxis{}
	}
	start := points[0].ElapsedMs
	end := points[len(points)-1].ElapsedMs
	mid := start + (end-start)/2
	return elapsedAxis{
		Start: formatElapsed(start),
		Mid:   formatElapsed(mid),
		End:   formatElapsed(end),
	}
}

func formatAxisValue(value float64) string {
	formatted := strconv.FormatFloat(value, 'f', 1, 64)
	formatted = strings.TrimSuffix(strings.TrimSuffix(formatted, "0"), ".")
	return formatted
}

func formatElapsed(milliseconds int64) string {
	formatted := strconv.FormatFloat(float64(milliseconds)/1000, 'f', 1, 64)
	formatted = strings.TrimSuffix(strings.TrimSuffix(formatted, "0"), ".")
	return formatted + "s"
}

func polylineForSeries(points []model.Point, value func(model.Point) float64, width, height int) string {
	if len(points) == 0 {
		return ""
	}
	min := value(points[0])
	max := min
	for _, p := range points[1:] {
		v := value(p)
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}
	if max == min {
		if max == 0 {
			max = 1
		} else {
			min = max - 1
		}
	}
	coords := make([]string, len(points))
	firstElapsed := points[0].ElapsedMs
	lastElapsed := points[len(points)-1].ElapsedMs
	for i, p := range points {
		x := 0.0
		if len(points) > 1 {
			if lastElapsed > firstElapsed {
				x = float64(p.ElapsedMs-firstElapsed) * float64(width) / float64(lastElapsed-firstElapsed)
			} else {
				x = float64(i) * float64(width) / float64(len(points)-1)
			}
		}
		y := float64(height) - ((value(p)-min)/(max-min))*float64(height)
		coords[i] = fmt.Sprintf("%g,%g", x, y)
	}
	return strings.Join(coords, " ")
}

// bucketBar is one histogram bar: Label is the ms range, Height is percent
// of the tallest bar (0-100, rendered as px), Count the raw bucket count.
type bucketBar struct {
	Label  string
	Height int
	Count  int
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

	// Async run registry: finished runs persist to SQLite, in-flight ones
	// live here for polling + Stop.
	reg := NewRegistry(func(r model.Run) error { return store.SaveRun(db, r) })

	mux.HandleFunc("GET /{$}", indexHandler(db, tmpl))
	mux.HandleFunc("POST /auth", authHandler(db, tmpl))
	mux.HandleFunc("GET /auth-fragment", authFragmentHandler(db, tmpl))
	mux.HandleFunc("GET /sidebar-fragment", sidebarHandler(db, tmpl))
	mux.HandleFunc("GET /requests/{id}", requestPanelHandler(db, tmpl))
	mux.HandleFunc("POST /import", importHandler(db, tmpl))
	mux.HandleFunc("POST /probe", probeHandler(db, tmpl))
	mux.HandleFunc("POST /runs", postRunHandler(db, tmpl, reg))
	mux.HandleFunc("GET /runs/{id}/status", runStatusHandler(tmpl, reg))
	mux.HandleFunc("POST /runs/{id}/stop", runStopHandler(tmpl, reg))
	mux.HandleFunc("GET /runs/{id}", getRunHandler(db, tmpl))
	mux.HandleFunc("GET /history-fragment", historyFragmentHandler(db, tmpl))
	mux.HandleFunc("GET /docs", docsIndexHandler(tmpl))
	mux.HandleFunc("GET /docs/{slug}", docsPageHandler(tmpl))

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

type sidebarData struct {
	Endpoints []model.RequestDefinition
	// Recent maps an endpoint ID to its latest (≤8, newest-first) run
	// pass/fail flags — the sidebar sparkline.
	Recent map[string][]bool
}

// loadSidebarData joins endpoints → test configs → runs for the sparkline.
func loadSidebarData(db *sql.DB) (sidebarData, error) {
	defs, err := store.ListRequestDefinitions(db)
	if err != nil {
		return sidebarData{}, fmt.Errorf("list endpoints: %w", err)
	}
	cfgs, err := store.ListTestConfigs(db)
	if err != nil {
		return sidebarData{}, fmt.Errorf("list configs: %w", err)
	}
	cfgReq := make(map[string]string, len(cfgs))
	for _, c := range cfgs {
		cfgReq[c.ID] = c.RequestID
	}
	runs, err := store.ListAllRuns(db)
	if err != nil {
		return sidebarData{}, fmt.Errorf("list runs: %w", err)
	}
	byReq := make(map[string][]model.Run)
	for _, r := range runs {
		if req, ok := cfgReq[r.TestConfigID]; ok {
			byReq[req] = append(byReq[req], r)
		}
	}
	recent := make(map[string][]bool, len(byReq))
	for req, rs := range byReq {
		sort.Slice(rs, func(i, j int) bool { return rs[i].StartedAt.After(rs[j].StartedAt) })
		if len(rs) > 8 {
			rs = rs[:8]
		}
		flags := make([]bool, 0, len(rs))
		for _, r := range rs {
			flags = append(flags, r.Passed)
		}
		recent[req] = flags
	}
	return sidebarData{Endpoints: defs, Recent: recent}, nil
}

func sidebarHandler(db *sql.DB, tmpl *template.Template) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := loadSidebarData(db)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		renderTemplate(w, tmpl, "sidebar.html", data)
	}
}

// ---- GET /requests/{id} — main-panel fragment ------------------------------

type panelData struct {
	Def           model.RequestDefinition
	BodyStr       string
	AuthzOverride string
	IsNew         bool
}

func panelDataFor(def model.RequestDefinition, isNew bool) panelData {
	var body string
	if def.Body != nil {
		body = string(def.Body.Raw)
	}
	var override string
	if def.Headers != nil {
		override = def.Headers["Authorization"]
	}
	return panelData{Def: def, BodyStr: body, AuthzOverride: override, IsNew: isNew}
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

		data, err := loadSidebarData(db)
		if err != nil {
			http.Error(w, "list failed: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// Sidebar refresh, plus the first imported endpoint loaded into the
		// main panel via an out-of-band swap.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := tmpl.ExecuteTemplate(w, "sidebar.html", data); err != nil {
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

// ---- POST /runs — start async, return the running fragment ---------------

type runningData struct {
	RunID      string
	Detail     string // set on the first render only; polls re-render minimally
	TimeSeries []model.Point
}

func postRunHandler(db *sql.DB, tmpl *template.Template, reg *Registry) http.HandlerFunc {
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
		if err := engine.ValidateTestConfig(cfg); err != nil {
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

		// One id for the registry entry, the poll URL, and the persisted run.
		runID := fmt.Sprintf("run-%d", time.Now().UnixNano())
		h := &runHandle{done: make(chan struct{})}
		reg.startWithHandle(r.Context(), runID, h, func(ctx context.Context, h *runHandle) (model.Run, error) {
			run, err := engine.RunWithSnapshot(ctx, def, cfg, auth, &h.snapshot)
			if err != nil {
				return model.Run{}, err
			}
			run.ID = runID
			return run, nil
		})

		w.WriteHeader(http.StatusAccepted)
		renderTemplate(w, tmpl, "running.html", runningData{
			RunID:  runID,
			Detail: fmt.Sprintf("%s · concurrency %d", cfg.Mode, cfg.Concurrency),
		})
	}
}

// ---- GET /runs/status/{id} — poll an in-flight run --------------------------

func runStatusHandler(tmpl *template.Template, reg *Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		renderStatus(w, r, tmpl, reg, r.PathValue("id"))
	}
}

// ---- POST /runs/{id}/stop — cancel an in-flight run -------------------------

func runStopHandler(tmpl *template.Template, reg *Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		reg.Stop(id) // no-op when already finished; status render decides
		renderStatus(w, r, tmpl, reg, id)
	}
}

// renderStatus renders the running fragment (keeps the client polling) or
// the final result fragment (polling stops — no poll trigger in it).
func renderStatus(w http.ResponseWriter, r *http.Request, tmpl *template.Template, reg *Registry, id string) {
	status, result, ok := reg.Poll(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if status == "running" || result == nil {
		renderTemplate(w, tmpl, "running.html", runningData{RunID: id, TimeSeries: reg.Snapshot(id)})
		return
	}
	renderTemplate(w, tmpl, "run-result-fragment.html", *result)
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

// ---- GET /docs + GET /docs/{slug} — in-app reference viewer ---------------

func docsIndexHandler(tmpl *template.Template) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		renderTemplate(w, tmpl, "docs-index.html", primusdocs.List())
	}
}

type docsPageData struct {
	Doc  primusdocs.Doc
	Nav  []primusdocs.Doc
	Body template.HTML
}

func docsPageHandler(tmpl *template.Template) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := r.PathValue("slug")
		body, err := primusdocs.Render(slug)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		title := slug
		for _, d := range primusdocs.List() {
			if d.Slug == slug {
				title = d.Title
				break
			}
		}
		renderTemplate(w, tmpl, "docs-page.html", docsPageData{
			Doc:  primusdocs.Doc{Slug: slug, Title: title},
			Nav:  primusdocs.List(),
			Body: body,
		})
	}
}

// ---- helpers ---------------------------------------------------------------

// requestDefFromForm builds a RequestDefinition from POST form fields.
// Authentication stays global; the form may carry one per-endpoint
// Authorization override (authz_override) — empty or the [GLOBAL]
// sentinel means "no override, use global auth".
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
	// Per-endpoint Authorization override (sentinel or empty = global applies).
	if ov := strings.TrimSpace(r.FormValue("authz_override")); ov != "" && ov != model.HeaderGlobalSentinel {
		if def.Headers == nil {
			def.Headers = map[string]string{}
		}
		def.Headers["Authorization"] = ov
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

	// Stop condition follows the mode: load and soak are duration-driven
	// in the UI (they share the duration field); burst/auth-probe stay
	// count-driven. The engine still accepts either for any mode.
	if cfg.Mode == model.ModeLoad || cfg.Mode == model.ModeSoak {
		raw := strings.TrimSpace(r.FormValue("duration_seconds"))
		if raw == "" {
			cfg.Duration = 30 * time.Second
		} else {
			secs, err := strconv.Atoi(raw)
			if err != nil || secs <= 0 {
				return cfg, fmt.Errorf("duration_seconds must be a positive integer")
			}
			cfg.Duration = time.Duration(secs) * time.Second
		}
	} else {
		cfg.TotalRequests, err = parseIntField(r, "total_requests", 1)
		if err != nil {
			return cfg, err
		}
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
