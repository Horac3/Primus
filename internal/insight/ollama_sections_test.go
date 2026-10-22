package insight

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Horac3/Primus/internal/model"
)

func ollamaReplying(t *testing.T, content string, captured *string) OllamaAnalyzer {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if captured != nil {
			body, _ := io.ReadAll(r.Body)
			*captured = string(body)
		}
		_, _ = w.Write([]byte(content))
	}))
	t.Cleanup(server.Close)
	return OllamaAnalyzer{URL: server.URL, Model: "test-model", Client: server.Client()}
}

func TestOllamaSections_FilledFromReplyAndComparisonFromCode(t *testing.T) {
	analyzer := ollamaReplying(t, `{"message":{"content":"{\"headline\":\"P95 was 310 ms.\",\"explanation\":\"Latency rose since the last run.\",\"next_steps\":[\"Check the slowest dependency.\"]}"}}`, nil)
	summary := Summary{Mode: model.ModeLoad, Concurrency: 5, RequestCount: 100, P95Millis: 310,
		Previous: &PreviousAggregates{Mode: model.ModeLoad, Concurrency: 5, RequestCount: 100, P95Millis: 120}}
	got, err := analyzer.Analyze(context.Background(), summary, nil)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	s := got.Sections
	if len(s.Numbers) != 1 || s.Numbers[0] != "P95 was 310 ms." || len(s.Causes) != 1 || len(s.NextSteps) != 1 {
		t.Errorf("Sections = %+v", s)
	}
	if !strings.Contains(strings.Join(s.Comparison, "\n"), "p95 rose from 120 ms to 310 ms") {
		t.Errorf("Comparison = %v, want the code-written p95 line", s.Comparison)
	}
}

func TestOllamaSections_NumbersFromPreviousRunAreGrounded(t *testing.T) {
	analyzer := ollamaReplying(t, `{"message":{"content":"{\"headline\":\"P95 was 310 ms, up from 120 ms.\",\"explanation\":\"Slower than before.\",\"next_steps\":[\"Look at the slowest dependency.\"]}"}}`, nil)
	summary := Summary{P95Millis: 310, Previous: &PreviousAggregates{P95Millis: 120}}
	if _, err := analyzer.Analyze(context.Background(), summary, nil); err != nil {
		t.Fatalf("a number from the previous run was rejected: %v", err)
	}
	summary.Previous = nil
	if _, err := analyzer.Analyze(context.Background(), summary, nil); err == nil {
		t.Fatal("120 ms is not in the input without a previous run, want the grounding error")
	}
}

func TestOllamaSections_InvalidNumberInAnySectionIsRejected(t *testing.T) {
	for _, reply := range []string{
		`{\"headline\":\"ok\",\"explanation\":\"Took 777 ms.\",\"next_steps\":[\"x\"]}`,
		`{\"headline\":\"ok\",\"explanation\":\"fine\",\"next_steps\":[\"Raise the timeout to 888 ms.\"]}`,
	} {
		analyzer := ollamaReplying(t, `{"message":{"content":"`+reply+`"}}`, nil)
		if _, err := analyzer.Analyze(context.Background(), Summary{P95Millis: 310}, nil); err == nil {
			t.Errorf("reply %s was accepted", reply)
		}
	}
}

func TestOllamaInput_CarriesAggregatesOnly(t *testing.T) {
	var sent string
	analyzer := ollamaReplying(t, `{"message":{"content":"{\"headline\":\"ok\",\"explanation\":\"fine\",\"next_steps\":[\"x\"]}"}}`, &sent)
	summary := BuildSummary(model.Run{
		Mode: model.ModeLoad,
		Summary: model.RunSummary{TotalRequests: 3, ErrorCount: 1, ErrorDetails: []model.ErrorDetail{
			{Cause: `Get "https://api.example.com/users?token=SECRET-TOKEN-1": context deadline exceeded`, Count: 1},
		}},
	})
	summary.Previous = &PreviousAggregates{P95Millis: 120, RequestCount: 2}
	if _, err := analyzer.Analyze(context.Background(), summary, Evaluate(summary)); err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	for _, leak := range []string{"SECRET-TOKEN-1", "api.example.com", "token="} {
		if strings.Contains(sent, leak) {
			t.Errorf("model input contains %q: %s", leak, sent)
		}
	}
	if !strings.Contains(sent, `previous`) || !strings.Contains(sent, `u003curl`) {
		t.Errorf("model input should carry the previous aggregates and normalized URLs: %s", sent)
	}
}
