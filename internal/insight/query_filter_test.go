package insight

import (
	"strings"
	"testing"
	"time"

	"github.com/Horac3/Primus/internal/model"
)

func TestFilterValidateDefaultsToNewestFirstAndTwenty(t *testing.T) {
	got, err := Filter{}.Validate()
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if got.Sort != SortNewest || got.Limit != 20 {
		t.Fatalf("defaults = sort %q limit %d, want %q 20", got.Sort, got.Limit, SortNewest)
	}
}

func TestFilterValidateNormalizesCase(t *testing.T) {
	got, err := Filter{Modes: []string{" Burst "}, Outcome: "FAIL", Sort: "P95_Desc"}.Validate()
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(got.Modes) != 1 || got.Modes[0] != "burst" || got.Outcome != "fail" || got.Sort != SortP95Desc {
		t.Fatalf("normalized = %+v", got)
	}
}

func TestFilterValidateRejectsInvalidValues(t *testing.T) {
	negative := -1.0
	tooHigh := 101.0
	cases := []struct {
		name   string
		filter Filter
		want   string
	}{
		{"unknown mode", Filter{Modes: []string{"spike"}}, "mode"},
		{"unknown outcome", Filter{Outcome: "maybe"}, "outcome"},
		{"unknown sort", Filter{Sort: "random"}, "sort"},
		{"limit too high", Filter{Limit: 101}, "limit"},
		{"negative limit", Filter{Limit: -1}, "limit"},
		{"negative p95", Filter{MinP95Millis: -5}, "p95"},
		{"inverted p95", Filter{MinP95Millis: 500, MaxP95Millis: 100}, "p95"},
		{"negative error rate", Filter{MinErrorRatePct: &negative}, "error rate"},
		{"error rate over 100", Filter{MaxErrorRatePct: &tooHigh}, "error rate"},
		{"negative total", Filter{MinTotalRequests: -1}, "total"},
		{"negative window", Filter{WithinHours: -1}, "within_hours"},
		{"window and dates", Filter{WithinHours: 24, Since: "2026-10-01"}, "within_hours"},
		{"bad since date", Filter{Since: "last week"}, "since"},
		{"until before since", Filter{Since: "2026-10-10", Until: "2026-10-01"}, "until"},
		{"long target", Filter{Target: strings.Repeat("x", 201)}, "target"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.filter.Validate()
			if err == nil {
				t.Fatalf("Validate(%+v) succeeded, want error mentioning %q", tc.filter, tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

func queryTestRuns(now time.Time) ([]model.Run, []model.TestConfig, []model.RequestDefinition) {
	runs := []model.Run{
		{ID: "load-pass", TestConfigID: "cfg-users", Mode: model.ModeLoad, Outcome: model.OutcomePass,
			StartedAt: now.Add(-2 * time.Hour), Summary: model.RunSummary{P50Millis: 40, P95Millis: 120, P99Millis: 200, ErrorRate: 0, TotalRequests: 1000}},
		{ID: "burst-fail", TestConfigID: "cfg-login", Mode: model.ModeBurst, Outcome: model.OutcomeFail,
			StartedAt: now.Add(-30 * time.Hour), Summary: model.RunSummary{P50Millis: 300, P95Millis: 900, P99Millis: 1500, ErrorRate: 0.2, TotalRequests: 500}},
		{ID: "burst-nologin", TestConfigID: "cfg-login", Mode: model.ModeBurst, Outcome: model.OutcomeFailNoLogins,
			StartedAt: now.Add(-10 * 24 * time.Hour), Summary: model.RunSummary{TotalRequests: 0}},
		{ID: "soak-stopped", TestConfigID: "cfg-users", Mode: model.ModeSoak, Outcome: model.OutcomeNoAssertions, Stopped: true,
			StartedAt: now.Add(-5 * time.Hour), Summary: model.RunSummary{P50Millis: 60, P95Millis: 600, P99Millis: 700, ErrorRate: 0.01, TotalRequests: 50}},
		// Older runs stored no Mode; the config's mode applies.
		{ID: "legacy-load", TestConfigID: "cfg-legacy", Outcome: model.OutcomePass,
			StartedAt: now.Add(-50 * time.Hour), Summary: model.RunSummary{P95Millis: 300, TotalRequests: 10}},
	}
	configs := []model.TestConfig{
		{ID: "cfg-users", RequestID: "req-users", Mode: model.ModeLoad},
		{ID: "cfg-login", RequestID: "req-login", Mode: model.ModeBurst},
		{ID: "cfg-legacy", RequestID: "req-missing", Mode: model.ModeLoad},
	}
	requests := []model.RequestDefinition{
		{ID: "req-users", Name: "List users", URLTemplate: "https://api.example.test/v1/users"},
		{ID: "req-login", Name: "Sign in", URLTemplate: "https://api.example.test/v1/auth/login"},
	}
	return runs, configs, requests
}

func runIDs(runs []model.Run) string {
	ids := make([]string, len(runs))
	for i, run := range runs {
		ids[i] = run.ID
	}
	return strings.Join(ids, ",")
}

func TestFilterApply(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	runs, configs, requests := queryTestRuns(now)
	yes := true
	five := 5.0
	zero := 0.0
	cases := []struct {
		name   string
		filter Filter
		want   string
	}{
		{"no filter lists newest first", Filter{}, "load-pass,soak-stopped,burst-fail,legacy-load,burst-nologin"},
		{"mode", Filter{Modes: []string{"burst"}}, "burst-fail,burst-nologin"},
		{"mode falls back to config", Filter{Modes: []string{"load"}}, "load-pass,legacy-load"},
		{"fail covers every fail outcome", Filter{Outcome: "fail"}, "burst-fail,burst-nologin"},
		{"pass", Filter{Outcome: "pass"}, "load-pass,legacy-load"},
		{"no assertions", Filter{Outcome: "no_assertions"}, "soak-stopped"},
		{"stopped", Filter{Stopped: &yes}, "soak-stopped"},
		{"within hours", Filter{WithinHours: 24}, "load-pass,soak-stopped"},
		{"since date", Filter{Since: "2026-10-19"}, "load-pass,soak-stopped,burst-fail,legacy-load"},
		{"until date is inclusive", Filter{Until: "2026-10-20"}, "burst-fail,legacy-load,burst-nologin"},
		{"p95 minimum", Filter{MinP95Millis: 500}, "soak-stopped,burst-fail"},
		{"p95 maximum skips runs without requests", Filter{MaxP95Millis: 300}, "load-pass,legacy-load"},
		{"p50 and p99", Filter{MinP50Millis: 50, MaxP99Millis: 1000}, "soak-stopped"},
		{"error rate minimum in percent", Filter{MinErrorRatePct: &five}, "burst-fail"},
		{"error-free runs", Filter{MaxErrorRatePct: &zero, MinTotalRequests: 1}, "load-pass,legacy-load"},
		{"target matches request name", Filter{Target: "sign IN"}, "burst-fail,burst-nologin"},
		{"target matches request url", Filter{Target: "/v1/users"}, "load-pass,soak-stopped"},
		{"target matches config id", Filter{Target: "cfg-legacy"}, "legacy-load"},
		{"sort oldest", Filter{Modes: []string{"burst"}, Sort: SortOldest}, "burst-nologin,burst-fail"},
		{"sort slowest", Filter{Sort: SortP95Desc, Limit: 2}, "burst-fail,soak-stopped"},
		{"sort fastest puts runs without requests last", Filter{Sort: SortP95Asc}, "load-pass,legacy-load,soak-stopped,burst-fail,burst-nologin"},
		{"sort error rate", Filter{Sort: SortErrorRateDesc, Limit: 1}, "burst-fail"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			filter, err := tc.filter.Validate()
			if err != nil {
				t.Fatalf("Validate: %v", err)
			}
			got := runIDs(filter.Apply(runs, configs, requests, now))
			if got != tc.want {
				t.Fatalf("Apply = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestFilterDescribe(t *testing.T) {
	yes := true
	two := 2.5
	cases := []struct {
		filter Filter
		want   string
	}{
		{Filter{}, "all runs · newest first · up to 20"},
		{Filter{Modes: []string{"burst", "soak"}, Outcome: "fail", WithinHours: 168, MinP95Millis: 500},
			"burst or soak runs · failed · last 168h · p95 ≥ 500 ms · newest first · up to 20"},
		{Filter{Outcome: "no_assertions", Stopped: &yes, Since: "2026-10-01", Until: "2026-10-07", MaxP99Millis: 900,
			MaxErrorRatePct: &two, MinTotalRequests: 100, Target: "login", Sort: SortP95Desc, Limit: 5},
			"all runs · no assertions · stopped · from 2026-10-01 · to 2026-10-07 · p99 ≤ 900 ms · error rate ≤ 2.5% · ≥ 100 requests · target contains \"login\" · slowest p95 first · up to 5"},
	}
	for _, tc := range cases {
		filter, err := tc.filter.Validate()
		if err != nil {
			t.Fatalf("Validate: %v", err)
		}
		if got := filter.Describe(); got != tc.want {
			t.Errorf("Describe =\n%s\nwant\n%s", got, tc.want)
		}
	}
}
