package insight

import (
	"encoding/json"
	"testing"
	"time"
)

func TestRulesInterpreterParse(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	yes := true
	no := false
	five := 5.0
	zero := 0.0
	cases := []struct {
		question string
		want     Filter
	}{
		{"failed burst runs", Filter{Modes: []string{"burst"}, Outcome: "fail"}},
		{"Show me passing load and soak tests", Filter{Modes: []string{"load", "soak"}, Outcome: "pass"}},
		{"rate runs with error rate above 5%", Filter{Modes: []string{"rate"}, MinErrorRatePct: &five}},
		{"error rate over 5 percent", Filter{MinErrorRatePct: &five}},
		{"runs without errors", Filter{MaxErrorRatePct: &zero}},
		{"runs with no assertions", Filter{Outcome: "no_assertions"}},
		{"stopped runs", Filter{Stopped: &yes}},
		{"cancelled soak runs", Filter{Modes: []string{"soak"}, Stopped: &yes}},
		{"runs in the last 3 days", Filter{WithinHours: 72}},
		{"failures in the past week", Filter{Outcome: "fail", WithinHours: 168}},
		{"last 12 hours", Filter{WithinHours: 12}},
		{"past month", Filter{WithinHours: 720}},
		{"today", Filter{Since: "2026-10-21"}},
		{"yesterday's runs", Filter{Since: "2026-10-20", Until: "2026-10-20"}},
		{"p95 over 500ms", Filter{MinP95Millis: 500}},
		{"p99 > 1.5s", Filter{MinP99Millis: 1500}},
		{"median latency under 80 ms", Filter{MaxP50Millis: 80}},
		{"latency above 300", Filter{MinP95Millis: 300}},
		{"slowest 5 runs", Filter{Sort: SortP95Desc, Limit: 5}},
		{"fastest runs", Filter{Sort: SortP95Asc}},
		{"top 3 runs with the most errors", Filter{Sort: SortErrorRateDesc, Limit: 3}},
		{"oldest runs", Filter{Sort: SortOldest}},
		{"last 10 runs", Filter{Limit: 10}},
		{"show 500 runs", Filter{Limit: 100}},
		{"at least 1000 requests", Filter{MinTotalRequests: 1000}},
		{"burst runs against login", Filter{Modes: []string{"burst"}, Target: "login"}},
		{"runs for /v1/users in the last day", Filter{Target: "/v1/users", WithinHours: 24}},
		{`failed runs on "Sign in"`, Filter{Outcome: "fail", Target: "Sign in"}},
		{"burst runs with high error rate", Filter{Modes: []string{"burst"}, Sort: SortErrorRateDesc}},
		{"error rate of 5%", Filter{MinErrorRatePct: &five}},
		{"error rate 5%", Filter{MinErrorRatePct: &five}},
		{"runs in the last 30 minutes", Filter{WithinHours: 1}},
		{"last 2 hrs", Filter{WithinHours: 2}},
		{"past 3 wks", Filter{WithinHours: 504}},
		{"runs with no failures", Filter{Outcome: "pass"}},
		{"load runs not stopped", Filter{Modes: []string{"load"}, Stopped: &no}},
		{"runs on soak mode", Filter{Modes: []string{"soak"}}},
	}
	for _, tc := range cases {
		t.Run(tc.question, func(t *testing.T) {
			got, understood := RulesInterpreter{}.Parse(tc.question, now)
			if !understood {
				t.Fatalf("Parse(%q) not understood", tc.question)
			}
			want, err := tc.want.Validate()
			if err != nil {
				t.Fatalf("Validate want: %v", err)
			}
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(want)
			if string(gotJSON) != string(wantJSON) {
				t.Fatalf("Parse(%q) =\n%s\nwant\n%s", tc.question, gotJSON, wantJSON)
			}
		})
	}
}

func TestRulesInterpreterParseNotUnderstood(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	for _, question := range []string{"what is the meaning of life", "hello", "   "} {
		got, understood := RulesInterpreter{}.Parse(question, now)
		if understood {
			t.Errorf("Parse(%q) understood as %+v, want not understood", question, got)
		}
		if got.Describe() != "all runs · newest first · up to 20" {
			t.Errorf("Parse(%q) = %s, want an empty filter", question, got.Describe())
		}
	}
}
