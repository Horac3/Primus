package insight

import (
	"context"
	"strings"
	"testing"

	"github.com/Horac3/Primus/internal/model"
)

func analyzeRules(t *testing.T, s Summary, f []Finding) Insight {
	t.Helper()
	got, err := (RulesAnalyzer{}).Analyze(context.Background(), s, f)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	return got
}

func TestRulesSections_NumbersCoverCountsAndPercentiles(t *testing.T) {
	s := Summary{
		Mode: model.ModeLoad, RequestCount: 500, RequestsPerSecond: 62.5, ErrorCount: 5, ErrorRate: 0.01,
		P50Millis: 80, P95Millis: 310, P99Millis: 540,
	}
	got := analyzeRules(t, s, nil)
	joined := strings.Join(got.Sections.Numbers, "\n")
	for _, want := range []string{"500 requests", "62.5 requests per second", "p50 80 ms", "p95 310 ms", "p99 540 ms", "5 errors", "1.0%"} {
		if !strings.Contains(joined, want) {
			t.Errorf("Numbers missing %q in:\n%s", want, joined)
		}
	}
}

func TestRulesSections_CausesAndStepsFollowFindings(t *testing.T) {
	findings := []Finding{
		{Code: "CLIENT_TIMEOUT", Severity: SeverityCritical, Values: map[string]any{"error_count": 7}},
		{Code: "NO_ASSERTIONS", Severity: SeverityInfo, Values: map[string]any{}},
	}
	got := analyzeRules(t, Summary{RequestCount: 10}, findings)
	causes := strings.Join(got.Sections.Causes, "\n")
	if !strings.Contains(causes, "All 7 failed requests ended because the client deadline expired.") {
		t.Errorf("Causes = %q, want the timeout sentence", causes)
	}
	if strings.Contains(causes, "No assertions") {
		t.Errorf("info finding listed as a cause: %q", causes)
	}
	steps := strings.Join(got.Sections.NextSteps, "\n")
	if !strings.Contains(steps, "timeout") || !strings.Contains(steps, "assertion") {
		t.Errorf("NextSteps = %q, want a step for the timeout and for assertions", steps)
	}
}

func TestRulesSections_NoFindingsUseFixedLines(t *testing.T) {
	got := analyzeRules(t, Summary{RequestCount: 10, AssertionsConfigured: true}, nil)
	if len(got.Sections.Causes) != 1 || got.Sections.Causes[0] != "Nothing in these numbers points to a problem." {
		t.Errorf("Causes = %v", got.Sections.Causes)
	}
	if len(got.Sections.NextSteps) != 1 {
		t.Errorf("NextSteps = %v, want one neutral step", got.Sections.NextSteps)
	}
}

func TestRulesSections_NumbersMentionLoginsOnlyWhenPoolRan(t *testing.T) {
	without := analyzeRules(t, Summary{RequestCount: 1}, nil)
	if strings.Contains(strings.Join(without.Sections.Numbers, " "), "login") {
		t.Errorf("Numbers mention logins without a pool: %v", without.Sections.Numbers)
	}
	with := analyzeRules(t, Summary{RequestCount: 1, Logins: &LoginCounts{Attempted: 10, Succeeded: 9, Failed: 1}}, nil)
	if !strings.Contains(strings.Join(with.Sections.Numbers, " "), "9 of 10 logins") {
		t.Errorf("Numbers = %v, want a logins line", with.Sections.Numbers)
	}
}
