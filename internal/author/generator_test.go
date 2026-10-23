package author

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOllamaGeneratorGenerateRequestUsesCanonicalEndpointWhenMatched(t *testing.T) {
	// Arrange
	var requestedEndpoints []Endpoint
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request ollamaRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode Ollama request: %v", err)
			return
		}
		var input RequestPrompt
		if err := json.Unmarshal([]byte(request.Messages[1].Content), &input); err != nil {
			t.Errorf("decode user prompt: %v", err)
			return
		}
		requestedEndpoints = input.Endpoints
		_, _ = w.Write([]byte(`{"message":{"content":"{\"matched_existing_id\":\"login\",\"method\":\"POST\",\"url\":\"https://invented.example/\",\"headers\":{\"Content-Type\":\"application/json\"},\"body\":\"{\\\"password\\\":\\\"wrong\\\"}\",\"confidence_note\":\"Matched the login endpoint.\"}"}}`))
	}))
	defer server.Close()
	generator := OllamaGenerator{URL: server.URL, Model: "test-model", Client: server.Client()}
	endpoints := []Endpoint{
		{ID: "login", Name: "Login", Method: "POST", URL: "https://api.example.com/login"},
		{ID: "health", Name: "Health", Method: "GET", URL: "https://api.example.com/health"},
	}

	// Act
	got, err := generator.GenerateRequest(context.Background(), "Test the login endpoint with a wrong password", endpoints)

	// Assert
	if err != nil {
		t.Fatalf("GenerateRequest: %v", err)
	}
	if got.MatchedExistingID == nil || *got.MatchedExistingID != "login" {
		t.Fatalf("MatchedExistingID = %v, want login", got.MatchedExistingID)
	}
	if got.Method != "POST" || got.URL != "https://api.example.com/login" {
		t.Errorf("matched target = %s %s, want canonical endpoint target", got.Method, got.URL)
	}
	if len(requestedEndpoints) != 2 || requestedEndpoints[0].ID != "login" ||
		requestedEndpoints[0].Name != "Login" || requestedEndpoints[0].URL != endpoints[0].URL {
		t.Errorf("prompt endpoints = %+v, want only method/URL/name endpoint context", requestedEndpoints)
	}
	if got.Body != `{"password":"wrong"}` || got.Headers["Content-Type"] != "application/json" {
		t.Errorf("request details = body %q, headers %+v", got.Body, got.Headers)
	}
}

func TestOllamaGeneratorGenerateRequestSupportsNewEndpoint(t *testing.T) {
	// Arrange
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request ollamaRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode Ollama request: %v", err)
			return
		}
		var input RequestPrompt
		if err := json.Unmarshal([]byte(request.Messages[1].Content), &input); err != nil {
			t.Errorf("decode user prompt: %v", err)
			return
		}
		if !strings.Contains(input.Description, "new invoice") {
			t.Errorf("description = %q, want user prompt", input.Description)
		}
		_, _ = w.Write([]byte(`{"message":{"content":"{\"matched_existing_id\":null,\"method\":\"POST\",\"url\":\"https://api.example.com/invoices\",\"headers\":{},\"body\":\"{}\",\"confidence_note\":\"No existing invoice endpoint matches; this is a new target.\"}"}}`))
	}))
	defer server.Close()
	generator := OllamaGenerator{URL: server.URL, Model: "test-model", Client: server.Client()}

	// Act
	got, err := generator.GenerateRequest(context.Background(), "Create a new invoice endpoint", []Endpoint{
		{ID: "login", Name: "Login", Method: "POST", URL: "https://api.example.com/login"},
	})

	// Assert
	if err != nil {
		t.Fatalf("GenerateRequest: %v", err)
	}
	if got.MatchedExistingID != nil || got.URL != "https://api.example.com/invoices" {
		t.Errorf("suggestion = %+v, want a clearly new invoice target", got)
	}
}

