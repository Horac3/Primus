package web

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"math"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	primusdocs "github.com/Horac3/Primus/docs"
	"github.com/Horac3/Primus/internal/author"
	"github.com/Horac3/Primus/internal/engine"
	"github.com/Horac3/Primus/internal/importer"
	"github.com/Horac3/Primus/internal/insight"
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
			return "✗ FAILED — no requests completed"
		case model.OutcomeFailNoLogins:
			return "✗ FAILED — no successful logins"
		case model.OutcomeFailRateNotSustained:
			return "✗ FAILED — target rate not sustained"
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
	"inc":          func(i int) int { return i + 1 },
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
	analyzer := configuredInsightAnalyzer()
	bounds, err := author.LoadBounds(os.Getenv)
	if err != nil {
		log.Printf("web: load author generation limits: %v; using defaults", err)
		bounds = author.DefaultBounds()
	}
	generator := configuredAuthorGenerator(bounds)
	if _, err := store.FailPendingInsights(db, "insight generation interrupted by server restart"); err != nil {
		log.Printf("web: recover pending insights: %v", err)
	}

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
	mux.HandleFunc("GET /pools-fragment", poolsFragmentHandler(db, tmpl))
	mux.HandleFunc("POST /pools/import", poolImportHandler(db, tmpl))
	mux.HandleFunc("POST /pools/active", poolActiveHandler(db, tmpl))
	mux.HandleFunc("POST /pools/{id}/spec", poolSpecHandler(db, tmpl))
	mux.HandleFunc("POST /pools/{id}/masking", poolMaskingHandler(db, tmpl))
	mux.HandleFunc("POST /pools/{id}/test-login", poolTestLoginHandler(db, tmpl))
	mux.HandleFunc("GET /requests/{id}", requestPanelHandler(db, tmpl, generator != nil))
	mux.HandleFunc("POST /requests/{id}/payloads/generate", postGeneratePayloadsHandler(db, generator))
	mux.HandleFunc("POST /nl/request", postNLRequestHandler(db, generator))
	mux.HandleFunc("POST /nl/testconfig", postNLTestConfigHandler(db, generator, bounds))
	mux.HandleFunc("POST /import", importHandler(db, tmpl, generator != nil))
	mux.HandleFunc("POST /probe", probeHandler(db, tmpl))
	mux.HandleFunc("POST /runs", postRunHandler(db, tmpl, reg))
	mux.HandleFunc("POST /runs/{id}/insights", postInsightHandler(db, tmpl, analyzer))
	mux.HandleFunc("GET /runs/{id}/insights", getInsightHandler(db, tmpl))
	mux.HandleFunc("GET /runs/{id}/status", runStatusHandler(db, tmpl, reg, analyzer))
	mux.HandleFunc("POST /runs/{id}/stop", runStopHandler(db, tmpl, reg, analyzer))
	mux.HandleFunc("GET /runs/{id}", getRunHandler(db, tmpl))
	mux.HandleFunc("GET /history-fragment", historyFragmentHandler(db, tmpl))
	mux.HandleFunc("GET /docs", docsIndexHandler(tmpl))
	mux.HandleFunc("GET /docs/{slug}", docsPageHandler(tmpl))

	return mux
}

func configuredAuthorGenerator(bounds author.Bounds) author.Generator {
	config, err := insight.LoadConfig()
	if err != nil {
		log.Printf("web: load author generator config: %v", err)
		return nil
	}
	generator, err := author.NewGenerator(author.Config{
		Analyzer: config.Analyzer,
		URL:      config.LLMURL,
		Model:    config.LLMModel,
		Timeout:  config.LLMTimeout,
		Bounds:   bounds,
	})
	if err != nil {
		log.Printf("web: configure author generator: %v; natural-language authoring disabled", err)
		return nil
	}
	return generator
}

func configuredInsightAnalyzer() insight.Analyzer {
	config, err := insight.LoadConfig()
	if err != nil {
		log.Printf("web: load insight config: %v", err)
	}
	analyzer, err := insight.NewAnalyzer(config)
	if err != nil {
		log.Printf("web: %v; using rules analyzer", err)
		return insight.RulesAnalyzer{}
	}
	return analyzer
}

// ---- GET / — single page ---------------------------------------------------

