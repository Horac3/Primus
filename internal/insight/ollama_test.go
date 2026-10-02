package insight

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOllamaAnalyzerAnalyze(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/chat" {
			t.Errorf("request = %s %s, want POST /api/chat", r.Method, r.URL.Path)
		}
		var request struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
			Stream    bool           `json:"stream"`
			Format    map[string]any `json:"format"`
			Options   map[string]any `json:"options"`
			KeepAlive string         `json:"keep_alive"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		if request.Model != "test-model" || request.Stream || request.KeepAlive != "2m" {
			t.Errorf("request settings: model %q stream %v keep_alive %q", request.Model, request.Stream, request.KeepAlive)
		}
		if request.Options["temperature"] != float64(0) {
			t.Errorf("temperature = %v, want 0", request.Options["temperature"])
		}
		if request.Format["type"] != "object" {
			t.Errorf("format = %#v, want JSON schema object", request.Format)
		}
		if len(request.Messages) != 2 || request.Messages[0].Role != "system" || request.Messages[1].Role != "user" {
			t.Errorf("messages = %+v, want system and user", request.Messages)
		} else {
			if !strings.Contains(strings.ToLower(request.Messages[0].Content), "never invent numbers") {
				t.Errorf("system message missing guardrail: %q", request.Messages[0].Content)
			}
			if !strings.Contains(request.Messages[1].Content, `"p95_millis":120`) || !strings.Contains(request.Messages[1].Content, `"findings"`) {
				t.Errorf("user message missing summary/findings JSON: %s", request.Messages[1].Content)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":{"content":"{\"headline\":\"P95 latency is 120 ms\",\"explanation\":\"The run recorded 2 errors.\",\"next_steps\":[\"Review timeout findings.\"]}"}}`))
	}))
	defer server.Close()

	analyzer, err := NewAnalyzer(Config{Analyzer: "ollama", LLMURL: server.URL, LLMModel: "test-model", LLMTimeout: time.Second})
	if err != nil {
		t.Fatalf("NewAnalyzer: %v", err)
	}
	got, err := analyzer.Analyze(context.Background(), Summary{P95Millis: 120, ErrorCount: 2}, []Finding{{Code: "CLIENT_TIMEOUT", Message: "timeouts occurred"}})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if got.AnalyzerName != "ollama" || got.ModelName != "test-model" || got.CreatedAt.IsZero() {
		t.Errorf("insight metadata = %+v", got)
	}
	if got.Headline != "P95 latency is 120 ms" || got.Explanation != "The run recorded 2 errors." || len(got.NextSteps) != 1 {
		t.Errorf("structured insight = %+v", got)
	}
}

func TestOllamaAnalyzerRejectsInventedNumber(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"message":{"content":"{\"headline\":\"Latency was 99 ms\",\"explanation\":\"P95 was 120 ms.\",\"next_steps\":[\"Review the result.\"]}"}}`))
	}))
	defer server.Close()

	analyzer := OllamaAnalyzer{URL: server.URL, Model: "test-model", Client: server.Client()}
	_, err := analyzer.Analyze(context.Background(), Summary{P95Millis: 120}, nil)
	if err == nil || !strings.Contains(err.Error(), `unsupported number "99"`) {
		t.Fatalf("Analyze error = %v, want invented-number guardrail error", err)
	}
}

func TestOllamaAnalyzerUsesContextAndConfiguredTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(100 * time.Millisecond):
		}
	}))
	defer server.Close()

	analyzer, err := NewAnalyzer(Config{Analyzer: "ollama", LLMURL: server.URL, LLMModel: "test-model", LLMTimeout: 20 * time.Millisecond})
	if err != nil {
		t.Fatalf("NewAnalyzer: %v", err)
	}
	started := time.Now()
	_, err = analyzer.Analyze(context.Background(), Summary{}, nil)
	if err == nil {
		t.Fatal("Analyze succeeded, want client timeout")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Errorf("configured client timeout took %s, want under 1s", elapsed)
	}
}
