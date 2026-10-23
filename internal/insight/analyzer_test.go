package insight

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRulesAnalyzer_Analyze(t *testing.T) {
	tests := []struct {
		name     string
		findings []Finding
		wantText string
	}{
		{
			name:     "no findings",
			wantText: "No diagnostic findings were identified.",
		},
		{
			name: "renders finding values",
			findings: []Finding{{
				Code:   "CONCURRENCY_EXCEEDS_CAPACITY",
				Values: map[string]any{"estimated_in_flight": 476.2, "concurrency": 400},
			}},
			wantText: "Estimated about 476.2 requests in flight, above configured concurrency of 400 workers.",
		},
		{
			name:     "unknown code uses finding message",
			findings: []Finding{{Code: "FUTURE_RULE", Message: "A future diagnostic."}},
			wantText: "A future diagnostic.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := (RulesAnalyzer{}).Analyze(context.Background(), Summary{}, tt.findings)
			if err != nil {
				t.Fatalf("Analyze: %v", err)
			}
			if got.Text != tt.wantText {
				t.Errorf("Text = %q, want %q", got.Text, tt.wantText)
			}
			if got.AnalyzerName != "rules" || got.ModelName == "" || got.CreatedAt.IsZero() {
				t.Errorf("missing insight metadata: %+v", got)
			}
		})
	}
}

func TestRulesAnalyzer_AnalyzeCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (RulesAnalyzer{}).Analyze(ctx, Summary{}, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("Analyze error = %v, want context.Canceled", err)
	}
}

func TestLoadConfig(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want Config
		bad  bool
	}{
		{
			name: "defaults",
			want: Config{Analyzer: "rules", LLMTimeout: 90 * time.Second},
		},
		{
			name: "overrides",
			env: map[string]string{
				"PRIMUS_ANALYZER":    "rules",
				"PRIMUS_LLM_URL":     "http://localhost:11434",
				"PRIMUS_LLM_MODEL":   "local-model",
				"PRIMUS_LLM_TIMEOUT": "2m",
			},
			want: Config{Analyzer: "rules", LLMURL: "http://localhost:11434", LLMModel: "local-model", LLMTimeout: 2 * time.Minute},
		},
		{
			name: "invalid timeout",
			env:  map[string]string{"PRIMUS_LLM_TIMEOUT": "later"},
			bad:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := loadConfig(func(key string) string { return tt.env[key] })
			if tt.bad {
				if err == nil {
					t.Fatal("loadConfig succeeded with invalid timeout")
				}
				return
			}
			if err != nil {
				t.Fatalf("loadConfig: %v", err)
			}
			if got != tt.want {
				t.Errorf("config = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestNewAnalyzer(t *testing.T) {
	if _, err := NewAnalyzer(Config{Analyzer: "unknown"}); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("NewAnalyzer error = %v, want unsupported analyzer error", err)
	}
	if analyzer, err := NewAnalyzer(Config{Analyzer: "rules"}); err != nil || analyzer.Name() != "rules" {
		t.Fatalf("NewAnalyzer(rules) = %v, %v", analyzer, err)
	}
}
