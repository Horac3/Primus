package model

import (
	"encoding/json"
	"time"
)

// AuthType identifies which authentication scheme a request uses.
type AuthType string

const (
	AuthTypeNone         AuthType = "none"
	AuthTypeBearer       AuthType = "bearer"
	AuthTypeAPIKey       AuthType = "api_key"
	AuthTypeBasic        AuthType = "basic"
	AuthTypeLoginChained AuthType = "login_chained"
	// AuthTypeCredentialPool carries one worker's extracted pool token as a
	// ready-to-send header (Header: Value). It is built per worker at run
	// time and never persisted as the global auth.
	AuthTypeCredentialPool AuthType = "credential_pool"
)

// AuthConfig holds the single global authentication configuration.
// It is stored once (store.GetGlobalAuth/SaveGlobalAuth) and applied to
// every dispatched request — it is not a property of an endpoint.
type AuthConfig struct {
	Type     AuthType `json:"type"`
	Token    string   `json:"token,omitempty"`
	Header   string   `json:"header,omitempty"` // e.g. "X-API-Key"
	Value    string   `json:"value,omitempty"`  // api-key value
	Username string   `json:"username,omitempty"`
	Password string   `json:"password,omitempty"`
	// LoginChained: reference to another RequestDefinition ID whose
	// response supplies the bearer token for this request.
	LoginRequestID string `json:"login_request_id,omitempty"`
	TokenJSONPath  string `json:"token_json_path,omitempty"`
}

// BodyTemplate is a JSON-serializable raw request body template.
type BodyTemplate struct {
	Raw []byte `json:"raw"`
}

// DynamicKind describes how a dynamic parameter value is generated.
type DynamicKind string

const (
	DynamicKindRandomInt    DynamicKind = "random_int"
	DynamicKindRandomString DynamicKind = "random_string"
	DynamicKindSequential   DynamicKind = "sequential"
)

// DynamicValue describes a generated parameter value.
type DynamicValue struct {
	Kind      DynamicKind `json:"kind"`
	IntMin    int         `json:"int_min,omitempty"`
	IntMax    int         `json:"int_max,omitempty"`
	StringLen int         `json:"string_len,omitempty"`
	SeqStart  int         `json:"seq_start,omitempty"`
}

// ParamSpec is either a static string or a dynamically generated value.
// Exactly one of Static or Dynamic should be non-nil.
// EnumValues captures any enum constraint from the source spec so a UI
// can render a select; it does not restrict runtime values.
type ParamSpec struct {
	Static     *string       `json:"static,omitempty"`
	Dynamic    *DynamicValue `json:"dynamic,omitempty"`
	EnumValues []string      `json:"enum_values,omitempty"`
}

// HeaderGlobalSentinel marks a per-endpoint header row as "use the global
// auth value". Any other stored value overrides global auth injection.
const HeaderGlobalSentinel = "[GLOBAL]"

// RequestDefinition is a reusable template for an HTTP request.
// Authentication is global (see AuthConfig) and intentionally not stored here.
type RequestDefinition struct {
	ID          string               `json:"id"`
	Name        string               `json:"name"`
	Method      string               `json:"method"`
	URLTemplate string               `json:"url_template"`
	Headers     map[string]string    `json:"headers,omitempty"`
	QueryParams map[string]ParamSpec `json:"query_params,omitempty"`
	Body        *BodyTemplate        `json:"body,omitempty"`
}

// Mode is the type of test to run.
type Mode string

const (
	ModeLoad      Mode = "load"
	ModeBurst     Mode = "burst"
	ModeAuthProbe Mode = "auth_probe"
	ModeSoak      Mode = "soak"
)

// BurstPattern describes the shape of a burst test.
type BurstPattern struct {
	Count int `json:"count"` // requests per burst
	Idle  int `json:"idle"`  // milliseconds idle between bursts
}