func indexHandler(db *sql.DB, tmpl *template.Template) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		renderTemplate(w, tmpl, "index.html", nil)
	}
}

// ---- auth bar --------------------------------------------------------------

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
	Def              model.RequestDefinition
	BodyStr          string
	HeadersJSON      string
	AuthzOverride    string
	IsNew            bool
	HasBody          bool
	AuthoringEnabled bool
}

func panelDataFor(def model.RequestDefinition, isNew, authoringEnabled bool) panelData {
	var body string
	if def.Body != nil {
		body = string(def.Body.Raw)
	}
	headers, err := json.Marshal(def.Headers)
	if err != nil {
		log.Printf("web: marshal endpoint %s headers for builder: %v", def.ID, err)
		headers = []byte("{}")
	}
	var override string
	if def.Headers != nil {
		override = def.Headers["Authorization"]
	}
	return panelData{
		Def: def, BodyStr: body, HeadersJSON: string(headers),
		AuthzOverride: override, IsNew: isNew, HasBody: def.Body != nil && len(def.Body.Raw) > 0,
		AuthoringEnabled: authoringEnabled,
	}
}

func requestPanelHandler(db *sql.DB, tmpl *template.Template, authoringEnabled bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "new" {
			renderTemplate(w, tmpl, "endpoint-panel.html", panelDataFor(model.RequestDefinition{Method: "GET"}, true, authoringEnabled))
			return
		}
		def, err := store.GetRequestDefinition(db, id)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		renderTemplate(w, tmpl, "endpoint-panel.html", panelDataFor(def, false, authoringEnabled))
	}
}

