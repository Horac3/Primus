package insight

import (
	"strings"
	"testing"

	"github.com/Horac3/Primus/internal/model"
)

func TestComparison_NoPreviousMeansNoSection(t *testing.T) {
	got := analyzeRules(t, Summary{RequestCount: 10, P95Millis: 310}, nil)
	if len(got.Sections.Comparison) != 0 {
		t.Errorf("Comparison = %v, want none with one run", got.Sections.Comparison)
	}
}

func TestComparison_WordsEachMetricByDirection(t *testing.T) {
	cur := Summary{
		Mode: model.ModeLoad, Concurrency: 10, RequestCount: 100, RequestsPerSecond: 40, ErrorRate: 0.05,
		P50Millis: 90, P95Millis: 310, P99Millis: 500,
		Previous: &PreviousAggregates{
			Mode: model.ModeLoad, Concurrency: 10, RequestCount: 100, RequestsPerSecond: 80, ErrorRate: 0.05,
			P50Millis: 90, P95Millis: 120, P99Millis: 900,
		},
	}
	joined := strings.Join(analyzeRules(t, cur, nil).Sections.Comparison, "\n")
	for _, want := range []string{
		"p95 rose from 120 ms to 310 ms",
		"p99 fell from 900 ms to 500 ms",
		"p50 stayed at 90 ms",
		"Throughput fell from 80.0 to 40.0 requests per second",
		"Error rate stayed at 5.0%",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("Comparison missing %q in:\n%s", want, joined)
		}
	}
}

func TestComparison_CaveatWhenModeOrConcurrencyDiffer(t *testing.T) {
	cur := Summary{Mode: model.ModeBurst, Concurrency: 50, RequestCount: 1,
		Previous: &PreviousAggregates{Mode: model.ModeLoad, Concurrency: 10, RequestCount: 1}}
	joined := strings.Join(analyzeRules(t, cur, nil).Sections.Comparison, "\n")
	if !strings.Contains(joined, "different mode or concurrency") {
		t.Errorf("Comparison = %q, want a caveat", joined)
	}
	same := Summary{Mode: model.ModeLoad, Concurrency: 10, RequestCount: 1,
		Previous: &PreviousAggregates{Mode: model.ModeLoad, Concurrency: 10, RequestCount: 1}}
	if strings.Contains(strings.Join(analyzeRules(t, same, nil).Sections.Comparison, "\n"), "different mode") {
		t.Error("caveat shown for a like-for-like comparison")
	}
}

func TestAggregatesOf_SkipsRunsWithoutCompletedRequests(t *testing.T) {
	if AggregatesOf(model.Run{}) != nil {
		t.Error("a run with no completed requests must not be a comparison baseline")
	}
	run := model.Run{Mode: model.ModeLoad, Concurrency: 4}
	run.Summary.TotalRequests = 20
	run.Summary.P95Millis = 120
	got := AggregatesOf(run)
	if got == nil || got.P95Millis != 120 || got.RequestCount != 20 || got.Concurrency != 4 {
		t.Errorf("AggregatesOf = %+v", got)
	}
}