// Assertions defines what a passing run must satisfy.
type Assertions struct {
	ExpectedStatus []int   `json:"expected_status,omitempty"`
	MaxP95Millis   int     `json:"max_p95_millis,omitempty"`
	MaxErrorRate   float64 `json:"max_error_rate,omitempty"`
}

// TestConfig is a parameterised test to execute against a RequestDefinition.
// Duration is used only when Mode == "soak"; load/burst use TotalRequests.
type TestConfig struct {
	ID            string            `json:"id"`
	RequestID     string            `json:"request_id"`
	Mode          Mode              `json:"mode"`
	Concurrency   int               `json:"concurrency"`
	TotalRequests int               `json:"total_requests"`
	Duration      time.Duration     `json:"duration,omitempty"`
	BurstPattern  *BurstPattern     `json:"burst_pattern,omitempty"`
	Assertions    Assertions        `json:"assertions"`
	PayloadPool   []json.RawMessage `json:"payload_pool,omitempty"`
	// CredentialPoolID selects a credential pool whose per-worker logins
	// replace global auth for this run. Empty means global auth applies.
	CredentialPoolID string `json:"credential_pool_id,omitempty"`
}

// MaxCredentialPoolRows caps how many credentials one pool may hold.
const MaxCredentialPoolRows = 10000

// Credential is one pre-existing account on the target: arbitrary named
// fields (username, password, email, client_id, ...) filled into the
// login template's {{field}} placeholders.
type Credential map[string]string

// ExtractFrom says where an Extraction reads its value.
type ExtractFrom string

const (
	ExtractFromBody   ExtractFrom = "body"
	ExtractFromHeader ExtractFrom = "header"
)

// Extraction pulls one value out of a response: a dot path into a JSON
// body (data.access_token) or a header name (X-Auth-Token). Prefix, when
// set, is stripped from the start of the value (for example "Bearer ").
type Extraction struct {
	From   ExtractFrom `json:"from"`
	Path   string      `json:"path"`
	Prefix string      `json:"prefix,omitempty"`
}

// LoginSpec is the request template a worker sends once to log in as its
// credential. URL, header values and body may contain {{field}} placeholders.
type LoginSpec struct {
	Method     string            `json:"method"`
	URL        string            `json:"url"`
	Headers    map[string]string `json:"headers,omitempty"`
	Body       string            `json:"body,omitempty"`
	Extraction Extraction        `json:"extraction"`
}

// ApplySpec says how the extracted token is sent on each request: header
// Header with value Format, where {{token}} is the extracted value.
type ApplySpec struct {
	Header string `json:"header"`
	Format string `json:"format"`
}

// Default apply step: Authorization: Bearer {{token}}.
const (
	DefaultApplyHeader      = "Authorization"
	DefaultApplyFormat      = "Bearer {{token}}"
	DefaultLoginConcurrency = 20
)

// CredentialPool is a named set of pre-existing accounts plus how to log
// each one in and how to send the resulting token. Primus never creates
// accounts on the target.
type CredentialPool struct {
	ID               string       `json:"id"`
	Name             string       `json:"name"`
	Credentials      []Credential `json:"credentials,omitempty"`
	Login            LoginSpec    `json:"login"`
	Apply            ApplySpec    `json:"apply"`
	AllowReuse       bool         `json:"allow_reuse,omitempty"`
	LoginConcurrency int          `json:"login_concurrency,omitempty"`
	ReloginOn401     bool         `json:"relogin_on_401,omitempty"`
}

// LoginOutcome is one worker's login attempt. It carries no secrets: the
// credential is identified by its row index only.
type LoginOutcome struct {
	Index      int           `json:"index"`
	Success    bool          `json:"success"`
	StatusCode int           `json:"status_code,omitempty"`
	Latency    time.Duration `json:"latency"`
	ErrorClass string        `json:"error_class,omitempty"`
}

