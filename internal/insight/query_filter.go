package insight

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Horac3/Primus/internal/model"
)

// Sort orders for a history query.
const (
	SortNewest        = "newest"
	SortOldest        = "oldest"
	SortP95Desc       = "p95_desc"
	SortP95Asc        = "p95_asc"
	SortErrorRateDesc = "error_rate_desc"
)

// Outcome groups for a history query. OutcomeGroupFail covers every fail_* outcome.
const (
	OutcomeGroupPass         = "pass"
	OutcomeGroupFail         = "fail"
	OutcomeGroupNoAssertions = "no_assertions"
)

const (
	defaultQueryLimit = 20
	maxQueryLimit     = 100
	maxWithinHours    = 24 * 366 * 5
	maxTargetLength   = 200
	queryDateLayout   = "2006-01-02"
)

var querySorts = []string{SortNewest, SortOldest, SortP95Desc, SortP95Asc, SortErrorRateDesc}
var queryOutcomes = []string{OutcomeGroupPass, OutcomeGroupFail, OutcomeGroupNoAssertions}
var queryModes = []string{string(model.ModeLoad), string(model.ModeBurst), string(model.ModeSoak), string(model.ModeRate)}

// Filter is a structured run-history query. Its JSON shape is the schema a model
// fills in; Primus validates it and applies it itself. Zero values mean "no bound".
type Filter struct {
	Modes            []string `json:"modes,omitempty"`
	Outcome          string   `json:"outcome,omitempty"`
	Stopped          *bool    `json:"stopped,omitempty"`
	WithinHours      int      `json:"within_hours,omitempty"`
	Since            string   `json:"since,omitempty"`
	Until            string   `json:"until,omitempty"`
	MinP50Millis     int      `json:"min_p50_ms,omitempty"`
	MaxP50Millis     int      `json:"max_p50_ms,omitempty"`
	MinP95Millis     int      `json:"min_p95_ms,omitempty"`
	MaxP95Millis     int      `json:"max_p95_ms,omitempty"`
	MinP99Millis     int      `json:"min_p99_ms,omitempty"`
	MaxP99Millis     int      `json:"max_p99_ms,omitempty"`
	MinErrorRatePct  *float64 `json:"min_error_rate_pct,omitempty"`
	MaxErrorRatePct  *float64 `json:"max_error_rate_pct,omitempty"`
	MinTotalRequests int      `json:"min_total_requests,omitempty"`
	Target           string   `json:"target,omitempty"`
	Sort             string   `json:"sort,omitempty"`
	Limit            int      `json:"limit,omitempty"`
}

// Validate returns a normalized copy with defaults applied, or an error naming
// the first invalid field.
func (f Filter) Validate() (Filter, error) {
	out := f
	out.Modes = nil
	for _, mode := range f.Modes {
		mode = strings.ToLower(strings.TrimSpace(mode))
		if !contains(queryModes, mode) {
			return Filter{}, fmt.Errorf("unknown mode %q", mode)
		}
		if !contains(out.Modes, mode) {
			out.Modes = append(out.Modes, mode)
		}
	}
	out.Outcome = strings.ToLower(strings.TrimSpace(f.Outcome))
	if out.Outcome != "" && !contains(queryOutcomes, out.Outcome) {
		return Filter{}, fmt.Errorf("unknown outcome %q", f.Outcome)
	}
	out.Sort = strings.ToLower(strings.TrimSpace(f.Sort))
	if out.Sort == "" {
		out.Sort = SortNewest
	}
	if !contains(querySorts, out.Sort) {
		return Filter{}, fmt.Errorf("unknown sort %q", f.Sort)
	}
	if f.Limit == 0 {
		out.Limit = defaultQueryLimit
	}
	if out.Limit < 1 || out.Limit > maxQueryLimit {
		return Filter{}, fmt.Errorf("limit %d is outside 1-%d", f.Limit, maxQueryLimit)
	}
	for _, bound := range []struct {
		name     string
		min, max int
	}{
		{"p50", f.MinP50Millis, f.MaxP50Millis},
		{"p95", f.MinP95Millis, f.MaxP95Millis},
		{"p99", f.MinP99Millis, f.MaxP99Millis},
	} {
		if bound.min < 0 || bound.max < 0 {
			return Filter{}, fmt.Errorf("%s bounds must not be negative", bound.name)
		}
		if bound.max > 0 && bound.min > bound.max {
			return Filter{}, fmt.Errorf("%s minimum %d is above maximum %d", bound.name, bound.min, bound.max)
		}
	}
	for _, pct := range []*float64{f.MinErrorRatePct, f.MaxErrorRatePct} {
		if pct != nil && (*pct < 0 || *pct > 100) {
			return Filter{}, fmt.Errorf("error rate %g%% is outside 0-100", *pct)
		}
	}
	if f.MinErrorRatePct != nil && f.MaxErrorRatePct != nil && *f.MinErrorRatePct > *f.MaxErrorRatePct {
		return Filter{}, fmt.Errorf("error rate minimum is above maximum")
	}
	if f.MinTotalRequests < 0 {
		return Filter{}, fmt.Errorf("min total requests must not be negative")
	}
	if f.WithinHours < 0 || f.WithinHours > maxWithinHours {
		return Filter{}, fmt.Errorf("within_hours %d is outside 0-%d", f.WithinHours, maxWithinHours)
	}
	out.Since = strings.TrimSpace(f.Since)
	out.Until = strings.TrimSpace(f.Until)
	if f.WithinHours > 0 && (out.Since != "" || out.Until != "") {
		return Filter{}, fmt.Errorf("within_hours cannot be combined with since or until")
	}
	since, err := parseQueryDate("since", out.Since)
	if err != nil {
		return Filter{}, err
	}
	until, err := parseQueryDate("until", out.Until)
	if err != nil {
		return Filter{}, err
	}
	if !since.IsZero() && !until.IsZero() && until.Before(since) {
		return Filter{}, fmt.Errorf("until %s is before since %s", out.Until, out.Since)
	}
	out.Target = strings.TrimSpace(f.Target)
	if len(out.Target) > maxTargetLength {
		return Filter{}, fmt.Errorf("target is longer than %d characters", maxTargetLength)
	}
	return out, nil
}

