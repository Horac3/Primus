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