// LoginSummary is the login phase's report attached to a run. Login latency
// is measured here and never mixed into the request latency stats.
type LoginSummary struct {
	PoolID            string         `json:"pool_id"`
	PoolName          string         `json:"pool_name"`
	Attempted         int            `json:"attempted"`
	Succeeded         int            `json:"succeeded"`
	Failed            int            `json:"failed"`
	P50Millis         int            `json:"p50_millis"`
	P95Millis         int            `json:"p95_millis"`
	FailureReasons    []ErrorDetail  `json:"failure_reasons,omitempty"`
	Relogins          int            `json:"relogins,omitempty"`
	ReloginsSucceeded int            `json:"relogins_succeeded,omitempty"`
	Outcomes          []LoginOutcome `json:"outcomes,omitempty"`
}

// RunSummary holds aggregate metrics for a completed run.
type ErrorDetail struct {
	Cause string `json:"cause"`
	Count int    `json:"count"`
}

type RunSummary struct {
	StatusCounts map[int]int   `json:"status_counts,omitempty"`
	ErrorCount   int           `json:"error_count"`
	ErrorDetails []ErrorDetail `json:"error_details,omitempty"`
	P50Millis    int           `json:"p50_millis"`
	P95Millis    int           `json:"p95_millis"`
	P99Millis    int           `json:"p99_millis"`
	ErrorRate    float64       `json:"error_rate"`
	// TotalRequests is the actual number of fired requests (completed
	// responses + transport errors). Basis for requests/sec.
	TotalRequests int `json:"total_requests"`
}

// RunOutcome is the tri-state result of a Run.
type RunOutcome string

const (
	// OutcomePass — all configured assertions met.
	OutcomePass RunOutcome = "pass"
	// OutcomeFail — at least one configured assertion failed.
	OutcomeFail RunOutcome = "fail"
	// OutcomeFailNoRequests — no request completed (nothing dispatched, or every
	// request was a transport error); applies even when no assertions are configured.
	OutcomeFailNoRequests RunOutcome = "fail_no_requests"
	// OutcomeNoAssertions — run completed with ≥1 success but no assertions were configured,
	// so pass/fail is indeterminate (neutral).
	OutcomeNoAssertions RunOutcome = "no_assertions"
	// OutcomeFailNoLogins — a credential-pool run where every login failed,
	// so nothing was dispatched (never run unauthenticated by accident).
	OutcomeFailNoLogins RunOutcome = "fail_no_logins"
)

// AssertionResult is one configured assertion and whether it held.
// The UI renders one checklist row per entry; overall Run.Passed is true
// only if every entry passed (plus the zero-success baseline rule).
type AssertionResult struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
}

// LatencyBucketEdges are the fixed upper-bound edges (ms) for latency
// histograms. LatencyBuckets has len(edges)+1 entries: one per edge plus
// a final >= last-edge overflow bucket.
var LatencyBucketEdges = []int{50, 100, 150, 200, 300, 400, 500, 600}

// Point captures one time-slice in a live or finished run timeline.
type Point struct {
	ElapsedMs    int64   `json:"elapsed_ms"`
	AvgLatencyMs float64 `json:"avg_latency_ms"`
	RPS          float64 `json:"rps"`
	ErrorRate    float64 `json:"error_rate"`
}

// Run is a single execution of a TestConfig.
type Run struct {
	ID               string            `json:"id"`
	TestConfigID     string            `json:"test_config_id"`
	Mode             Mode              `json:"mode,omitempty"`
	Concurrency      int               `json:"concurrency,omitempty"`
	StartedAt        time.Time         `json:"started_at"`
	FinishedAt       time.Time         `json:"finished_at"`
	Summary          RunSummary        `json:"summary"`
	Outcome          RunOutcome        `json:"outcome"`
	Passed           bool              `json:"passed"`
	AssertionResults []AssertionResult `json:"assertion_results,omitempty"`
	// Stopped is true when the run was cancelled (Stop button or context
	// cancel) before reaching its own stop condition.
	Stopped        bool          `json:"stopped,omitempty"`
	LatencyBuckets []int         `json:"latency_buckets,omitempty"`
	TimeSeries     []Point       `json:"time_series,omitempty"`
	Login          *LoginSummary `json:"login,omitempty"`
}
