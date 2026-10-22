package insight

import (
	"fmt"

	"github.com/Horac3/Primus/internal/model"
)

// PreviousAggregates is all of an earlier run that may reach an analyzer or the comparison section:
// aggregate numbers only, never IDs, URLs, names, credentials or bodies.
type PreviousAggregates struct {
	P50Millis         int        `json:"p50_millis"`
	P95Millis         int        `json:"p95_millis"`
	P99Millis         int        `json:"p99_millis"`
	RequestCount      int        `json:"request_count"`
	RequestsPerSecond float64    `json:"requests_per_second"`
	ErrorRate         float64    `json:"error_rate"`
	Mode              model.Mode `json:"mode"`
	Concurrency       int        `json:"concurrency"`
}

// AggregatesOf projects a finished run into the baseline a later run is compared with. A run that completed
// no requests is no baseline (nil).
func AggregatesOf(run model.Run) *PreviousAggregates {
	if run.Summary.TotalRequests == 0 {
		return nil
	}
	previous := BuildSummary(run)
	return &PreviousAggregates{
		P50Millis:         previous.P50Millis,
		P95Millis:         previous.P95Millis,
		P99Millis:         previous.P99Millis,
		RequestCount:      previous.RequestCount,
		RequestsPerSecond: previous.RequestsPerSecond,
		ErrorRate:         previous.ErrorRate,
		Mode:              previous.Mode,
		Concurrency:       previous.Concurrency,
	}
}

// comparisonLines is the "compared with the previous run" section. It is written by code, not by a model,
// so every number in it is one of the two runs' aggregates (FR-INS-03). Empty when there is no baseline.
func comparisonLines(s Summary) []string {
	p := s.Previous
	if p == nil {
		return nil
	}
	lines := []string{
		compareMillis("p50", p.P50Millis, s.P50Millis),
		compareMillis("p95", p.P95Millis, s.P95Millis),
		compareMillis("p99", p.P99Millis, s.P99Millis),
		compareFormatted("Throughput", fmt.Sprintf("%.1f", p.RequestsPerSecond), fmt.Sprintf("%.1f", s.RequestsPerSecond),
			p.RequestsPerSecond, s.RequestsPerSecond, " requests per second"),
		compareFormatted("Error rate", fmt.Sprintf("%.1f%%", p.ErrorRate*100), fmt.Sprintf("%.1f%%", s.ErrorRate*100),
			p.ErrorRate, s.ErrorRate, ""),
	}
	if p.Mode != s.Mode || p.Concurrency != s.Concurrency {
		lines = append(lines, "The previous run used a different mode or concurrency, so treat this comparison with care.")
	}
	return lines
}

func compareMillis(name string, before, after int) string {
	switch {
	case after > before:
		return fmt.Sprintf("%s rose from %d ms to %d ms.", name, before, after)
	case after < before:
		return fmt.Sprintf("%s fell from %d ms to %d ms.", name, before, after)
	}
	return fmt.Sprintf("%s stayed at %d ms.", name, after)
}

// compareFormatted words a change by the displayed values, so two numbers that print the same read as "stayed".
func compareFormatted(name, beforeText, afterText string, before, after float64, unit string) string {
	switch {
	case beforeText == afterText:
		return fmt.Sprintf("%s stayed at %s%s.", name, afterText, unit)
	case after > before:
		return fmt.Sprintf("%s rose from %s to %s%s.", name, beforeText, afterText, unit)
	}
	return fmt.Sprintf("%s fell from %s to %s%s.", name, beforeText, afterText, unit)
}
