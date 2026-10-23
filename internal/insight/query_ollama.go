package insight

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// DefaultQueryTimeout caps how long a history question waits for the model
// before the rules interpreter answers instead.
const DefaultQueryTimeout = 20 * time.Second

// MaxQuestionLength bounds a history question.
const MaxQuestionLength = 500

// QueryInterpreter turns a history question into a validated Filter.
type QueryInterpreter interface {
	Name() string
	Interpret(ctx context.Context, question string, now time.Time) (Filter, error)
}

// QueryResult is an interpreted history question and which interpreter produced it.
type QueryResult struct {
	Filter      Filter
	Interpreter string
	// FellBack is true when a model was configured but the rules answered.
	FellBack bool
	// Understood is false when the rules matched nothing and the filter lists every run.
	Understood bool
}

// InterpretQuery asks primary (may be nil) within timeout and falls back to
// RulesInterpreter on any failure.
func InterpretQuery(ctx context.Context, primary QueryInterpreter, timeout time.Duration, question string, now time.Time) QueryResult {
	if primary != nil {
		modelCtx, cancel := context.WithTimeout(ctx, timeout)
		filter, err := primary.Interpret(modelCtx, question, now)
		cancel()
		if err == nil {
			return QueryResult{Filter: filter, Interpreter: primary.Name(), Understood: true}
		}
		// The error can quote values echoed from the question, so only its kind is logged.
		slog.Debug("history query: model interpreter failed; using rules", "interpreter", primary.Name(),
			"timeout", errors.Is(err, context.DeadlineExceeded))
	}
	filter, understood := RulesInterpreter{}.Parse(question, now)
	return QueryResult{Filter: filter, Interpreter: "rules", FellBack: primary != nil, Understood: understood}
}

// NewQueryInterpreter returns the model interpreter configured by
// PRIMUS_ANALYZER, or nil when the rules interpreter should answer alone.
func NewQueryInterpreter(config Config) QueryInterpreter {
	if strings.ToLower(strings.TrimSpace(config.Analyzer)) != "ollama" {
		return nil
	}
	analyzer, err := newOllamaAnalyzer(config)
	if err != nil {
		slog.Warn("history query: ollama not configured; using rules", "error", err)
		return nil
	}
	ollama, ok := analyzer.(OllamaAnalyzer)
	if !ok {
		return nil
	}
	return OllamaInterpreter{URL: ollama.URL, Model: ollama.Model, Client: ollama.Client}
}

// OllamaInterpreter asks a local model to fill the Filter schema. It sends only
// the question, today's date and the schema: no run data.
type OllamaInterpreter struct {
	URL    string
	Model  string
	Client *http.Client
}

func (OllamaInterpreter) Name() string { return "ollama" }

func (o OllamaInterpreter) Interpret(ctx context.Context, question string, now time.Time) (Filter, error) {
	requestBody := ollamaChatRequest{
		Model: o.Model,
		Messages: []ollamaMessage{
			{Role: "system", Content: queryPrompt(now)},
			{Role: "user", Content: question},
		},
		Format:    queryFilterSchema(),
		KeepAlive: "2m",
	}
	requestBody.Options.Temperature = 0
	encoded, err := json.Marshal(requestBody)
	if err != nil {
		return Filter{}, fmt.Errorf("marshal Ollama query request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, o.URL+"/api/chat", bytes.NewReader(encoded))
	if err != nil {
		return Filter{}, fmt.Errorf("create Ollama query request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	client := o.Client
	if client == nil {
		client = &http.Client{Timeout: defaultLLMTimeout}
	}
	response, err := client.Do(request)
	if err != nil {
		return Filter{}, fmt.Errorf("call Ollama chat API: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return Filter{}, fmt.Errorf("Ollama chat API returned %s", response.Status)
	}
	var chat ollamaChatResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&chat); err != nil {
		return Filter{}, fmt.Errorf("decode Ollama chat response: %w", err)
	}
	decoder := json.NewDecoder(strings.NewReader(chat.Message.Content))
	decoder.DisallowUnknownFields()
	var filter Filter
	if err := decoder.Decode(&filter); err != nil {
		return Filter{}, fmt.Errorf("decode Ollama query filter: %w", err)
	}
	if decoder.More() {
		return Filter{}, errors.New("Ollama query filter has trailing data")
	}
	validated, err := filter.Validate()
	if err != nil {
		return Filter{}, fmt.Errorf("Ollama query filter is invalid: %w", err)
	}
	return validated, nil
}

func queryPrompt(now time.Time) string {
	return "Turn a question about past API load-test runs into a JSON filter. Today is " + now.Format(queryDateLayout) +
		". Fill only the fields the question asks for and leave the rest out. " +
		"modes: load, burst, soak or rate. outcome: pass, fail or no_assertions. stopped: true for runs stopped early. " +
		"Use within_hours for relative windows (last week = 168) and since/until (YYYY-MM-DD, inclusive) for dates; never both. " +
		"Latency bounds are in milliseconds; error-rate bounds are percentages 0-100. " +
		"target is a word from the endpoint name or URL. " +
		"sort: newest, oldest, p95_desc (slowest), p95_asc (fastest) or error_rate_desc. limit: 1-100."
}

func queryFilterSchema() map[string]any {
	integer := map[string]any{"type": "integer", "minimum": 0}
	percent := map[string]any{"type": "number", "minimum": 0, "maximum": 100}
	date := map[string]any{"type": "string", "pattern": `^\d{4}-\d{2}-\d{2}$`}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"modes":              map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": queryModes}},
			"outcome":            map[string]any{"type": "string", "enum": queryOutcomes},
			"stopped":            map[string]any{"type": "boolean"},
			"within_hours":       integer,
			"since":              date,
			"until":              date,
			"min_p50_ms":         integer,
			"max_p50_ms":         integer,
			"min_p95_ms":         integer,
			"max_p95_ms":         integer,
			"min_p99_ms":         integer,
			"max_p99_ms":         integer,
			"min_error_rate_pct": percent,
			"max_error_rate_pct": percent,
			"min_total_requests": integer,
			"target":             map[string]any{"type": "string", "maxLength": maxTargetLength},
			"sort":               map[string]any{"type": "string", "enum": querySorts},
			"limit":              map[string]any{"type": "integer", "minimum": 1, "maximum": maxQueryLimit},
		},
		"additionalProperties": false,
	}
}
