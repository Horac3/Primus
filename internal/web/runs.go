package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Horac3/Primus/internal/author"
	"github.com/Horac3/Primus/internal/engine"
	"github.com/Horac3/Primus/internal/insight"
	"github.com/Horac3/Primus/internal/model"
	"github.com/Horac3/Primus/internal/store"
)

type runningData struct {
	RunID      string
	Detail     string // mode, concurrency and the identity the run sends as
	TimeSeries []model.Point
}

func postRunHandler(db *sql.DB, tmpl *template.Template, reg *Registry, guard *scopeGuard) http.HandlerFunc {
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

		// From here until the run is registered, a delete or clear of this endpoint's scope must not
		// slip in: it would either miss the run or leave the endpoint in a deleted collection.
		guard.mu.Lock()
		unlock := sync.OnceFunc(guard.mu.Unlock)
		defer unlock()
		// A form that omits the collection must not move an existing endpoint to "My endpoints".
		// Likewise a form that omits notes must not wipe the endpoint's note.
		_, postedNotes := r.PostForm["notes"]
		if (def.CollectionID == "" || !postedNotes) && def.ID != "" {
			if existing, err := store.GetRequestDefinition(db, def.ID); err == nil {
				if def.CollectionID == "" {
					def.CollectionID = existing.CollectionID
				}
				if !postedNotes {
					def.Notes = existing.Notes
				}
			}
		}
		if def.CollectionID != "" {
			if _, err := store.GetCollection(db, def.CollectionID); err != nil {
				if errors.Is(err, store.ErrNoSuchCollection) {
					http.Error(w, "invalid request: that collection no longer exists", http.StatusBadRequest)
					return
				}
				http.Error(w, "invalid request: "+err.Error(), http.StatusInternalServerError)
				return
			}
		}

		// The run uses the resolved copy; the saved definition keeps the {{base_url}} template.
		resolved, err := store.ResolveURL(db, def)
		if err != nil {
			http.Error(w, "invalid request: "+err.Error(), http.StatusBadRequest)
			return
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
		started, err := startConfiguredRun(r.Context(), db, reg, def, resolved, cfg, true)
		if err != nil {
			writeStartError(w, err)
			return
		}
		// The run is registered, so a delete now sees it. Write the response without holding the guard.
		unlock()

		runID, detail := started.ID, started.Detail
		w.WriteHeader(http.StatusAccepted)
		renderTemplate(w, tmpl, "running.html", runningData{RunID: runID, Detail: detail})
	}
}

// startError carries the HTTP status and message for a run that could not start.
type startError struct {
	Status int
	Msg    string
}

func (e *startError) Error() string { return e.Msg }

func writeStartError(w http.ResponseWriter, err error) {
	var se *startError
	if errors.As(err, &se) {
		http.Error(w, se.Msg, se.Status)
		return
	}
	http.Error(w, err.Error(), http.StatusInternalServerError)
}

// startedRun identifies a run registered in the registry; Handle lets a caller wait for it.
type startedRun struct {
	ID     string
	Detail string
	Handle *runHandle
}

// startConfiguredRun validates cfg against def, resolves auth and the credential pool, optionally
// saves cfg, and registers the run. def is the stored definition (the {{base_url}} template) and
// resolved the copy the run sends. The caller holds guard.mu until this returns, so a delete of the
// scope cannot slip in before the run is registered. Failures are *startError.
func startConfiguredRun(parent context.Context, db *sql.DB, reg *Registry, def, resolved model.RequestDefinition,
	cfg model.TestConfig, saveConfig bool) (startedRun, error) {
	bad := func(format string, a ...any) error {
		return &startError{Status: http.StatusBadRequest, Msg: fmt.Sprintf(format, a...)}
	}
	internal := func(format string, a ...any) error {
		return &startError{Status: http.StatusInternalServerError, Msg: fmt.Sprintf(format, a...)}
	}
	if len(cfg.PayloadPool) > 0 {
		if def.Body == nil {
			return startedRun{}, bad("payload_pool requires a JSON body template")
		}
		if err := author.ValidatePayloads(def.Body.Raw, cfg.PayloadPool, len(cfg.PayloadPool)); err != nil {
			return startedRun{}, bad("invalid payload_pool: %v", err)
		}
	}
	if err := engine.ValidateTestConfig(cfg); err != nil {
		return startedRun{}, bad("invalid config: %v", err)
	}
	// An active credential pool overrides global auth for the run,
	// unless auth_source says not to use one.
	var pool *model.CredentialPool
	var err error
	if cfg.AuthSource.UsesPool() {
		if pool, err = activeRunPool(db); err != nil {
			return startedRun{}, bad("invalid config: %v", err)
		}
	}
	auth, err := store.GetGlobalAuth(db)
	if err != nil {
		return startedRun{}, internal("load auth: %v", err)
	}
	// auth_source may swap the active pool for global auth or no auth.
	auth, pool, err = engine.ResolveAuth(cfg, auth, pool)
	if err != nil {
		return startedRun{}, bad("invalid config: %v", err)
	}
	detail := fmt.Sprintf("%s · concurrency %d", cfg.Mode, cfg.Concurrency)
	switch {
	case pool != nil:
		cfg.CredentialPoolID = pool.ID
		if err := engine.ValidatePool(cfg, *pool); err != nil {
			return startedRun{}, bad("invalid config: %v", err)
		}
		detail += " · logging in as pool " + pool.Name
	case auth.Type == "" || auth.Type == model.AuthTypeNone:
		detail += " · no auth"
	default:
		detail += " · global auth"
	}
	if saveConfig {
		if err := store.SaveTestConfig(db, cfg); err != nil {
			return startedRun{}, internal("save config: %v", err)
		}
	}

	// One id for the registry entry, the poll URL, and the persisted run.
	runID := fmt.Sprintf("run-%d", time.Now().UnixNano())
	h := &runHandle{done: make(chan struct{}), detail: detail, requestID: def.ID}
	reg.startWithHandle(parent, runID, h, func(ctx context.Context, h *runHandle) (model.Run, error) {
		run, err := engine.RunWithPool(ctx, resolved, cfg, auth, pool, &h.snapshot)
		if err != nil {
			return model.Run{}, err
		}
		run.ID = runID
		return run, nil
	})
	return startedRun{ID: runID, Detail: detail, Handle: h}, nil
}

func runStatusHandler(db *sql.DB, tmpl *template.Template, reg *Registry, analyzer insight.Analyzer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		renderStatus(w, r, db, tmpl, reg, analyzer, r.PathValue("id"))
	}
}

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