func TestOllamaGeneratorGenerateRequestRejectsUnknownMatchAndInvalidNewTarget(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{
			name:    "unknown endpoint id",
			content: `{"matched_existing_id":"other","method":"GET","url":"https://api.example.com/login","headers":{},"body":"","confidence_note":"matched"}`,
		},
		{
			name:    "invalid fabricated URL",
			content: `{"matched_existing_id":null,"method":"GET","url":"javascript:alert(1)","headers":{},"body":"","confidence_note":"new"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				content, err := json.Marshal(map[string]any{"message": map[string]string{"content": tt.content}})
				if err != nil {
					t.Errorf("marshal response: %v", err)
					return
				}
				_, _ = w.Write(content)
			}))
			defer server.Close()
			generator := OllamaGenerator{URL: server.URL, Model: "test-model", Client: server.Client()}

			// Act
			_, err := generator.GenerateRequest(context.Background(), "Test an endpoint", []Endpoint{
				{ID: "login", Name: "Login", Method: "POST", URL: "https://api.example.com/login"},
			})

			// Assert
			if err == nil {
				t.Fatal("GenerateRequest succeeded, want validation error")
			}
		})
	}
}

func TestNewGeneratorSelection(t *testing.T) {
	// Arrange
	config := Config{Analyzer: "rules"}

	// Act
	generator, err := NewGenerator(config)

	// Assert
	if err != nil {
		t.Fatalf("NewGenerator(rules): %v", err)
	}
	if generator != nil {
		t.Errorf("NewGenerator(rules) = %T, want nil to hide NL affordances", generator)
	}
}

func TestOllamaGeneratorUsesConfiguredTimeout(t *testing.T) {
	// Arrange
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(time.Second):
		}
	}))
	defer server.Close()
	generator, err := NewOllamaGenerator(Config{
		Analyzer: "ollama", URL: server.URL, Model: "test-model", Timeout: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewOllamaGenerator: %v", err)
	}

	// Act
	started := time.Now()
	_, err = generator.GenerateRequest(context.Background(), "Create an endpoint", nil)

	// Assert
	if err == nil {
		t.Fatal("GenerateRequest succeeded, want timeout error")
	}
	if time.Since(started) > time.Second {
		t.Errorf("GenerateRequest took more than one second with 20 ms timeout")
	}
}

func TestOllamaGeneratorGenerateTestConfigTable(t *testing.T) {
	tests := []struct {
		name        string
		description string
		content     string
		want        TestConfigSuggestion
	}{
		{
			name:        "load test",
			description: "Simulate 200 users hammering this for 2 minutes",
			content:     `{"mode":"load","concurrency":200,"duration_seconds":120,"total_requests":null,"reasoning":"A two-minute load run with 200 concurrent workers matches the request."}`,
			want:        TestConfigSuggestion{Mode: "load", Concurrency: 200, DurationSeconds: 120, Reasoning: "A two-minute load run with 200 concurrent workers matches the request."},
		},
		{
			name:        "soak test",
			description: "Run a 1 hour soak with 20 users",
			content:     `{"mode":"soak","concurrency":20,"duration_seconds":3600,"total_requests":null,"reasoning":"Soak mode runs for the requested hour at 20 workers."}`,
			want:        TestConfigSuggestion{Mode: "soak", Concurrency: 20, DurationSeconds: 3600, Reasoning: "Soak mode runs for the requested hour at 20 workers."},
		},
		{
			name:        "burst test",
			description: "Send 1000 requests in bursts with 10 workers",
			content:     `{"mode":"burst","concurrency":10,"duration_seconds":null,"total_requests":1000,"reasoning":"Burst mode uses the requested 1000-request count."}`,
			want:        TestConfigSuggestion{Mode: "burst", Concurrency: 10, TotalRequests: 1000, Reasoning: "Burst mode uses the requested 1000-request count."},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				body, err := json.Marshal(map[string]any{"message": map[string]string{"content": tt.content}})
				if err != nil {
					t.Errorf("marshal response: %v", err)
					return
				}
				_, _ = w.Write(body)
			}))
			defer server.Close()
			generator := OllamaGenerator{URL: server.URL, Model: "test-model", Client: server.Client()}

			// Act
			got, err := generator.GenerateTestConfig(context.Background(), tt.description, Endpoint{
				ID: "ep-1", Name: "Login", Method: "POST", URL: "https://api.example.com/login",
			}, DefaultBounds())

			// Assert
			if err != nil {
				t.Fatalf("GenerateTestConfig: %v", err)
			}
			if got != tt.want {
				t.Errorf("GenerateTestConfig() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestOllamaGeneratorGeneratePayloadsUsesTemplateAndValidatesShape(t *testing.T) {
	// Arrange
	template := json.RawMessage(`{"name":"example","profile":{"active":true},"tags":["starter"]}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode Ollama request: %v", err)
			return
		}
		var input struct {
			Template json.RawMessage `json:"template"`
			Count    int             `json:"count"`
		}
		if err := json.Unmarshal([]byte(request.Messages[1].Content), &input); err != nil {
			t.Errorf("decode generation input: %v", err)
			return
		}
		if string(input.Template) != string(template) || input.Count != 2 {
			t.Errorf("generation input = %+v, want template and count 2", input)
		}
		content, err := json.Marshal(map[string]any{"payloads": []json.RawMessage{
			json.RawMessage(`{"name":"Ada","profile":{"active":false},"tags":["alpha"]}`),
			json.RawMessage(`{"name":"Grace","profile":{"active":true},"tags":["beta"]}`),
		}})
		if err != nil {
			t.Errorf("marshal generated payloads: %v", err)
			return
		}
		response, err := json.Marshal(map[string]any{"message": map[string]string{"content": string(content)}})
		if err != nil {
			t.Errorf("marshal Ollama response: %v", err)
			return
		}
		_, _ = w.Write(response)
	}))
	defer server.Close()
	generator := OllamaGenerator{URL: server.URL, Model: "test-model", Client: server.Client()}

	// Act
	got, err := generator.GeneratePayloads(context.Background(), template, 2)

	// Assert
	if err != nil {
		t.Fatalf("GeneratePayloads: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d payloads, want 2", len(got))
	}
	if err := ValidatePayloads(template, got, 2); err != nil {
		t.Fatalf("ValidatePayloads: %v", err)
	}
}

