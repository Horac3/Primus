package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Horac3/Primus/internal/engine"
	"github.com/Horac3/Primus/internal/importer"
	"github.com/Horac3/Primus/internal/model"
	"github.com/Horac3/Primus/internal/store"
)

// sidebarGroup is one collection and its endpoints, in name order.
type sidebarGroup struct {
	Collection model.Collection
	Endpoints  []model.RequestDefinition
}

type sidebarData struct {
	Groups []sidebarGroup
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
	collections, err := store.ListCollections(db)
	if err != nil {
		return sidebarData{}, fmt.Errorf("list collections: %w", err)
	}
	sort.SliceStable(defs, func(i, j int) bool {
		return strings.ToLower(defs[i].Name) < strings.ToLower(defs[j].Name)
	})
	groups := make([]sidebarGroup, 0, len(collections))
	at := make(map[string]int, len(collections))
	for _, c := range collections {
		at[c.ID] = len(groups)
		groups = append(groups, sidebarGroup{Collection: c})
	}
	for _, d := range defs {
		if i, ok := at[d.CollectionID]; ok {
			groups[i].Endpoints = append(groups[i].Endpoints, d)
		}
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
	return sidebarData{Groups: groups, Recent: recent}, nil
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

type panelData struct {
	Def              model.RequestDefinition
	BodyStr          string
	HeadersJSON      string
	AuthzOverride    string
	IsNew            bool
	HasBody          bool
	AuthoringEnabled bool
	Collections      []model.Collection
	// DefaultOption adds a leading "My endpoints" choice (empty value) when no collection can serve as
	// the default yet; saving then creates it.
	DefaultOption bool
}

func panelDataFor(db *sql.DB, def model.RequestDefinition, isNew, authoringEnabled bool) panelData {
	collections, err := store.ListCollections(db)
	if err != nil {
		log.Printf("web: list collections for builder: %v", err)
	}
	// A new endpoint goes to "My endpoints" unless the user picks another. Without a preselection the
	// browser would pick whichever collection sorts first, possibly a built-in one.
	defaultOption := false
	if def.CollectionID == "" {
		defaultOption = true
		for _, c := range collections {
			if c.ID == store.DefaultCollectionID || strings.EqualFold(c.Name, store.DefaultCollectionName) {
				def.CollectionID, defaultOption = c.ID, false
				break
			}
		}
	}
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
		AuthoringEnabled: authoringEnabled, Collections: collections, DefaultOption: defaultOption,
	}
}

func requestPanelHandler(db *sql.DB, tmpl *template.Template, authoringEnabled bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "new" {
			// ?collection= preselects the collection an empty group's add control belongs to.
			def := model.RequestDefinition{Method: "GET", CollectionID: r.URL.Query().Get("collection")}
			renderTemplate(w, tmpl, "endpoint-panel.html", panelDataFor(db, def, true, authoringEnabled))
			return
		}
		def, err := store.GetRequestDefinition(db, id)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		renderTemplate(w, tmpl, "endpoint-panel.html", panelDataFor(db, def, false, authoringEnabled))
	}
}

func importHandler(db *sql.DB, tmpl *template.Template, authoringEnabled bool, guard *scopeGuard) http.HandlerFunc {
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

		// Saving may create "My endpoints"; a delete of it must not land between that and the insert.
		guard.mu.Lock()
		for i := range defs {
			if err := store.SaveRequestDefinition(db, defs[i]); err != nil {
				guard.mu.Unlock()
				http.Error(w, "save failed: "+err.Error(), http.StatusInternalServerError)
				return
			}
		}
		guard.mu.Unlock()

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
			if err := tmpl.ExecuteTemplate(w, "endpoint-panel.html", panelDataFor(db, defs[0], false, authoringEnabled)); err != nil {
				log.Printf("template endpoint-panel.html: %v", err)
				return
			}
			fmt.Fprint(w, `</div>`)
		}
	}
}

func probeHandler(db *sql.DB, tmpl *template.Template) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		def, err := requestDefFromForm(r)
		if err != nil {
			http.Error(w, "invalid request: "+err.Error(), http.StatusBadRequest)
			return
		}
		def, err = store.ResolveURL(db, def)
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
		Notes:       strings.TrimSpace(r.FormValue("notes")),

		CollectionID: strings.TrimSpace(r.FormValue("collection_id")),
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