func parseQueryDate(field, value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(queryDateLayout, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s %q is not a YYYY-MM-DD date", field, value)
	}
	return parsed, nil
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

// Apply returns the runs that match the filter, sorted and limited. The filter
// must come from Validate. now anchors within_hours; since and until are whole
// days in now's location, until inclusive. Runs saved without a mode use their
// config's mode. Target matches the config ID, request name or request URL.
func (f Filter) Apply(runs []model.Run, configs []model.TestConfig, requests []model.RequestDefinition, now time.Time) []model.Run {
	configByID := make(map[string]model.TestConfig, len(configs))
	for _, config := range configs {
		configByID[config.ID] = config
	}
	requestByID := make(map[string]model.RequestDefinition, len(requests))
	for _, request := range requests {
		requestByID[request.ID] = request
	}
	from, to := f.window(now)
	target := strings.ToLower(f.Target)

	matched := make([]model.Run, 0, len(runs))
	for _, run := range runs {
		config := configByID[run.TestConfigID]
		mode := run.Mode
		if mode == "" {
			mode = config.Mode
		}
		if len(f.Modes) > 0 && !contains(f.Modes, string(mode)) {
			continue
		}
		if f.Outcome != "" && outcomeGroup(run.Outcome) != f.Outcome {
			continue
		}
		if f.Stopped != nil && run.Stopped != *f.Stopped {
			continue
		}
		if !from.IsZero() && run.StartedAt.Before(from) {
			continue
		}
		if !to.IsZero() && !run.StartedAt.Before(to) {
			continue
		}
		// A run with no requests has no latency, so no latency bound matches it.
		if f.hasLatencyBound() && run.Summary.TotalRequests == 0 {
			continue
		}
		if !withinMillis(run.Summary.P50Millis, f.MinP50Millis, f.MaxP50Millis) ||
			!withinMillis(run.Summary.P95Millis, f.MinP95Millis, f.MaxP95Millis) ||
			!withinMillis(run.Summary.P99Millis, f.MinP99Millis, f.MaxP99Millis) {
			continue
		}
		errorPct := run.Summary.ErrorRate * 100
		if f.MinErrorRatePct != nil && errorPct < *f.MinErrorRatePct {
			continue
		}
		if f.MaxErrorRatePct != nil && errorPct > *f.MaxErrorRatePct {
			continue
		}
		if run.Summary.TotalRequests < f.MinTotalRequests {
			continue
		}
		if target != "" {
			request := requestByID[config.RequestID]
			haystack := strings.ToLower(run.TestConfigID + "\n" + request.Name + "\n" + request.URLTemplate)
			if !strings.Contains(haystack, target) {
				continue
			}
		}
		matched = append(matched, run)
	}

	sort.SliceStable(matched, func(i, j int) bool {
		return matched[i].StartedAt.After(matched[j].StartedAt)
	})
	switch f.Sort {
	case SortOldest:
		sort.SliceStable(matched, func(i, j int) bool { return matched[i].StartedAt.Before(matched[j].StartedAt) })
	case SortP95Desc:
		sort.SliceStable(matched, func(i, j int) bool { return matched[i].Summary.P95Millis > matched[j].Summary.P95Millis })
	case SortP95Asc:
		// Runs with no requests have p95 0 but are not fast; they go last.
		sort.SliceStable(matched, func(i, j int) bool {
			iEmpty, jEmpty := matched[i].Summary.TotalRequests == 0, matched[j].Summary.TotalRequests == 0
			if iEmpty != jEmpty {
				return jEmpty
			}
			return matched[i].Summary.P95Millis < matched[j].Summary.P95Millis
		})
	case SortErrorRateDesc:
		sort.SliceStable(matched, func(i, j int) bool { return matched[i].Summary.ErrorRate > matched[j].Summary.ErrorRate })
	}
	if f.Limit > 0 && len(matched) > f.Limit {
		matched = matched[:f.Limit]
	}
	return matched
}

func (f Filter) window(now time.Time) (from, to time.Time) {
	if f.WithinHours > 0 {
		return now.Add(-time.Duration(f.WithinHours) * time.Hour), time.Time{}
	}
	if f.Since != "" {
		from, _ = time.ParseInLocation(queryDateLayout, f.Since, now.Location())
	}
	if f.Until != "" {
		day, _ := time.ParseInLocation(queryDateLayout, f.Until, now.Location())
		to = day.AddDate(0, 0, 1)
	}
	return from, to
}

func (f Filter) hasLatencyBound() bool {
	return f.MinP50Millis > 0 || f.MaxP50Millis > 0 || f.MinP95Millis > 0 || f.MaxP95Millis > 0 ||
		f.MinP99Millis > 0 || f.MaxP99Millis > 0
}

func withinMillis(value, min, max int) bool {
	return value >= min && (max == 0 || value <= max)
}

func outcomeGroup(outcome model.RunOutcome) string {
	switch {
	case outcome == model.OutcomePass:
		return OutcomeGroupPass
	case outcome == model.OutcomeNoAssertions:
		return OutcomeGroupNoAssertions
	case strings.HasPrefix(string(outcome), "fail"):
		return OutcomeGroupFail
	}
	return ""
}

var sortLabels = map[string]string{
	SortNewest:        "newest first",
	SortOldest:        "oldest first",
	SortP95Desc:       "slowest p95 first",
	SortP95Asc:        "fastest p95 first",
	SortErrorRateDesc: "highest error rate first",
}

var outcomeLabels = map[string]string{
	OutcomeGroupPass:         "passed",
	OutcomeGroupFail:         "failed",
	OutcomeGroupNoAssertions: "no assertions",
}

// Describe renders a validated filter as one readable line for the UI.
func (f Filter) Describe() string {
	parts := []string{"all runs"}
	if len(f.Modes) > 0 {
		parts[0] = strings.Join(f.Modes, " or ") + " runs"
	}
	if f.Outcome != "" {
		parts = append(parts, outcomeLabels[f.Outcome])
	}
	if f.Stopped != nil {
		if *f.Stopped {
			parts = append(parts, "stopped")
		} else {
			parts = append(parts, "not stopped")
		}
	}
	if f.WithinHours > 0 {
		parts = append(parts, fmt.Sprintf("last %dh", f.WithinHours))
	}
	if f.Since != "" {
		parts = append(parts, "from "+f.Since)
	}
	if f.Until != "" {
		parts = append(parts, "to "+f.Until)
	}
	for _, bound := range []struct {
		name     string
		min, max int
	}{
		{"p50", f.MinP50Millis, f.MaxP50Millis},
		{"p95", f.MinP95Millis, f.MaxP95Millis},
		{"p99", f.MinP99Millis, f.MaxP99Millis},
	} {
		if bound.min > 0 {
			parts = append(parts, fmt.Sprintf("%s ≥ %d ms", bound.name, bound.min))
		}
		if bound.max > 0 {
			parts = append(parts, fmt.Sprintf("%s ≤ %d ms", bound.name, bound.max))
		}
	}
	if f.MinErrorRatePct != nil {
		parts = append(parts, fmt.Sprintf("error rate ≥ %g%%", *f.MinErrorRatePct))
	}
	if f.MaxErrorRatePct != nil {
		parts = append(parts, fmt.Sprintf("error rate ≤ %g%%", *f.MaxErrorRatePct))
	}
	if f.MinTotalRequests > 0 {
		parts = append(parts, fmt.Sprintf("≥ %d requests", f.MinTotalRequests))
	}
	if f.Target != "" {
		parts = append(parts, fmt.Sprintf("target contains %q", f.Target))
	}
	parts = append(parts, sortLabels[f.Sort], fmt.Sprintf("up to %d", f.Limit))
	return strings.Join(parts, " · ")
}
