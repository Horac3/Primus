package web

import (
	"database/sql"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/Horac3/Primus/internal/author"
	"github.com/Horac3/Primus/internal/insight"
	"github.com/Horac3/Primus/internal/model"
	"github.com/Horac3/Primus/internal/store"
)

//go:embed templates static
var embeddedFS embed.FS

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
	provisions := newProvisionRegistry(db)
	mux.HandleFunc("POST /pools/provision", poolProvisionHandler(db, tmpl, provisions))
	mux.HandleFunc("GET /pools/provision/{id}/status", poolProvisionStatusHandler(db, tmpl, provisions))
	mux.HandleFunc("POST /pools/provision/{id}/stop", poolProvisionStopHandler(db, tmpl, provisions))
	mux.HandleFunc("POST /pools/{id}/spec", poolSpecHandler(db, tmpl))
	mux.HandleFunc("POST /pools/{id}/masking", poolMaskingHandler(db, tmpl))
	mux.HandleFunc("POST /pools/{id}/test-login", poolTestLoginHandler(db, tmpl))
	mux.HandleFunc("GET /requests/{id}", requestPanelHandler(db, tmpl, generator != nil))
	mux.HandleFunc("POST /requests/{id}/payloads/generate", postGeneratePayloadsHandler(db, generator))
	mux.HandleFunc("POST /nl/request", postNLRequestHandler(db, generator))
	mux.HandleFunc("POST /nl/testconfig", postNLTestConfigHandler(db, generator, bounds))
	// Serializes starting a run, importing endpoints and deleting or clearing a scope.
	guard := &scopeGuard{}
	mux.HandleFunc("POST /import", importHandler(db, tmpl, generator != nil, guard))
	mux.HandleFunc("POST /probe", probeHandler(db, tmpl))
	mux.HandleFunc("POST /runs", postRunHandler(db, tmpl, reg, guard))
	mux.HandleFunc("GET /collections", collectionsPanelHandler(db, tmpl))
	mux.HandleFunc("POST /collections", createCollectionHandler(db, tmpl))
	mux.HandleFunc("POST /collections/restore", restoreExamplesHandler(db, tmpl))
	mux.HandleFunc("POST /collections/{id}", updateCollectionHandler(db, tmpl))
	mux.HandleFunc("POST /collections/{id}/delete", deleteCollectionHandler(db, tmpl, reg, guard))
	mux.HandleFunc("POST /history/clear", clearHistoryHandler(db, tmpl, reg, guard))
	mux.HandleFunc("POST /runs/{id}/insights", postInsightHandler(db, tmpl, analyzer))
	mux.HandleFunc("GET /runs/{id}/insights", getInsightHandler(db, tmpl))
	mux.HandleFunc("GET /runs/{id}/status", runStatusHandler(db, tmpl, reg, analyzer))
	mux.HandleFunc("POST /runs/{id}/stop", runStopHandler(db, tmpl, reg, analyzer))
	mux.HandleFunc("GET /runs/{id}", getRunHandler(db, tmpl))
	mux.HandleFunc("GET /runs/{id}/series.csv", seriesCSVHandler(db))
	mux.HandleFunc("GET /runs/{id}/report.md", reportMarkdownHandler(db))
	mux.HandleFunc("GET /runs/{id}/report.html", reportHTMLHandler(db, tmpl))
	mux.HandleFunc("GET /history-fragment", historyFragmentHandler(db, tmpl, configuredQueryInterpreter()))
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

// configuredQueryInterpreter returns the model that reads history questions, or
// nil when the rules interpreter answers alone.
func configuredQueryInterpreter() insight.QueryInterpreter {
	config, err := insight.LoadConfig()
	if err != nil {
		log.Printf("web: load history query config: %v; using rules", err)
		return nil
	}
	return insight.NewQueryInterpreter(config)
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

func indexHandler(db *sql.DB, tmpl *template.Template) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		renderTemplate(w, tmpl, "index.html", nil)
	}
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
