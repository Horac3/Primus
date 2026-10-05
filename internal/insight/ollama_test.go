package insight

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
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
			if !strings.Contains(strings.ToLower(request.Messages[0].Content), "original unit and precision") {
				t.Errorf("system message missing number-format guidance: %q", request.Messages[0].Content)
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

func TestOllamaAnalyzerLogsRejectedInsightAtDebug(t *testing.T) {
	// Arrange
	rawInsightJSON := `{"headline":"Uptime was 87.65 percent","explanation":"The run was successful.","next_steps":["Review the run."]}`
	responseBody, err := json.Marshal(map[string]any{
		"message": map[string]string{"content": rawInsightJSON},
	})
	if err != nil {
		t.Fatalf("marshal mock response: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(responseBody)
	}))
	defer server.Close()
	var logs bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })
	analyzer := OllamaAnalyzer{URL: server.URL, Model: "test-model", Client: server.Client()}

	// Act
	_, err = analyzer.Analyze(context.Background(), Summary{P95Millis: 104}, nil)

	// Assert
	if err == nil {
		t.Fatal("Analyze succeeded, want grounding error")
	}
	var entry struct {
		Level    string `json:"level"`
		Message  string `json:"msg"`
		Response string `json:"response"`
	}
	if decodeErr := json.Unmarshal(logs.Bytes(), &entry); decodeErr != nil {
		t.Fatalf("decode debug log %q: %v", logs.String(), decodeErr)
	}
	if entry.Level != "DEBUG" || entry.Response != rawInsightJSON {
		t.Errorf("debug log entry = %+v, want full rejected response", entry)
	}
}

func TestVerifyInsightNumbersGroundsActualRunOutput(t *testing.T) {
	// Arrange
	input, err := json.Marshal(struct {
		Summary  Summary   `json:"summary"`
		Findings []Finding `json:"findings"`
	}{
		Summary: Summary{
			RequestsPerSecond: 103.98910573732108,
			ErrorRate:         0.0019230769230769232,
		},
		Findings: []Finding{},
	})
	if err != nil {
		t.Fatalf("marshal actual run summary values: %v", err)
	}
	output := "The error rate was 0.001923 and throughput was 103.989 requests per second."

	// Act
	err = verifyInsightNumbers(output, input)

	// Assert
	if err != nil {
		t.Fatalf("verifyInsightNumbers rejected output using the run's actual summary values: %v", err)
	}
}

func TestVerifyInsightNumbersAcceptsActualQwenResponseForStoredRun(t *testing.T) {
	// Arrange
	input := []byte(`{
		"summary": {
			"p50_millis": 44,
			"p95_millis": 58,
			"p99_millis": 150,
			"request_count": 1040,
			"requests_per_second": 103.98910573732108,
			"error_count": 2,
			"error_rate": 0.0019230769230769232,
			"status_counts": {"200": 1038},
			"failure_causes": [{"cause": "context deadline exceeded", "count": 2}],
			"latency_buckets": [{"lower_bound_ms": 0, "upper_bound_ms": 50, "count": 794}],
			"average_latency_ms": 47.35163776493256,
			"concurrency": 5
		},
		"findings": []
	}`)
	rawInsightJSON := `{"explanation":"The load test achieved 103.99 requests per second with a 200 status code for all requests. However, 2 out of 1040 requests failed with a 'context deadline exceeded' error. The average response time was 47.35 ms, with 95th percentile at 58 ms and 99th percentile at 150 ms. The test had 2 failures, both due to client timeout. No assertions were configured, so no pass/fail criteria were applied. Latency distribution shows most requests (794) had response times between 0-50 ms, with a few requests taking longer. The test was run at a concurrency level of 5. All failures were due to client timeout issues.","headline":"Client Timeout Failures: 2 out of 1040 requests failed with context deadline exceeded error, average response time 47.35 ms, 95th percentile 58 ms, 99th percentile 150 ms, concurrency 5, no assertions configured for pass/fail criteria.","next_steps":["Investigate the root cause of the context deadline exceeded errors.","Adjust the client timeout settings if necessary.","Consider adding more concurrency to improve throughput."]}`
	var generated ollamaInsight
	if err := json.Unmarshal([]byte(rawInsightJSON), &generated); err != nil {
		t.Fatalf("unmarshal captured Ollama response: %v", err)
	}
	output := strings.Join(append([]string{generated.Headline, generated.Explanation}, generated.NextSteps...), " ")

	// Act
	err := verifyInsightNumbers(output, input)

	// Assert
	if err != nil {
		t.Fatalf("verifyInsightNumbers rejected captured Ollama output for the stored run: %v", err)
	}
}

func TestVerifyInsightNumbersToleratesEquivalentAndRejectsFabricatedNumbers(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		output string
		wantOK bool
	}{
		{
			name:   "fraction restated as percentage",
			input:  `{"summary":{"error_rate":0.0019}}`,
			output: "The error rate was 0.19 percent.",
			wantOK: true,
		},
		{
			name:   "percentage rounded to shown precision",
			input:  `{"summary":{"error_rate":0.0019230769230769232}}`,
			output: "The error rate was 0.2%.",
			wantOK: true,
		},
		{
			name:   "percentage restated as fraction",
			input:  `{"summary":{"error_rate":0.19}}`,
			output: "The error rate was 0.0019.",
			wantOK: true,
		},
		{
			name:   "rounding within tolerance",
			input:  `{"summary":{"latency_ms":104}}`,
			output: "Latency was 103.989 ms.",
			wantOK: true,
		},
		{
			name:   "outside tolerance",
			input:  `{"summary":{"latency_ms":104}}`,
			output: "Latency was 250 ms.",
			wantOK: false,
		},
		{
			name:   "fabricated uptime percentage",
			input:  `{"summary":{"request_count":10,"error_rate":0.2}}`,
			output: "The service had 87.65 percent uptime.",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			input := []byte(tt.input)

			// Act
			err := verifyInsightNumbers(tt.output, input)

			// Assert
			if gotOK := err == nil; gotOK != tt.wantOK {
				t.Errorf("verifyInsightNumbers() error = %v, want accepted = %v", err, tt.wantOK)
			}
		})
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
