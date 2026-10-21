package web

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Horac3/Primus/internal/credential"
	"github.com/Horac3/Primus/internal/engine"
	"github.com/Horac3/Primus/internal/model"
	"github.com/Horac3/Primus/internal/store"
)

const (
	// maxPoolUploadBytes bounds a credential import (10,000 rows fit easily).
	maxPoolUploadBytes = 16 << 20
	// poolPreviewRows is how many (masked) credentials the panel lists.
	poolPreviewRows = 20
)

// ---- auth bar data ---------------------------------------------------------

// loadAuthBar gathers the auth bar: global auth plus the credential pools
// and which one (if any) overrides global auth for runs.
func loadAuthBar(db *sql.DB) (authBarData, error) {
	auth, err := store.GetGlobalAuth(db)
	if err != nil {
		return authBarData{}, fmt.Errorf("load auth: %w", err)
	}
	pools, err := store.ListCredentialPools(db)
	if err != nil {
		return authBarData{}, fmt.Errorf("list credential pools: %w", err)
	}
	activeID, err := store.GetActiveCredentialPool(db)
	if err != nil {
		return authBarData{}, err
	}
	data := authBarData{Auth: auth, Pools: pools}
	for i := range pools {
		if pools[i].ID == activeID {
			data.ActivePool = &pools[i]
		}
	}
	return data, nil
}

// ---- pool panel ------------------------------------------------------------

type importReport struct {
	Count    int
	Errors   []credential.RowIssue
	Warnings []credential.RowIssue
}

type poolPanelData struct {
	Pools       []store.PoolSummary
	ActiveID    string
	Selected    *model.CredentialPool // masked
	Preview     []model.Credential    // masked, at most poolPreviewRows
	Fields      []string
	Masked      map[string]bool // fields the pool masks, for the checkboxes
	HeadersJSON string
	Import      *importReport
	Message     string
	Provision   *provisionView
}

// renderPoolPanel renders the pool manager with selectedID (falling back
// to the active pool, then the first pool) shown with masked credentials.
func renderPoolPanel(w http.ResponseWriter, db *sql.DB, tmpl *template.Template, selectedID string, data poolPanelData) {
	pools, err := store.ListCredentialPools(db)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	activeID, err := store.GetActiveCredentialPool(db)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data.Pools, data.ActiveID = pools, activeID
	if selectedID == "" {
		selectedID = activeID
	}
	if selectedID == "" && len(pools) > 0 {
		selectedID = pools[0].ID
	}
	if selectedID != "" {
		pool, err := store.GetCredentialPoolMasked(db, selectedID)
		if err == nil {
			data.Selected = &pool
			data.Preview = pool.Credentials
			if len(data.Preview) > poolPreviewRows {
				data.Preview = data.Preview[:poolPreviewRows]
			}
			data.Fields = credentialFields(pool.Credentials)
			masked := store.EffectiveMaskedFields(pool)
			data.Masked = make(map[string]bool, len(masked))
			for _, f := range masked {
				data.Masked[f] = true
			}
			headers, _ := json.Marshal(pool.Login.Headers)
			data.HeadersJSON = string(headers)
		} else if !errors.Is(err, sql.ErrNoRows) {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	renderTemplate(w, tmpl, "pools-panel.html", data)
}

// credentialFields lists the field names across the pool, identity
// fields first, then alphabetically.
func credentialFields(creds []model.Credential) []string {
	seen := map[string]bool{}
	var names []string
	for _, c := range creds {
		for name := range c {
			if !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
	}
	rank := func(n string) int {
		switch n {
		case "username":
			return 0
		case "email":
			return 1
		}
		return 2
	}
	sort.Slice(names, func(i, j int) bool {
		if rank(names[i]) != rank(names[j]) {
			return rank(names[i]) < rank(names[j])
		}
		return names[i] < names[j]
	})
	return names
}

func poolsFragmentHandler(db *sql.DB, tmpl *template.Template) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		renderPoolPanel(w, db, tmpl, r.URL.Query().Get("id"), poolPanelData{})
	}
}

// ---- POST /pools/import ------------------------------------------------------

func poolImportHandler(db *sql.DB, tmpl *template.Template) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxPoolUploadBytes)
		content, err := poolUploadContent(r)
		if err != nil {
			http.Error(w, "import failed: "+err.Error(), http.StatusBadRequest)
			return
		}
		if len(bytes.TrimSpace(content)) == 0 {
			http.Error(w, "import failed: upload a CSV or JSON file, or paste its content", http.StatusBadRequest)
			return
		}
		var res credential.ImportResult
		if isJSONImport(r.FormValue("format"), content) {
			res, err = credential.ImportJSON(bytes.NewReader(content))
		} else {
			res, err = credential.ImportCSV(bytes.NewReader(content))
		}
		if err != nil {
			http.Error(w, "import failed: "+err.Error(), http.StatusBadRequest)
			return
		}
		if len(res.Credentials) == 0 {
			http.Error(w, fmt.Sprintf("import failed: no valid rows (%d rejected)", len(res.Errors)), http.StatusBadRequest)
			return
		}
		name := strings.TrimSpace(r.FormValue("name"))
		if name == "" {
			name = "Credential pool"
		}
		pool := defaultPool(fmt.Sprintf("pool-%d", time.Now().UnixNano()), name, res.Credentials)
		if err := store.SaveCredentialPool(db, pool); err != nil {
			http.Error(w, "save pool: "+err.Error(), http.StatusInternalServerError)
			return
		}
		renderPoolPanel(w, db, tmpl, pool.ID, poolPanelData{Import: &importReport{
			Count: len(res.Credentials), Errors: res.Errors, Warnings: res.Warnings,
		}})
	}
}

