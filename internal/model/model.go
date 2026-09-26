package model

import "time"

// AuthType identifies which authentication scheme a request uses.
type AuthType string

const (
	AuthTypeNone         AuthType = "none"
	AuthTypeBearer       AuthType = "bearer"
	AuthTypeAPIKey       AuthType = "api_key"
	AuthTypeLoginChained AuthType = "login_chained"
)

// AuthConfig holds authentication parameters for a request.
type AuthConfig struct {
	Type   AuthType `json:"type"`
	Token  string   `json:"token,omitempty"`
	Header string   `json:"header,omitempty"` // e.g. "X-API-Key"
	Value  string   `json:"value,omitempty"`  // api-key value
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
	Kind       DynamicKind `json:"kind"`
	IntMin     int         `json:"int_min,omitempty"`
	IntMax     int         `json:"int_max,omitempty"`
	StringLen  int         `json:"string_len,omitempty"`
	SeqStart   int         `json:"seq_start,omitempty"`
}

// ParamSpec is either a static string or a dynamically generated value.
// Exactly one of Static or Dynamic should be non-nil.
type ParamSpec struct {
	Static  *string       `json:"static,omitempty"`
	Dynamic *DynamicValue `json:"dynamic,omitempty"`
}

// RequestDefinition is a reusable template for an HTTP request.
type RequestDefinition struct {
	ID          string               `json:"id"`
	Name        string               `json:"name"`
	Method      string               `json:"method"`
	URLTemplate string               `json:"url_template"`
	Headers     map[string]string    `json:"headers,omitempty"`
	QueryParams map[string]ParamSpec `json:"query_params,omitempty"`
	Body        *BodyTemplate        `json:"body,omitempty"`
	Auth        *AuthConfig          `json:"auth,omitempty"`
}

// Mode is the type of test to run.
type Mode string

const (
	ModeLoad      Mode = "load"
	ModeBurst     Mode = "burst"
	ModeAuthProbe Mode = "auth_probe"
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
type TestConfig struct {
	ID            string        `json:"id"`
	RequestID     string        `json:"request_id"`
	Mode          Mode          `json:"mode"`
	Concurrency   int           `json:"concurrency"`
	TotalRequests int           `json:"total_requests"`
	BurstPattern  *BurstPattern `json:"burst_pattern,omitempty"`
	Assertions    Assertions    `json:"assertions"`
}

// RunSummary holds aggregate metrics for a completed run.
type RunSummary struct {
	StatusCounts map[int]int `json:"status_counts,omitempty"`
	P50Millis    int         `json:"p50_millis"`
	P95Millis    int         `json:"p95_millis"`
	P99Millis    int         `json:"p99_millis"`
	ErrorRate    float64     `json:"error_rate"`
}

// Run is a single execution of a TestConfig.
type Run struct {
	ID           string     `json:"id"`
	TestConfigID string     `json:"test_config_id"`
	StartedAt    time.Time  `json:"started_at"`
	FinishedAt   time.Time  `json:"finished_at"`
	Summary      RunSummary `json:"summary"`
	Passed       bool       `json:"passed"`
}