func postNLRequestHandler(db *sql.DB, generator author.Generator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if generator == nil {
			http.Error(w, "natural-language authoring is unavailable; configure PRIMUS_ANALYZER=ollama", http.StatusServiceUnavailable)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
		var input struct {
			Description string `json:"description"`
		}
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			http.Error(w, "invalid request: "+err.Error(), http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(input.Description) == "" {
			http.Error(w, "description is required", http.StatusBadRequest)
			return
		}
		definitions, err := store.ListRequestDefinitions(db)
		if err != nil {
			http.Error(w, "list endpoints: "+err.Error(), http.StatusInternalServerError)
			return
		}
		endpoints := make([]author.Endpoint, 0, len(definitions))
		for _, definition := range definitions {
			endpoints = append(endpoints, author.Endpoint{
				ID: definition.ID, Name: definition.Name, Method: definition.Method, URL: definition.URLTemplate,
			})
		}
		sort.Slice(endpoints, func(i, j int) bool { return endpoints[i].ID < endpoints[j].ID })
		suggestion, err := generator.GenerateRequest(r.Context(), input.Description, endpoints)
		if err != nil {
			log.Printf("web: generate request definition: %v", err)
			http.Error(w, "request generation failed: "+err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if err := json.NewEncoder(w).Encode(suggestion); err != nil {
			log.Printf("web: encode generated request definition: %v", err)
		}
	}
}

func postNLTestConfigHandler(db *sql.DB, generator author.Generator, bounds author.Bounds) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if generator == nil {
			http.Error(w, "natural-language authoring is unavailable; configure PRIMUS_ANALYZER=ollama", http.StatusServiceUnavailable)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
		var input struct {
			RequestID   string `json:"request_id"`
			Description string `json:"description"`
		}
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			http.Error(w, "invalid request: "+err.Error(), http.StatusBadRequest)
			return
		}
		input.RequestID = strings.TrimSpace(input.RequestID)
		if input.RequestID == "" || strings.TrimSpace(input.Description) == "" {
			http.Error(w, "request_id and description are required", http.StatusBadRequest)
			return
		}
		definition, err := store.GetRequestDefinition(db, input.RequestID)
		if err != nil {
			http.Error(w, "load endpoint: "+err.Error(), http.StatusNotFound)
			return
		}
		suggestion, err := generator.GenerateTestConfig(r.Context(), input.Description, author.Endpoint{
			ID: definition.ID, Name: definition.Name, Method: definition.Method, URL: definition.URLTemplate,
		}, bounds)
		if err != nil {
			log.Printf("web: generate test config: %v", err)
			http.Error(w, "test config generation rejected: "+err.Error(), http.StatusUnprocessableEntity)
			return
		}
		if err := author.ValidateTestConfigSuggestion(suggestion, bounds); err != nil {
			log.Printf("web: generated test config failed server validation: %v", err)
			http.Error(w, "test config generation rejected: "+err.Error(), http.StatusUnprocessableEntity)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if err := json.NewEncoder(w).Encode(suggestion); err != nil {
			log.Printf("web: encode generated test config: %v", err)
		}
	}
}

func postGeneratePayloadsHandler(db *sql.DB, generator author.Generator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if generator == nil {
			http.Error(w, "payload generation is unavailable; configure PRIMUS_ANALYZER=ollama", http.StatusServiceUnavailable)
			return
		}
		requestedCount, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("count")))
		if err != nil || requestedCount < 1 {
			http.Error(w, "count must be a positive integer", http.StatusBadRequest)
			return
		}
		count := requestedCount
		if count > author.MaxPayloadCount {
			count = author.MaxPayloadCount
		}
		definition, err := store.GetRequestDefinition(db, r.PathValue("id"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		if definition.Body == nil || len(definition.Body.Raw) == 0 {
			http.Error(w, "endpoint has no body template", http.StatusBadRequest)
			return
		}
		payloads, err := generator.GeneratePayloads(r.Context(), definition.Body.Raw, count)
		if err != nil {
			log.Printf("web: generate payload variants for endpoint %s: %v", definition.ID, err)
			http.Error(w, "payload generation failed: "+err.Error(), http.StatusBadGateway)
			return
		}
		if err := author.ValidatePayloads(definition.Body.Raw, payloads, count); err != nil {
			log.Printf("web: generated payload pool failed server validation for endpoint %s: %v", definition.ID, err)
			http.Error(w, "payload generation rejected: "+err.Error(), http.StatusUnprocessableEntity)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		response := struct {
			Payloads       []json.RawMessage `json:"payloads"`
			RequestedCount int               `json:"requested_count"`
			GeneratedCount int               `json:"generated_count"`
			Capped         bool              `json:"capped"`
		}{
			Payloads: payloads, RequestedCount: requestedCount, GeneratedCount: len(payloads),
			Capped: requestedCount > count,
		}
		if err := json.NewEncoder(w).Encode(response); err != nil {
			log.Printf("web: encode generated payloads for endpoint %s: %v", definition.ID, err)
		}
	}
}

// ---- POST /import ----------------------------------------------------------

func importHandler(db *sql.DB, tmpl *template.Template, authoringEnabled bool) http.HandlerFunc {
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
			if err := tmpl.ExecuteTemplate(w, "endpoint-panel.html", panelDataFor(defs[0], false, authoringEnabled)); err != nil {
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
		def, err := requestDefFromForm(r)
		if err != nil {
			http.Error(w, "invalid request: "+err.Error(), http.StatusBadRequest)
			return
		}
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
	Detail     string // mode, concurrency and the identity the run sends as
	TimeSeries []model.Point
}

func postRunHandler(db *sql.DB, tmpl *template.Template, reg *Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		def, err := requestDefFromForm(r)
		if err != nil {
			http.Error(w, "invalid request: "+err.Error(), http.StatusBadRequest)
			return
		}
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
		if len(cfg.PayloadPool) > 0 {
			if def.Body == nil {
				http.Error(w, "payload_pool requires a JSON body template", http.StatusBadRequest)
				return
			}
			if err := author.ValidatePayloads(def.Body.Raw, cfg.PayloadPool, len(cfg.PayloadPool)); err != nil {
				http.Error(w, "invalid payload_pool: "+err.Error(), http.StatusBadRequest)
				return
			}
		}
		if err := engine.ValidateTestConfig(cfg); err != nil {
			http.Error(w, "invalid config: "+err.Error(), http.StatusBadRequest)
			return
		}
		// An active credential pool overrides global auth for the run,
		// unless auth_source says not to use one.
		var pool *model.CredentialPool
		if cfg.AuthSource.UsesPool() {
			if pool, err = activeRunPool(db); err != nil {
				http.Error(w, "invalid config: "+err.Error(), http.StatusBadRequest)
				return
			}
		}
		auth, err := store.GetGlobalAuth(db)
		if err != nil {
			http.Error(w, "load auth: "+err.Error(), http.StatusInternalServerError)
			return
		}
		// auth_source may swap the active pool for global auth or no auth.
		auth, pool, err = engine.ResolveAuth(cfg, auth, pool)
		if err != nil {
			http.Error(w, "invalid config: "+err.Error(), http.StatusBadRequest)
			return
		}
		detail := fmt.Sprintf("%s · concurrency %d", cfg.Mode, cfg.Concurrency)
		switch {
		case pool != nil:
			cfg.CredentialPoolID = pool.ID
			if err := engine.ValidatePool(cfg, *pool); err != nil {
				http.Error(w, "invalid config: "+err.Error(), http.StatusBadRequest)
				return
			}
			detail += " · logging in as pool " + pool.Name
		case auth.Type == "" || auth.Type == model.AuthTypeNone:
			detail += " · no auth"
		default:
			detail += " · global auth"
		}
		if err := store.SaveTestConfig(db, cfg); err != nil {
			http.Error(w, "save config: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// One id for the registry entry, the poll URL, and the persisted run.
		runID := fmt.Sprintf("run-%d", time.Now().UnixNano())
		h := &runHandle{done: make(chan struct{}), detail: detail}
		reg.startWithHandle(r.Context(), runID, h, func(ctx context.Context, h *runHandle) (model.Run, error) {
			run, err := engine.RunWithPool(ctx, def, cfg, auth, pool, &h.snapshot)
			if err != nil {
				return model.Run{}, err
			}
			run.ID = runID
			return run, nil
		})

		w.WriteHeader(http.StatusAccepted)
		renderTemplate(w, tmpl, "running.html", runningData{RunID: runID, Detail: detail})
	}
}

// ---- GET /runs/status/{id} — poll an in-flight run --------------------------

func runStatusHandler(db *sql.DB, tmpl *template.Template, reg *Registry, analyzer insight.Analyzer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		renderStatus(w, r, db, tmpl, reg, analyzer, r.PathValue("id"))
	}
}

// ---- POST /runs/{id}/stop — cancel an in-flight run -------------------------

func runStopHandler(db *sql.DB, tmpl *template.Template, reg *Registry, analyzer insight.Analyzer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		reg.Stop(id) // no-op when already finished; status render decides
		renderStatus(w, r, db, tmpl, reg, analyzer, id)
	}
}

// renderStatus renders the running fragment (keeps the client polling) or
// the final result fragment (polling stops — no poll trigger in it).
func renderStatus(w http.ResponseWriter, r *http.Request, db *sql.DB, tmpl *template.Template, reg *Registry, analyzer insight.Analyzer, id string) {
	status, result, ok := reg.Poll(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if status == "running" || result == nil {
		renderTemplate(w, tmpl, "running.html", runningData{RunID: id, Detail: reg.Detail(id), TimeSeries: reg.Snapshot(id)})
		return
	}
	ensureCompletedInsight(db, *result, analyzer)
	renderTemplate(w, tmpl, "run-result-fragment.html", runResultData(db, *result))
}

func ensureCompletedInsight(db *sql.DB, run model.Run, analyzer insight.Analyzer) {
	if _, err := store.GetInsight(db, run.ID); err == nil {
		return
	} else if !errors.Is(err, sql.ErrNoRows) {
		log.Printf("web: load insight for completed run %s: %v", run.ID, err)
		return
	}
	modelName := analyzerModelName(analyzer)
	if _, started, err := store.StartInsight(db, run.ID, analyzer.Name(), modelName); err != nil {
		log.Printf("web: start insight for completed run %s: %v", run.ID, err)
		return
	} else if !started {
		return
	}
	generateRunInsight(db, run, analyzer)
}

type runResultTemplateData struct {
	model.Run
	InsightView insightFragmentData
}

func runResultData(db *sql.DB, run model.Run) runResultTemplateData {
	return runResultTemplateData{Run: run, InsightView: loadInsightView(db, run.ID)}
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
		renderTemplate(w, tmpl, "run-page.html", runResultData(db, run))
	}
}

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
func requestDefFromForm(r *http.Request) (model.RequestDefinition, error) {
	def := model.RequestDefinition{
		ID:          strings.TrimSpace(r.FormValue("request_id")),
		Name:        strings.TrimSpace(r.FormValue("request_name")),
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
	if rawHeaders := strings.TrimSpace(r.FormValue("headers_json")); rawHeaders != "" {
		var headers map[string]string
		if err := json.Unmarshal([]byte(rawHeaders), &headers); err != nil {
			return model.RequestDefinition{}, fmt.Errorf("headers_json must be a JSON object with string values: %w", err)
		}
		for name, value := range headers {
			if http.CanonicalHeaderKey(name) == "" || strings.ContainsAny(value, "\r\n") {
				return model.RequestDefinition{}, fmt.Errorf("header %q is invalid", name)
			}
			if def.Headers == nil {
				def.Headers = make(map[string]string)
			}
			def.Headers[name] = value
		}
	}
	// Per-endpoint Authorization override (sentinel or empty = global applies).
	if ov := strings.TrimSpace(r.FormValue("authz_override")); ov != "" && ov != model.HeaderGlobalSentinel {
		if def.Headers == nil {
			def.Headers = map[string]string{}
		}
		def.Headers["Authorization"] = ov
	}
	return def, nil
}

// parseTestConfig builds a TestConfig from form values.
func parseTestConfig(r *http.Request, requestID string) (model.TestConfig, error) {
	cfg := model.TestConfig{
		ID:        fmt.Sprintf("cfg-%d", time.Now().UnixNano()),
		RequestID: requestID,
	}
	if raw := strings.TrimSpace(r.FormValue("payload_pool")); raw != "" {
		if len(raw) > author.MaxPayloadPoolBytes*4 {
			return cfg, fmt.Errorf("payload_pool exceeds the form size limit")
		}
		if err := json.Unmarshal([]byte(raw), &cfg.PayloadPool); err != nil {
			return cfg, fmt.Errorf("payload_pool must be a JSON array: %w", err)
		}
		if cfg.PayloadPool == nil && raw != "[]" {
			return cfg, fmt.Errorf("payload_pool must be a JSON array")
		}
		var poolBytes int
		for i, payload := range cfg.PayloadPool {
			poolBytes += len(payload)
			if poolBytes > author.MaxPayloadPoolBytes {
				return cfg, fmt.Errorf("payload_pool exceeds the %d-byte limit", author.MaxPayloadPoolBytes)
			}
			if len(payload) == 0 || !json.Valid(payload) {
				return cfg, fmt.Errorf("payload_pool item %d is not valid JSON", i+1)
			}
		}
	}
	switch r.FormValue("mode") {
	case "burst":
		cfg.Mode = model.ModeBurst
	case "soak":
		cfg.Mode = model.ModeSoak
	case "auth_probe":
		cfg.Mode = model.ModeAuthProbe
	case "rate":
		cfg.Mode = model.ModeRate
	default:
		cfg.Mode = model.ModeLoad
	}

	var err error
	cfg.Concurrency, err = parseIntField(r, "concurrency", 1)
	if err != nil {
		return cfg, err
	}

	// Stop condition follows the mode: load, soak and rate are
	// duration-driven in the UI (they share the duration field);
	// burst/auth-probe stay count-driven. The engine still accepts either
	// for any mode but rate.
	if cfg.Mode == model.ModeLoad || cfg.Mode == model.ModeSoak || cfg.Mode == model.ModeRate {
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

	// The UI shows these in auth-probe mode only; any other mode that
	// still sends max_rate is rejected by engine.ValidateTestConfig.
	if raw := strings.TrimSpace(r.FormValue("max_rate")); raw != "" {
		rate, err := strconv.ParseFloat(raw, 64)
		if err != nil || rate < 0 || math.IsInf(rate, 0) || math.IsNaN(rate) {
			return cfg, fmt.Errorf("max_rate must be a number >= 0")
		}
		cfg.MaxRate = rate
	}
	// Shown in rate mode only; engine.ValidateTestConfig rejects a missing
	// target in rate mode and any target elsewhere.
	if raw := strings.TrimSpace(r.FormValue("target_rate")); raw != "" {
		rate, err := strconv.ParseFloat(raw, 64)
		if err != nil || !(rate > 0) || math.IsInf(rate, 0) {
			return cfg, fmt.Errorf("target_rate must be a number > 0")
		}
		cfg.TargetRate = rate
	}
	cfg.AuthSource = model.AuthSource(r.FormValue("auth_source"))
	if !cfg.AuthSource.Valid() {
		return cfg, fmt.Errorf("unknown auth_source %q", cfg.AuthSource)
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