// poolUploadContent reads the uploaded file, or the pasted content field.
func poolUploadContent(r *http.Request) ([]byte, error) {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		if err := r.ParseMultipartForm(maxPoolUploadBytes); err != nil {
			return nil, fmt.Errorf("read upload: %w", err)
		}
		if file, _, err := r.FormFile("file"); err == nil {
			defer file.Close()
			return io.ReadAll(file)
		}
	}
	return []byte(r.FormValue("content")), nil
}

func isJSONImport(format string, content []byte) bool {
	switch strings.ToLower(format) {
	case "json":
		return true
	case "csv":
		return false
	}
	trimmed := bytes.TrimSpace(content)
	return len(trimmed) > 0 && trimmed[0] == '['
}

// defaultPool is a new pool with a JSON login body built from the
// imported field names, ready for the user to point at their login URL.
func defaultPool(id, name string, creds []model.Credential) model.CredentialPool {
	fields := credentialFields(creds)
	body := make(map[string]string, len(fields))
	for _, f := range fields {
		body[f] = "{{" + f + "}}"
	}
	raw, _ := json.Marshal(body)
	masked := credential.DefaultMaskedFields(fields)
	if masked == nil {
		masked = []string{}
	}
	return model.CredentialPool{
		ID:           id,
		Name:         name,
		Credentials:  creds,
		MaskedFields: masked,
		Login: model.LoginSpec{
			Method:     http.MethodPost,
			Headers:    map[string]string{"Content-Type": "application/json"},
			Body:       string(raw),
			Extraction: model.Extraction{From: model.ExtractFromBody, Path: "token"},
		},
		Apply:            model.ApplySpec{Header: model.DefaultApplyHeader, Format: model.DefaultApplyFormat},
		LoginConcurrency: model.DefaultLoginConcurrency,
	}
}

// ---- POST /pools/{id}/spec ---------------------------------------------------

func poolSpecHandler(db *sql.DB, tmpl *template.Template) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		pool, err := store.GetCredentialPoolWithSecrets(db, id)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		if err := applySpecForm(r, &pool); err != nil {
			http.Error(w, "invalid login spec: "+err.Error(), http.StatusBadRequest)
			return
		}
		// Concurrency 1: pool size is checked against each run's concurrency.
		if err := engine.ValidatePool(model.TestConfig{Concurrency: 1}, pool); err != nil {
			http.Error(w, "invalid login spec: "+err.Error(), http.StatusBadRequest)
			return
		}
		if err := store.SaveCredentialPoolSpec(db, pool); err != nil {
			http.Error(w, "save pool: "+err.Error(), http.StatusInternalServerError)
			return
		}
		renderPoolPanel(w, db, tmpl, id, poolPanelData{Message: "Login settings saved."})
	}
}

// ---- POST /pools/{id}/masking ------------------------------------------------

