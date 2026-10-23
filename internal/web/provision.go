package web

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Horac3/Primus/internal/credential"
	"github.com/Horac3/Primus/internal/engine"
	"github.com/Horac3/Primus/internal/model"
	"github.com/Horac3/Primus/internal/store"
)

// provisionResult is one provisioning job's outcome. rows holds the generated
// secrets: it is unexported so no template can render it, and it is dropped
// once persisted.
type provisionResult struct {
	PoolID   string
	PoolName string
	NewPool  bool
	Summary  *model.LoginSummary
	Stored   int
	SaveErr  string

	seq        int // highest {{seq}} this job attempts; saved as the pool's ProvisionedSeq
	rows       []model.Credential
	maskFields []string
}

// provisionView is what the pool panel shows about a job: running, or its
// summary once done. It carries no credential values.
type provisionView struct {
	JobID   string
	PoolID  string
	Running bool
	Result  *provisionResult
}

// provisionRegistry tracks provisioning jobs and allows one running job per
// pool, so two jobs cannot share {{seq}} numbers or push a pool over its cap
// after their accounts already exist on the target.
type provisionRegistry struct {
	*registry[*provisionResult]
	mu   sync.Mutex
	busy map[string]bool
}

func newProvisionRegistry(db *sql.DB) *provisionRegistry {
	p := &provisionRegistry{busy: map[string]bool{}}
	p.registry = newRegistry(func(res *provisionResult) error {
		defer p.release(res.PoolID)
		return persistProvisioned(db, res)
	})
	return p
}

// acquire marks poolID as having a running job; false if it already has one.
func (p *provisionRegistry) acquire(poolID string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.busy[poolID] {
		return false
	}
	p.busy[poolID] = true
	return true
}

func (p *provisionRegistry) release(poolID string) {
	p.mu.Lock()
	delete(p.busy, poolID)
	p.mu.Unlock()
}

// newJobID returns a unique id: the clock alone can repeat within one tick.
func newJobID(prefix string) string {
	return fmt.Sprintf("%s-%d-%s", prefix, time.Now().UnixNano(), strings.ToLower(rand.Text()[:6]))
}

// persistProvisioned stores a finished job's rows: a new pool, or appended to
// an existing one. It runs on Stop too, so accounts already created on the
// target are not lost. Failures are recorded on the result (never returned,
// so the registry still delivers it) and logged by pool id only.
func persistProvisioned(db *sql.DB, res *provisionResult) error {
	rows, mask := res.rows, res.maskFields
	res.rows = nil
	if len(rows) == 0 {
		return nil
	}
	var err error
	if res.NewPool {
		pool := defaultPool(res.PoolID, res.PoolName, rows)
		pool.MaskedFields = mergeFields(pool.MaskedFields, mask)
		pool.ProvisionedSeq = res.seq
		err = store.SaveCredentialPool(db, pool)
	} else {
		err = store.AppendCredentials(db, res.PoolID, rows, mask, res.seq)
	}
	if err != nil {
		log.Printf("provision: save accounts for pool %s: %v", res.PoolID, err)
		res.SaveErr = "the accounts were created on the target but could not be saved"
		return nil
	}
	res.Stored = len(rows)
	return nil
}

func mergeFields(a, b []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, f := range append(append([]string{}, a...), b...) {
		if !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	sort.Strings(out)
	return out
}

var fieldNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// parseProvisionFields reads "name=template" lines. Templates may only use
// the generated {{seq}} and {{secret}}.
func parseProvisionFields(raw string) (map[string]string, []string, error) {
	fields := map[string]string{}
	var order []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name, tmpl, ok := strings.Cut(line, "=")
		name = strings.TrimSpace(name)
		if !ok || !fieldNamePattern.MatchString(name) {
			return nil, nil, fmt.Errorf("each field line must look like name=template")
		}
		if _, dup := fields[name]; dup {
			return nil, nil, fmt.Errorf("field %q is listed twice", name)
		}
		for _, p := range credential.Placeholders(tmpl) {
			if p != "seq" && p != "secret" {
				return nil, nil, fmt.Errorf("field %q uses {{%s}}; only {{seq}} and {{secret}} are generated", name, p)
			}
		}
		fields[name] = tmpl
		order = append(order, name)
	}
	if len(fields) == 0 {
		return nil, nil, fmt.Errorf("list at least one account field, e.g. email=user{{seq}}@example.test")
	}
	return fields, order, nil
}

