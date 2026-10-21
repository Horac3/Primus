package engine

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/Horac3/Primus/internal/model"
)

// ============================================================
// Public helper: ValidateTestConfig
// ============================================================

// ValidateTestConfig rejects configs that set both or neither of the two
// mutually exclusive stop conditions (TotalRequests vs Duration), and
// non-positive concurrency. Callers must validate before running.
func ValidateTestConfig(cfg model.TestConfig) error {
	if cfg.Concurrency < 1 {
		return fmt.Errorf("concurrency must be >= 1 (got %d)", cfg.Concurrency)
	}
	countSet := cfg.TotalRequests > 0
	durSet := cfg.Duration > 0
	if countSet == durSet {
		return fmt.Errorf("set exactly one of total_requests (%d) or duration (%s), not both/neither",
			cfg.TotalRequests, cfg.Duration)
	}
	// Written as !(>= 0) so NaN is rejected too.
	if !(cfg.MaxRate >= 0) || math.IsInf(cfg.MaxRate, 0) {
		return fmt.Errorf("max_rate must be a finite number >= 0 (got %g)", cfg.MaxRate)
	}
	if cfg.MaxRate > 0 && cfg.MaxRate < MinMaxRate {
		return fmt.Errorf("max_rate must be 0 (no cap) or at least %g (got %g)", MinMaxRate, cfg.MaxRate)
	}
	if cfg.MaxRate > 0 && cfg.Mode != model.ModeAuthProbe {
		return fmt.Errorf("max_rate applies to auth_probe only (mode %s)", cfg.Mode)
	}
	if cfg.MaxRate > 0 && cfg.BurstPattern != nil {
		return fmt.Errorf("max_rate cannot be combined with a burst_pattern")
	}
	return validateTargetRate(cfg)
}

// validateTargetRate checks the rate-mode fields: a finite TargetRate > 0
// and a Duration, never alongside a burst_pattern. TargetRate is rejected
// in every other mode.
func validateTargetRate(cfg model.TestConfig) error {
	if cfg.Mode != model.ModeRate {
		if cfg.TargetRate != 0 {
			return fmt.Errorf("target_rate applies to rate mode only (mode %s)", cfg.Mode)
		}
		return nil
	}
	if !(cfg.TargetRate >= MinTargetRate) || math.IsInf(cfg.TargetRate, 0) {
		return fmt.Errorf("target_rate must be a finite number >= %g (got %g)", MinTargetRate, cfg.TargetRate)
	}
	if cfg.Duration <= 0 {
		return fmt.Errorf("rate mode needs a duration, not total_requests")
	}
	if cfg.BurstPattern != nil {
		return fmt.Errorf("target_rate cannot be combined with a burst_pattern")
	}
	return nil
}

// ============================================================
// Public helper: CheckAssertions
// ============================================================

// CheckAssertions returns one AssertionResult per configured assertion.
// Entries mirror EvaluateAssertions item-by-item; the overall pass/fail
// (including the 100%-failure baseline) stays with DetermineOutcome.
func CheckAssertions(a model.Assertions, s model.RunSummary) []model.AssertionResult {
	var out []model.AssertionResult
	if len(a.ExpectedStatus) > 0 {
		allowed := make(map[int]bool, len(a.ExpectedStatus))
		for _, code := range a.ExpectedStatus {
			allowed[code] = true
		}
		passed := len(s.StatusCounts) > 0
		for code := range s.StatusCounts {
			if !allowed[code] {
				passed = false
				break
			}
		}
		names := make([]string, 0, len(a.ExpectedStatus))
		for _, code := range a.ExpectedStatus {
			names = append(names, strconv.Itoa(code))
		}
		out = append(out, model.AssertionResult{
			Name:   "Status code: " + strings.Join(names, ", "),
			Passed: passed,
		})
	}
	if a.MaxP95Millis > 0 {
		out = append(out, model.AssertionResult{
			Name:   fmt.Sprintf("Latency (p95) < %dms", a.MaxP95Millis),
			Passed: !(s.P95Millis > a.MaxP95Millis),
		})
	}
	if a.MaxErrorRate > 0 {
		out = append(out, model.AssertionResult{
			Name:   "Error rate < " + strconv.FormatFloat(a.MaxErrorRate*100, 'f', -1, 64) + "%",
			Passed: !(s.ErrorRate > a.MaxErrorRate),
		})
	}
	return out
}

// ============================================================
// Public helper: EvaluateAssertions
// ============================================================

// noRequestsCompleted reports whether the run recorded no HTTP response at
// all: nothing was dispatched (e.g. stopped early) or every request was a
// transport error. Such a run can never pass, whatever the assertions say
// (0 errors out of 0 requests must not satisfy a max error rate).
func noRequestsCompleted(s model.RunSummary) bool {
	for _, count := range s.StatusCounts {
		if count > 0 {
			return false
		}
	}
	return true
}

// EvaluateAssertions returns true if all configured assertions hold.
// Baseline rule: if no request completed (no status codes recorded), it
// always returns false regardless of configured assertions.
func EvaluateAssertions(a model.Assertions, s model.RunSummary) bool {
	if noRequestsCompleted(s) {
		return false
	}
	// Error rate
	if a.MaxErrorRate > 0 && s.ErrorRate > a.MaxErrorRate {
		return false
	}
	// P95
	if a.MaxP95Millis > 0 && s.P95Millis > a.MaxP95Millis {
		return false
	}
	// Expected status codes
	if len(a.ExpectedStatus) > 0 {
		allowed := make(map[int]bool, len(a.ExpectedStatus))
		for _, code := range a.ExpectedStatus {
			allowed[code] = true
		}
		for code := range s.StatusCounts {
			if !allowed[code] {
				return false
			}
		}
	}
	return true
}

// DetermineOutcome returns the tri-state RunOutcome for a completed run.
func DetermineOutcome(a model.Assertions, s model.RunSummary) model.RunOutcome {
	// Baseline: no completed request overrides everything.
	if noRequestsCompleted(s) {
		return model.OutcomeFailNoRequests
	}
	// A rate run that could not release every due request did not hold its
	// target, whatever the assertions say.
	if s.Dropped > 0 {
		return model.OutcomeFailRateNotSustained
	}
	// No assertions configured and at least one success → neutral.
	hasAssertions := a.MaxErrorRate > 0 || a.MaxP95Millis > 0 || len(a.ExpectedStatus) > 0
	if !hasAssertions {
		return model.OutcomeNoAssertions
	}
	if EvaluateAssertions(a, s) {
		return model.OutcomePass
	}
	return model.OutcomeFail
}
