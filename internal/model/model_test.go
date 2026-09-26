package model_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Horac3/Primus/internal/model"
)

func TestRequestDefinition_JSONRoundTrip(t *testing.T) {
	// Arrange
	orig := model.RequestDefinition{
		ID:          "req-1",
		Name:        "Login",
		Method:      "POST",
		URLTemplate: "https://example.com/login",
		Headers:     map[string]string{"Content-Type": "application/json"},
		QueryParams: map[string]model.ParamSpec{
			"trace": {Static: strPtr("abc")},
		},
		Body: &model.BodyTemplate{Raw: []byte(`{"user":"test"}`)},
		Auth: &model.AuthConfig{Type: model.AuthTypeBearer, Token: "tok"},
	}

	// Act
	b, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got model.RequestDefinition
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Assert
	if got.ID != orig.ID {
		t.Errorf("ID: got %q, want %q", got.ID, orig.ID)
	}
	if got.Name != orig.Name {
		t.Errorf("Name: got %q, want %q", got.Name, orig.Name)
	}
	if got.Method != orig.Method {
		t.Errorf("Method: got %q, want %q", got.Method, orig.Method)
	}
	if got.URLTemplate != orig.URLTemplate {
		t.Errorf("URLTemplate: got %q, want %q", got.URLTemplate, orig.URLTemplate)
	}
	if got.Headers["Content-Type"] != orig.Headers["Content-Type"] {
		t.Errorf("Headers: got %v, want %v", got.Headers, orig.Headers)
	}
	if got.Auth == nil || got.Auth.Token != orig.Auth.Token {
		t.Errorf("Auth.Token: got %v, want %v", got.Auth, orig.Auth)
	}
}

func TestParamSpec_JSONRoundTrip(t *testing.T) {
	// Arrange
	orig := model.ParamSpec{
		Dynamic: &model.DynamicValue{
			Kind:   model.DynamicKindRandomInt,
			IntMin: 1,
			IntMax: 100,
		},
	}

	// Act
	b, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got model.ParamSpec
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Assert
	if got.Dynamic == nil {
		t.Fatal("Dynamic is nil after round-trip")
	}
	if got.Dynamic.Kind != orig.Dynamic.Kind {
		t.Errorf("Dynamic.Kind: got %q, want %q", got.Dynamic.Kind, orig.Dynamic.Kind)
	}
	if got.Dynamic.IntMin != orig.Dynamic.IntMin || got.Dynamic.IntMax != orig.Dynamic.IntMax {
		t.Errorf("Dynamic range: got [%d,%d], want [%d,%d]",
			got.Dynamic.IntMin, got.Dynamic.IntMax,
			orig.Dynamic.IntMin, orig.Dynamic.IntMax)
	}
}

func TestTestConfig_JSONRoundTrip(t *testing.T) {
	// Arrange
	orig := model.TestConfig{
		ID:            "cfg-1",
		RequestID:     "req-1",
		Mode:          model.ModeLoad,
		Concurrency:   10,
		TotalRequests: 1000,
		BurstPattern:  nil,
		Assertions: model.Assertions{
			ExpectedStatus: []int{200, 201},
			MaxP95Millis:   500,
			MaxErrorRate:   0.01,
		},
	}

	// Act
	b, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got model.TestConfig
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Assert
	if got.Mode != orig.Mode {
		t.Errorf("Mode: got %q, want %q", got.Mode, orig.Mode)
	}
	if got.Concurrency != orig.Concurrency {
		t.Errorf("Concurrency: got %d, want %d", got.Concurrency, orig.Concurrency)
	}
	if len(got.Assertions.ExpectedStatus) != 2 {
		t.Errorf("ExpectedStatus len: got %d, want 2", len(got.Assertions.ExpectedStatus))
	}
}

func TestRun_JSONRoundTrip(t *testing.T) {
	// Arrange
	now := time.Now().UTC().Truncate(time.Second)
	orig := model.Run{
		ID:           "run-1",
		TestConfigID: "cfg-1",
		StartedAt:    now,
		FinishedAt:   now.Add(5 * time.Second),
		Summary: model.RunSummary{
			StatusCounts: map[int]int{200: 950, 500: 50},
			P50Millis:    120,
			P95Millis:    480,
			P99Millis:    900,
			ErrorRate:    0.05,
		},
		Passed: true,
	}

	// Act
	b, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got model.Run
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Assert
	if got.ID != orig.ID {
		t.Errorf("ID: got %q, want %q", got.ID, orig.ID)
	}
	if !got.StartedAt.Equal(orig.StartedAt) {
		t.Errorf("StartedAt: got %v, want %v", got.StartedAt, orig.StartedAt)
	}
	if got.Summary.P95Millis != orig.Summary.P95Millis {
		t.Errorf("P95: got %d, want %d", got.Summary.P95Millis, orig.Summary.P95Millis)
	}
	if got.Passed != orig.Passed {
		t.Errorf("Passed: got %v, want %v", got.Passed, orig.Passed)
	}
}

func strPtr(s string) *string { return &s }