// provisionSpecFromForm validates the sign-up form. Nothing is sent to the
// target until it passes. Errors never contain generated values.
func provisionSpecFromForm(r *http.Request) (model.ProvisionSpec, []string, error) {
	var spec model.ProvisionSpec
	count, err := strconv.Atoi(strings.TrimSpace(r.FormValue("count")))
	if err != nil || count < 1 {
		return spec, nil, fmt.Errorf("count must be a positive whole number")
	}
	if count > model.MaxCredentialPoolRows {
		return spec, nil, fmt.Errorf("count exceeds the %d-account limit", model.MaxCredentialPoolRows)
	}
	spec.Count = count

	fields, order, err := parseProvisionFields(r.FormValue("fields"))
	if err != nil {
		return spec, nil, err
	}
	spec.Fields = fields

	spec.Signup.Method = strings.ToUpper(strings.TrimSpace(r.FormValue("signup_method")))
	if spec.Signup.Method == "" {
		spec.Signup.Method = http.MethodPost
	}
	spec.Signup.URL = strings.TrimSpace(r.FormValue("signup_url"))
	if spec.Signup.URL == "" {
		return spec, nil, fmt.Errorf("enter the sign-up URL")
	}
	spec.Signup.Body = r.FormValue("signup_body")
	if raw := strings.TrimSpace(r.FormValue("signup_headers")); raw != "" && raw != "null" {
		if err := json.Unmarshal([]byte(raw), &spec.Signup.Headers); err != nil {
			return spec, nil, fmt.Errorf("sign-up headers must be a JSON object with string values")
		}
	}
	templates := []string{spec.Signup.URL, spec.Signup.Body}
	for _, v := range spec.Signup.Headers {
		templates = append(templates, v)
	}
	for _, tmpl := range templates {
		for _, p := range credential.Placeholders(tmpl) {
			if _, ok := fields[p]; !ok {
				return spec, nil, fmt.Errorf("the sign-up request uses {{%s}}, which is not one of the account fields", p)
			}
		}
	}

	spec.ExtractField = strings.TrimSpace(r.FormValue("extract_field"))
	if spec.ExtractField != "" {
		if !fieldNamePattern.MatchString(spec.ExtractField) {
			return spec, nil, fmt.Errorf("the saved field name may only use letters, digits, _, - and .")
		}
		if _, clash := fields[spec.ExtractField]; clash {
			return spec, nil, fmt.Errorf("%q is already an account field; save the response value under another name", spec.ExtractField)
		}
		spec.Signup.Extraction = model.Extraction{
			From:   model.ExtractFrom(strings.TrimSpace(r.FormValue("extract_from"))),
			Path:   strings.TrimSpace(r.FormValue("extract_path")),
			Prefix: r.FormValue("extract_prefix"),
		}
		if spec.Signup.Extraction.Path == "" {
			return spec, nil, fmt.Errorf("enter where to read %q in the response", spec.ExtractField)
		}
		if f := spec.Signup.Extraction.From; f != model.ExtractFromBody && f != model.ExtractFromHeader {
			return spec, nil, fmt.Errorf("extract from must be the body or a header")
		}
	}

	spec.Concurrency = model.DefaultLoginConcurrency
	if raw := strings.TrimSpace(r.FormValue("concurrency")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			return spec, nil, fmt.Errorf("concurrency must be a positive whole number")
		}
		spec.Concurrency = n
	}

	// Every field built from {{secret}} is masked, plus the usual name rule.
	mask := credential.DefaultMaskedFields(order)
	for name, tmpl := range fields {
		if slices.Contains(credential.Placeholders(tmpl), "secret") {
			mask = append(mask, name)
		}
	}
	if spec.ExtractField != "" {
		mask = append(mask, credential.DefaultMaskedFields([]string{spec.ExtractField})...)
	}
	return spec, mergeFields(nil, mask), nil
}

// ---- POST /pools/provision ---------------------------------------------------

