package insight

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/template"
	"time"
)

type Insight struct {
	Text         string    `json:"text,omitempty"`
	Sentences    []string  `json:"sentences,omitempty"`
	Headline     string    `json:"headline,omitempty"`
	Explanation  string    `json:"explanation,omitempty"`
	NextSteps    []string  `json:"next_steps,omitempty"`
	Sections     Sections  `json:"sections"`
	AnalyzerName string    `json:"analyzer_name"`
	ModelName    string    `json:"model_name"`
	CreatedAt    time.Time `json:"created_at"`
}

// Sections is the fixed shape every insight takes, whichever analyzer wrote it (FR-INS-01).
// Comparison is empty unless the endpoint has an earlier run (FR-INS-03).
type Sections struct {
	Numbers      []string `json:"numbers"`
	Causes       []string `json:"causes"`
	NextSteps    []string `json:"next_steps"`
	Comparison   []string `json:"comparison,omitempty"`
	FallbackFrom string   `json:"fallback_from,omitempty"`
}

type Analyzer interface {
	Name() string
	Analyze(ctx context.Context, s Summary, f []Finding) (Insight, error)
}

type Config struct {
	Analyzer   string
	LLMURL     string
	LLMModel   string
	LLMTimeout time.Duration
}

type RulesAnalyzer struct{}

var sentenceTemplates = map[string]string{
	"CLIENT_TIMEOUT":               "All {{.error_count}} failed requests ended because the client deadline expired.",
	"SATURATION_FROM_START":        "Latency was already elevated in the first time bucket ({{printf \"%.0f\" .first_bucket_latency_ms}} ms) and stayed nearly flat.",
	"CONCURRENCY_EXCEEDS_CAPACITY": "Estimated about {{printf \"%.1f\" .estimated_in_flight}} requests in flight, above configured concurrency of {{.concurrency}} workers.",
	"NO_ASSERTIONS":                "No assertions were configured, so this run has no pass/fail criteria.",
	"PARTIAL_LAST_BUCKET":          "The final time-series bucket covered {{.bucket_duration_ms}} ms instead of a full {{.bucket_width_ms}} ms.",
}

// nextStepTemplates maps a finding code to the one step worth trying first. Codes without an entry add none.
var nextStepTemplates = map[string]string{
	"LOGIN_FAILURES":               "Check the credentials in the pool and the login endpoint, then run again.",
	"CLIENT_TIMEOUT":               "Raise the request timeout, or check whether the server is hanging or overloaded.",
	"SATURATION_FROM_START":        "Lower the concurrency and run again to find where latency starts to climb.",
	"CONCURRENCY_EXCEEDS_CAPACITY": "Raise the concurrency, or treat the measured latency as a lower bound.",
	"NO_ASSERTIONS":                "Add an assertion on status or latency so the run has a pass/fail verdict.",
}

func (RulesAnalyzer) Name() string { return "rules" }

func (RulesAnalyzer) ModelName() string { return "rules-v1" }

func (RulesAnalyzer) Analyze(ctx context.Context, summary Summary, findings []Finding) (Insight, error) {
	if err := ctx.Err(); err != nil {
		return Insight{}, err
	}
	result := Insight{
		AnalyzerName: "rules",
		ModelName:    (RulesAnalyzer{}).ModelName(),
		CreatedAt:    time.Now().UTC(),
	}
	if len(findings) == 0 {
		result.Sentences = []string{"No diagnostic findings were identified."}
	} else {
		result.Sentences = make([]string, 0, len(findings))
		for _, finding := range findings {
			if err := ctx.Err(); err != nil {
				return Insight{}, err
			}
			text, ok := sentenceTemplates[finding.Code]
			if !ok {
				text = finding.Message
			} else {
				parsed, err := template.New(finding.Code).Option("missingkey=zero").Parse(text)
				if err != nil {
					return Insight{}, fmt.Errorf("parse insight template %s: %w", finding.Code, err)
				}
				var rendered strings.Builder
				if err := parsed.Execute(&rendered, finding.Values); err != nil {
					return Insight{}, fmt.Errorf("render insight template %s: %w", finding.Code, err)
				}
				text = rendered.String()
			}
			if step, ok := nextStepTemplates[finding.Code]; ok {
				result.Sections.NextSteps = append(result.Sections.NextSteps, step)
			}
			if text != "" {
				result.Sentences = append(result.Sentences, text)
				if finding.Severity != SeverityInfo {
					result.Sections.Causes = append(result.Sections.Causes, text)
				}
			}
		}
	}
	result.Text = strings.Join(result.Sentences, " ")
	result.Sections.Numbers = numberLines(summary)
	if len(result.Sections.Causes) == 0 {
		result.Sections.Causes = []string{"Nothing in these numbers points to a problem."}
	}
	if len(result.Sections.NextSteps) == 0 {
		result.Sections.NextSteps = []string{"Re-run after your next change and compare the percentiles and error rate."}
	}
	return result, nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// numberLines is the "What the numbers say" section of the rules analyzer: aggregates only.
func numberLines(s Summary) []string {
	lines := []string{
		fmt.Sprintf("%s completed, %.1f requests per second.", plural(s.RequestCount, "request", "requests"), s.RequestsPerSecond),
		fmt.Sprintf("Latency: p50 %d ms, p95 %d ms, p99 %d ms.", s.P50Millis, s.P95Millis, s.P99Millis),
		fmt.Sprintf("%s (%.1f%% of requests).", plural(s.ErrorCount, "error", "errors"), s.ErrorRate*100),
	}
	if s.Logins != nil {
		lines = append(lines, fmt.Sprintf("%d of %d logins succeeded.", s.Logins.Succeeded, s.Logins.Attempted))
	}
	return lines
}

func LoadConfig() (Config, error) {
	return loadConfig(os.Getenv)
}

func loadConfig(getenv func(string) string) (Config, error) {
	config := Config{Analyzer: "rules", LLMTimeout: 90 * time.Second}
	if value := strings.TrimSpace(getenv("PRIMUS_ANALYZER")); value != "" {
		config.Analyzer = value
	}
	config.LLMURL = strings.TrimSpace(getenv("PRIMUS_LLM_URL"))
	config.LLMModel = strings.TrimSpace(getenv("PRIMUS_LLM_MODEL"))
	if value := strings.TrimSpace(getenv("PRIMUS_LLM_TIMEOUT")); value != "" {
		timeout, err := time.ParseDuration(value)
		if err != nil {
			return config, fmt.Errorf("parse PRIMUS_LLM_TIMEOUT %q: %w", value, err)
		}
		if timeout <= 0 {
			return config, fmt.Errorf("PRIMUS_LLM_TIMEOUT must be positive")
		}
		config.LLMTimeout = timeout
	}
	return config, nil
}

func NewAnalyzer(config Config) (Analyzer, error) {
	name := strings.ToLower(strings.TrimSpace(config.Analyzer))
	if name == "" || name == "rules" {
		return RulesAnalyzer{}, nil
	}
	if name == "ollama" {
		return newOllamaAnalyzer(config)
	}
	return nil, fmt.Errorf("unsupported analyzer %q", config.Analyzer)
}