func TestValidatePayloadsRejectsShapeChanges(t *testing.T) {
	tests := []struct {
		name     string
		template json.RawMessage
		payload  json.RawMessage
	}{
		{
			name: "missing object key", template: json.RawMessage(`{"name":"example","active":true}`),
			payload: json.RawMessage(`{"name":"generated"}`),
		},
		{
			name: "wrong scalar type", template: json.RawMessage(`{"count":1}`),
			payload: json.RawMessage(`{"count":"one"}`),
		},
		{
			name: "different array length", template: json.RawMessage(`{"tags":["one"]}`),
			payload: json.RawMessage(`{"tags":["one","two"]}`),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			payloads := []json.RawMessage{tt.payload}

			// Act
			err := ValidatePayloads(tt.template, payloads, 1)

			// Assert
			if err == nil {
				t.Fatalf("ValidatePayloads(%s) succeeded, want shape rejection", tt.payload)
			}
		})
	}
}

func TestValidateTestConfigSuggestionRejectsOutOfBoundsWithoutClamping(t *testing.T) {
	tests := []struct {
		name       string
		suggestion TestConfigSuggestion
		wantError  string
	}{
		{
			name:       "concurrency above ceiling",
			suggestion: TestConfigSuggestion{Mode: "load", Concurrency: 2001, DurationSeconds: 30, Reasoning: "extreme"},
			wantError:  "concurrency 2001 exceeds maximum 2000",
		},
		{
			name:       "duration above ceiling",
			suggestion: TestConfigSuggestion{Mode: "soak", Concurrency: 5, DurationSeconds: 3601, Reasoning: "extreme"},
			wantError:  "duration_seconds 3601 exceeds maximum 3600",
		},
		{
			name:       "request count above ceiling",
			suggestion: TestConfigSuggestion{Mode: "burst", Concurrency: 5, TotalRequests: 1000001, Reasoning: "extreme"},
			wantError:  "total_requests 1000001 exceeds maximum 1000000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			bounds := DefaultBounds()

			// Act
			err := ValidateTestConfigSuggestion(tt.suggestion, bounds)

			// Assert
			if err == nil || err.Error() != tt.wantError {
				t.Fatalf("ValidateTestConfigSuggestion() error = %v, want %q", err, tt.wantError)
			}
		})
	}
}

func TestValidateTestConfigSuggestionRejectsInvalidModesAndMissingStops(t *testing.T) {
	tests := []struct {
		name       string
		suggestion TestConfigSuggestion
	}{
		{name: "unknown mode", suggestion: TestConfigSuggestion{Mode: "unbounded", Concurrency: 1, DurationSeconds: 1, Reasoning: "x"}},
		{name: "load missing duration", suggestion: TestConfigSuggestion{Mode: "load", Concurrency: 1, Reasoning: "x"}},
		{name: "soak with request count", suggestion: TestConfigSuggestion{Mode: "soak", Concurrency: 1, TotalRequests: 1, Reasoning: "x"}},
		{name: "burst with duration", suggestion: TestConfigSuggestion{Mode: "burst", Concurrency: 1, DurationSeconds: 1, Reasoning: "x"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			bounds := DefaultBounds()

			// Act
			err := ValidateTestConfigSuggestion(tt.suggestion, bounds)

			// Assert
			if err == nil {
				t.Fatalf("ValidateTestConfigSuggestion(%+v) succeeded, want rejection", tt.suggestion)
			}
		})
	}
}

func TestValidateTestConfigSuggestionRequestsClarification(t *testing.T) {
	// Arrange
	suggestion := TestConfigSuggestion{
		Mode: "load", Reasoning: "Clarification required: specify a concurrency value.",
	}

	// Act
	err := ValidateTestConfigSuggestion(suggestion, DefaultBounds())

	// Assert
	if err == nil || err.Error() != suggestion.Reasoning {
		t.Fatalf("ValidateTestConfigSuggestion() error = %v, want %q", err, suggestion.Reasoning)
	}
}

func TestLoadBoundsDefaultsAndOverrides(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want Bounds
	}{
		{name: "defaults", env: map[string]string{}, want: DefaultBounds()},
		{
			name: "configured ceilings",
			env: map[string]string{
				"PRIMUS_AUTHOR_MAX_CONCURRENCY":      "150",
				"PRIMUS_AUTHOR_MAX_DURATION_SECONDS": "7200",
				"PRIMUS_AUTHOR_MAX_TOTAL_REQUESTS":   "25000",
			},
			want: Bounds{MaxConcurrency: 150, MaxDurationSeconds: 7200, MaxTotalRequests: 25000},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			getenv := func(key string) string { return tt.env[key] }

			// Act
			got, err := LoadBounds(getenv)

			// Assert
			if err != nil {
				t.Fatalf("LoadBounds: %v", err)
			}
			if got != tt.want {
				t.Errorf("LoadBounds() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