func poolProvisionHandler(db *sql.DB, tmpl *template.Template, reg *provisionRegistry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		spec, mask, err := provisionSpecFromForm(r)
		if err != nil {
			http.Error(w, "provisioning: "+err.Error(), http.StatusBadRequest)
			return
		}

		res := &provisionResult{maskFields: mask}
		existing := strings.TrimSpace(r.FormValue("existing_pool_id"))
		startSeq := 1
		if existing != "" {
			// Claim the pool before reading it, so the row count and seq
			// counter cannot change under this job.
			if !reg.acquire(existing) {
				http.Error(w, "provisioning: another job is already adding accounts to this pool; wait for it or stop it",
					http.StatusConflict)
				return
			}
			pool, err := store.GetCredentialPoolMasked(db, existing)
			if err != nil {
				reg.release(existing)
				http.Error(w, "provisioning: choose an existing pool or enter a name for a new one", http.StatusBadRequest)
				return
			}
			if len(pool.Credentials)+spec.Count > model.MaxCredentialPoolRows {
				reg.release(existing)
				http.Error(w, fmt.Sprintf("provisioning: pool %q has %d accounts; at most %d more fit in the %d-account limit",
					pool.Name, len(pool.Credentials), model.MaxCredentialPoolRows-len(pool.Credentials), model.MaxCredentialPoolRows),
					http.StatusBadRequest)
				return
			}
			limit := pool.LoginConcurrency
			if limit <= 0 {
				limit = model.DefaultLoginConcurrency
			}
			if spec.Concurrency > limit {
				reg.release(existing)
				http.Error(w, fmt.Sprintf("provisioning: concurrency can be at most %d (this pool's login concurrency)", limit),
					http.StatusBadRequest)
				return
			}
			res.PoolID, res.PoolName = pool.ID, pool.Name
			startSeq = max(pool.ProvisionedSeq, len(pool.Credentials)) + 1
		} else {
			name := strings.TrimSpace(r.FormValue("name"))
			if name == "" {
				http.Error(w, "provisioning: choose an existing pool or enter a name for a new one", http.StatusBadRequest)
				return
			}
			if spec.Concurrency > model.DefaultLoginConcurrency {
				http.Error(w, fmt.Sprintf("provisioning: concurrency can be at most %d", model.DefaultLoginConcurrency),
					http.StatusBadRequest)
				return
			}
			res.NewPool = true
			res.PoolID, res.PoolName = newJobID("pool"), name
			reg.acquire(res.PoolID)
		}
		res.seq = startSeq - 1 + spec.Count

		jobID := newJobID("prov")
		reg.Start(r.Context(), jobID, func(ctx context.Context) (*provisionResult, error) {
			rows, summary := engine.Provision(ctx, spec, startSeq)
			summary.PoolID, summary.PoolName = res.PoolID, res.PoolName
			res.rows, res.Summary = rows, summary
			return res, nil
		})

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusAccepted)
		renderProvisionPanel(w, db, tmpl, res.PoolID, &provisionView{JobID: jobID, PoolID: res.PoolID, Running: true})
	}
}

// renderProvisionPanel renders the pool panel with the job's state. A pool
// that does not exist yet (nothing created so far) is simply not selected.
func renderProvisionPanel(w http.ResponseWriter, db *sql.DB, tmpl *template.Template, poolID string, view *provisionView) {
	renderPoolPanel(w, db, tmpl, poolID, poolPanelData{Provision: view})
}

// ---- GET /pools/provision/{id}/status ------------------------------------------

func poolProvisionStatusHandler(db *sql.DB, tmpl *template.Template, reg *provisionRegistry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		jobID, poolID := r.PathValue("id"), r.URL.Query().Get("pool")
		status, res, ok := reg.Poll(jobID)
		switch {
		case !ok:
			renderPoolPanel(w, db, tmpl, poolID, poolPanelData{Message: "That provisioning job is no longer tracked."})
		case status == "running":
			renderProvisionPanel(w, db, tmpl, poolID, &provisionView{JobID: jobID, PoolID: poolID, Running: true})
		default:
			view := &provisionView{JobID: jobID, PoolID: poolID}
			if res != nil {
				view.Result = *res
			}
			renderProvisionPanel(w, db, tmpl, poolID, view)
		}
	}
}

// ---- POST /pools/provision/{id}/stop -------------------------------------------

func poolProvisionStopHandler(db *sql.DB, tmpl *template.Template, reg *provisionRegistry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		jobID, poolID := r.PathValue("id"), r.URL.Query().Get("pool")
		if !reg.Stop(jobID) {
			renderPoolPanel(w, db, tmpl, poolID, poolPanelData{Message: "That provisioning job already finished."})
			return
		}
		renderProvisionPanel(w, db, tmpl, poolID, &provisionView{JobID: jobID, PoolID: poolID, Running: true})
	}
}
