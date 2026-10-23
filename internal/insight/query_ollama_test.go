package insight

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func ollamaQueryServer(t *testing.T, content string, inspect func(body []byte)) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/chat" {
			t.Errorf("request = %s %s, want POST /api/chat", r.Method, r.URL.Path)
		}
		var body json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if inspect != nil {
			inspect(body)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]string{"role": "assistant", "content": content}})
	}))
	t.Cleanup(server.Close)
	return server
}

func TestOllamaInterpreterReturnsValidatedFilter(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	var request struct {
		Model    string `json:"model"`
		Stream   bool   `json:"stream"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		Format struct {
			Properties           map[string]any `json:"properties"`
			AdditionalProperties bool           `json:"additionalProperties"`
		} `json:"format"`
		Options map[string]any `json:"options"`
	}
	// The body crosses from the server goroutine through a channel so -race sees the hand-off.
	bodies := make(chan []byte, 1)
	server := ollamaQueryServer(t, `{"modes":["Burst"],"outcome":"fail","within_hours":168,"min_p95_ms":500}`, func(body []byte) {
		bodies <- body
	})
	interpreter := OllamaInterpreter{URL: server.URL, Model: "test-model"}

	got, err := interpreter.Interpret(context.Background(), "failed burst runs last week with p95 over 500ms", now)
	if err != nil {
		t.Fatalf("Interpret: %v", err)
	}
	if err := json.Unmarshal(<-bodies, &request); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	if got.Describe() != "burst runs · failed · last 168h · p95 ≥ 500 ms · newest first · up to 20" {
		t.Fatalf("filter = %s", got.Describe())
	}
	if request.Model != "test-model" || request.Stream || request.Options["temperature"] != float64(0) {
		t.Errorf("request settings = model %q stream %v options %v", request.Model, request.Stream, request.Options)
	}
	for _, field := range []string{"modes", "outcome", "within_hours", "since", "min_p95_ms", "max_error_rate_pct", "target", "sort", "limit"} {
		if _, ok := request.Format.Properties[field]; !ok {
			t.Errorf("format schema is missing %q", field)
		}
	}
	if request.Format.AdditionalProperties {
		t.Error("format schema allows additional properties")
	}
	if len(request.Messages) != 2 {
		t.Fatalf("messages = %d, want system and user", len(request.Messages))
	}
	if !strings.Contains(request.Messages[0].Content, "2026-10-21") {
		t.Errorf("system prompt does not give today's date: %q", request.Messages[0].Content)
	}
	if request.Messages[1].Content != "failed burst runs last week with p95 over 500ms" {
		t.Errorf("user message = %q, want only the question", request.Messages[1].Content)
	}
}

func TestOllamaInterpreterRejectsInvalidReplies(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	for name, content := range map[string]string{
		"unknown field": `{"modes":["load"],"sql":"DROP TABLE runs"}`,
		"unknown mode":  `{"modes":["spike"]}`,
		"bad limit":     `{"limit":1000}`,
		"not json":      `burst runs, failed`,
		"trailing data": `{"outcome":"fail"} {"outcome":"pass"}`,
	} {
		t.Run(name, func(t *testing.T) {
			server := ollamaQueryServer(t, content, nil)
			_, err := OllamaInterpreter{URL: server.URL, Model: "m"}.Interpret(context.Background(), "q", now)
			if err == nil {
				t.Fatalf("Interpret accepted %s", content)
			}
		})
	}
}

type fakeQueryInterpreter struct {
	filter Filter
	err    error
	wait   time.Duration
}

func (fakeQueryInterpreter) Name() string { return "ollama" }

func (f fakeQueryInterpreter) Interpret(ctx context.Context, _ string, _ time.Time) (Filter, error) {
	if f.wait > 0 {
		select {
		case <-time.After(f.wait):
		case <-ctx.Done():
			return Filter{}, ctx.Err()
		}
	}
	return f.filter, f.err
}

func TestInterpretQueryUsesModelWhenItAnswers(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	filter, _ := Filter{Outcome: "pass"}.Validate()
	got := InterpretQuery(context.Background(), fakeQueryInterpreter{filter: filter}, time.Second, "failed burst runs", now)
	if got.Interpreter != "ollama" || got.FellBack || !got.Understood || got.Filter.Outcome != "pass" {
		t.Fatalf("result = %+v, want the model's filter", got)
	}
}

func TestInterpretQueryFallsBackToRules(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	cases := map[string]QueryInterpreter{
		"no model":    nil,
		"model error": fakeQueryInterpreter{err: errors.New("connection refused")},
		"model slow":  fakeQueryInterpreter{wait: time.Minute},
	}
	for name, primary := range cases {
		t.Run(name, func(t *testing.T) {
			start := time.Now()
			got := InterpretQuery(context.Background(), primary, 50*time.Millisecond, "failed burst runs", now)
			if elapsed := time.Since(start); elapsed > 5*time.Second {
				t.Fatalf("InterpretQuery took %s, want the timeout to cut the model off", elapsed)
			}
			if got.Interpreter != "rules" || !got.Understood {
				t.Fatalf("result = %+v, want the rules filter", got)
			}
			if got.Filter.Describe() != "burst runs · failed · newest first · up to 20" {
				t.Fatalf("filter = %s", got.Filter.Describe())
			}
			if got.FellBack != (primary != nil) {
				t.Fatalf("FellBack = %v, want %v", got.FellBack, primary != nil)
			}
		})
	}
}

func TestNewQueryInterpreterFollowsAnalyzerConfig(t *testing.T) {
	if got := NewQueryInterpreter(Config{Analyzer: "rules"}); got != nil {
		t.Fatalf("rules analyzer gave interpreter %T, want nil", got)
	}
	if got := NewQueryInterpreter(Config{Analyzer: "ollama", LLMURL: "not a url", LLMModel: "m"}); got != nil {
		t.Fatalf("invalid config gave interpreter %T, want nil", got)
	}
	got := NewQueryInterpreter(Config{Analyzer: "ollama", LLMURL: "http://ollama:11434/", LLMModel: "qwen"})
	ollama, ok := got.(OllamaInterpreter)
	if !ok || ollama.URL != "http://ollama:11434" || ollama.Model != "qwen" {
		t.Fatalf("interpreter = %#v", got)
	}
}
