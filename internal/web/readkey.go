package web

import (
	"fmt"
	"strings"

	"github.com/Horac3/Primus/internal/model"
)

// keyEntry is one definition in the "how to read this" key.
type keyEntry struct {
	Term string
	Text string
}

// readingKey returns the fixed key shown on every report (FR-REP-02). The text is the same for every
// endpoint and never comes from a model. Login latency is defined only when a credential pool ran.
func readingKey(login *model.LoginSummary) []keyEntry {
	key := []keyEntry{
		{"p50", "Median latency. Half of the completed responses were this fast or faster. Transport errors have no latency and are left out."},
		{"p95", "95% of completed responses were this fast or faster; the slowest 5% took longer. The usual measure of what a slow request feels like."},
		{"p99", "99% of completed responses were this fast or faster; the slowest 1% took longer. It needs many requests to be steady: with a few hundred it mostly reflects one or two outliers."},
		{"Error rate", "Share of completed requests that failed: a transport error or timeout, or a status code outside the expected statuses when the assertions set them. Cancelled requests are not counted."},
		{"Throughput", "Completed requests per second over the run's wall time. In a normal load run it follows from concurrency and latency, so it is a result, not a setting."},
		{"Concurrency vs requests per second", "Concurrency is how many requests are in flight at once, not a rate. In a normal load run throughput = concurrency ÷ latency, so a slower server receives less traffic. Only fixed-rate mode sends a set number of requests per second (the target rate), and there concurrency is just the cap on requests in flight."},
		{"Cancelled", "Requests still in flight when the run was stopped or reached its end. They are counted on their own, never as errors, and have no latency."},
	}
	if login != nil {
		key = append(key, keyEntry{"Login latency", "How long each account took to log in with the active credential pool. It is measured before the measured run starts and kept apart from request latency."})
	}
	return append(key,
		keyEntry{"✓ PASSED", "Requests completed and every configured assertion held."},
		keyEntry{"✗ FAILED", "An assertion did not hold, or the run could not do its job: no request completed, no login succeeded, or a fixed-rate run dropped requests."},
		keyEntry{"◎ Completed — no assertions configured", "The run finished but nothing was asserted, so Primus makes no pass/fail claim. Add assertions to get a verdict."},
	)
}

// writeKeyMarkdown appends the key to a Markdown report.
func writeKeyMarkdown(b *strings.Builder, login *model.LoginSummary) {
	b.WriteString("\n## How to read this\n\n")
	for _, e := range readingKey(login) {
		fmt.Fprintf(b, "- **%s** — %s\n", e.Term, e.Text)
	}
}