// poolMaskingHandler stores which fields the pool masks: the ticked "mask"
// boxes that name a field of the pool. Unknown names are ignored.
func poolMaskingHandler(db *sql.DB, tmpl *template.Template) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		pool, err := store.GetCredentialPoolWithSecrets(db, id)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid masking form", http.StatusBadRequest)
			return
		}
		fields := credentialFields(pool.Credentials)
		pool.MaskedFields = []string{}
		for _, f := range fields {
			if slices.Contains(r.PostForm["mask"], f) {
				pool.MaskedFields = append(pool.MaskedFields, f)
			}
		}
		sort.Strings(pool.MaskedFields)
		if err := store.SaveCredentialPoolSpec(db, pool); err != nil {
			http.Error(w, "save pool: "+err.Error(), http.StatusInternalServerError)
			return
		}
		renderPoolPanel(w, db, tmpl, id, poolPanelData{Message: "Masked fields saved."})
	}
}

func applySpecForm(r *http.Request, pool *model.CredentialPool) error {
	if name := strings.TrimSpace(r.FormValue("name")); name != "" {
		pool.Name = name
	}
	pool.Login.Method = strings.ToUpper(strings.TrimSpace(r.FormValue("login_method")))
	if pool.Login.Method == "" {
		pool.Login.Method = http.MethodPost
	}
	pool.Login.URL = strings.TrimSpace(r.FormValue("login_url"))
	pool.Login.Body = r.FormValue("login_body")
	pool.Login.Headers = nil
	if raw := strings.TrimSpace(r.FormValue("login_headers_json")); raw != "" && raw != "null" {
		if err := json.Unmarshal([]byte(raw), &pool.Login.Headers); err != nil {
			return fmt.Errorf("login headers must be a JSON object with string values")
		}
	}
	pool.Login.Extraction = model.Extraction{
		From:   model.ExtractFrom(strings.TrimSpace(r.FormValue("extract_from"))),
		Path:   strings.TrimSpace(r.FormValue("extract_path")),
		Prefix: r.FormValue("extract_prefix"),
	}
	pool.Apply = model.ApplySpec{
		Header: strings.TrimSpace(r.FormValue("apply_header")),
		Format: strings.TrimSpace(r.FormValue("apply_format")),
	}
	pool.LoginConcurrency = model.DefaultLoginConcurrency
	if raw := strings.TrimSpace(r.FormValue("login_concurrency")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			return fmt.Errorf("login concurrency must be a positive integer")
		}
		pool.LoginConcurrency = n
	}
	pool.AllowReuse = r.FormValue("allow_reuse") != ""
	pool.ReloginOn401 = r.FormValue("relogin_on_401") != ""
	return nil
}

// ---- POST /pools/{id}/test-login -------------------------------------------

type testLoginData struct {
	Outcome     model.LoginOutcome
	LatencyMs   int64
	MaskedToken string
}

func poolTestLoginHandler(db *sql.DB, tmpl *template.Template) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pool, err := store.GetCredentialPoolWithSecrets(db, r.PathValue("id"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		index, err := strconv.Atoi(strings.TrimSpace(r.FormValue("index")))
		if err != nil || index < 0 || index >= len(pool.Credentials) {
			http.Error(w, "index must select a credential in the pool", http.StatusBadRequest)
			return
		}
		if err := engine.ValidatePool(model.TestConfig{Concurrency: 1}, pool); err != nil {
			http.Error(w, "invalid login spec: "+err.Error(), http.StatusBadRequest)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		outcome, token := engine.Login(ctx, pool, index)
		renderTemplate(w, tmpl, "pool-test-login", testLoginData{
			Outcome: outcome, LatencyMs: outcome.Latency.Milliseconds(), MaskedToken: maskToken(token),
		})
	}
}

// maskToken shows only enough of a token to recognise it.
func maskToken(token string) string {
	if token == "" {
		return ""
	}
	if len(token) <= 8 {
		return "••••"
	}
	return "••••" + last4(token)
}

// ---- POST /pools/active ------------------------------------------------------

func poolActiveHandler(db *sql.DB, tmpl *template.Template) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.FormValue("pool_id"))
		if id != "" {
			if _, err := store.GetCredentialPoolMasked(db, id); err != nil {
				http.Error(w, "unknown credential pool", http.StatusBadRequest)
				return
			}
		}
		if err := store.SetActiveCredentialPool(db, id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
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

// activeRunPool loads the active pool (with secrets) for a run, or nil
// when runs use global auth.
func activeRunPool(db *sql.DB) (*model.CredentialPool, error) {
	id, err := store.GetActiveCredentialPool(db)
	if err != nil || id == "" {
		return nil, err
	}
	pool, err := store.GetCredentialPoolWithSecrets(db, id)
	if err != nil {
		return nil, fmt.Errorf("active credential pool is missing; pick another in the auth bar")
	}
	return &pool, nil
}
