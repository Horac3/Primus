package engine

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Horac3/Primus/internal/model"
)

// ============================================================
// Public helper: Percentiles
// ============================================================

// Percentiles returns the p50, p95, and p99 of a slice of millisecond samples.
// The slice is sorted in place. Returns 0 for all if the slice is empty.
func Percentiles(samples []int) (p50, p95, p99 int) {
	if len(samples) == 0 {
		return 0, 0, 0
	}
	sort.Ints(samples)
	p50 = samples[percentileIdx(len(samples), 50)]
	p95 = samples[percentileIdx(len(samples), 95)]
	p99 = samples[percentileIdx(len(samples), 99)]
	return
}

// percentileIdx returns the 0-based index for the given percentile.
func percentileIdx(n, pct int) int {
	idx := (n*pct+99)/100 - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= n {
		idx = n - 1
	}
	return idx
}

// ============================================================
// Public helper: Bucketize
// ============================================================

// Bucketize counts samples into the fixed model.LatencyBucketEdges buckets
// plus a final >= last-edge overflow bucket. Counts sum to len(samples).
func Bucketize(samples []int) []int {
	edges := model.LatencyBucketEdges
	buckets := make([]int, len(edges)+1)
	for _, s := range samples {
		placed := false
		for i, e := range edges {
			if s < e {
				buckets[i]++
				placed = true
				break
			}
		}
		if !placed {
			buckets[len(buckets)-1]++
		}
	}
	return buckets
}

func storeSeriesSnapshot(snapshot *atomic.Pointer[[]model.Point], series []model.Point) {
	if snapshot == nil {
		return
	}
	clone := append([]model.Point(nil), series...)
	snapshot.Store(&clone)
}

func emitSeriesPoint(
	start time.Time,
	windowRequests int,
	windowSuccesses int,
	windowLatencyMS float64,
	windowErrors int,
	series *[]model.Point,
	snapshot *atomic.Pointer[[]model.Point],
) {
	elapsedMs := time.Since(start).Milliseconds()
	point := model.Point{ElapsedMs: elapsedMs}
	if windowRequests > 0 {
		point.RPS = float64(windowRequests) / 1.0
		if windowErrors > 0 {
			point.ErrorRate = float64(windowErrors) / float64(windowRequests)
		}
	}
	if windowSuccesses > 0 {
		point.AvgLatencyMs = windowLatencyMS / float64(windowSuccesses)
	}
	*series = append(*series, point)
	storeSeriesSnapshot(snapshot, *series)
}

// aggregate consumes all results and builds a RunSummary plus the latency
// bucket histogram. Buckets cover completed responses; transport errors
// have no latency sample.
func aggregate(results <-chan result, assertions model.Assertions, start time.Time, snapshot *atomic.Pointer[[]model.Point]) (model.RunSummary, []int, []model.Point) {
	statusCounts := make(map[int]int)
	errorCauses := make(map[string]int)
	var latencies []int
	var errorCount int
	cancelled := 0
	total := 0
	windowRequests := 0
	windowSuccesses := 0
	windowLatencyTotalMs := 0.0
	windowErrors := 0
	series := make([]model.Point, 0)
	allowed := make(map[int]bool, len(assertions.ExpectedStatus))
	for _, code := range assertions.ExpectedStatus {
		allowed[code] = true
	}

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case r, ok := <-results:
			if !ok {
				if total > 0 || len(series) > 0 {
					emitSeriesPoint(start, windowRequests, windowSuccesses, windowLatencyTotalMs, windowErrors, &series, snapshot)
				}
				goto finalize
			}
			if r.cancelled {
				cancelled++
				continue
			}
			total++
			windowRequests++
			if r.err != nil {
				errorCount++
				windowErrors++
				errorCauses["Request failed: "+r.err.Error()]++
				continue
			}
			statusCounts[r.statusCode]++
			latencies = append(latencies, int(r.latency.Milliseconds()))
			windowSuccesses++
			windowLatencyTotalMs += float64(r.latency.Milliseconds())
			if len(allowed) > 0 && !allowed[r.statusCode] {
				errorCount++
				windowErrors++
				expected := make([]string, len(assertions.ExpectedStatus))
				for i, code := range assertions.ExpectedStatus {
					expected[i] = strconv.Itoa(code)
				}
				cause := fmt.Sprintf("Unexpected HTTP status %d (expected: %s)", r.statusCode, strings.Join(expected, ", "))
				errorCauses[cause]++
			}
		case <-ticker.C:
			if windowRequests == 0 {
				continue
			}
			point := model.Point{
				ElapsedMs:    time.Since(start).Milliseconds(),
				RPS:          float64(windowRequests) / 1.0,
				AvgLatencyMs: 0,
				ErrorRate:    0,
			}
			if windowSuccesses > 0 {
				point.AvgLatencyMs = windowLatencyTotalMs / float64(windowSuccesses)
			}
			if windowRequests > 0 {
				point.ErrorRate = float64(windowErrors) / float64(windowRequests)
			}
			series = append(series, point)
			storeSeriesSnapshot(snapshot, series)
			windowRequests = 0
			windowSuccesses = 0
			windowLatencyTotalMs = 0
			windowErrors = 0
		}
	}

finalize:
	p50, p95, p99 := Percentiles(latencies)
	var errorRate float64
	if total > 0 {
		errorRate = float64(errorCount) / float64(total)
	}
	errorDetails := make([]model.ErrorDetail, 0, len(errorCauses))
	for cause, count := range errorCauses {
		errorDetails = append(errorDetails, model.ErrorDetail{Cause: cause, Count: count})
	}
	sort.Slice(errorDetails, func(i, j int) bool {
		return errorDetails[i].Cause < errorDetails[j].Cause
	})

	return model.RunSummary{
		StatusCounts:  statusCounts,
		ErrorCount:    errorCount,
		ErrorDetails:  errorDetails,
		P50Millis:     p50,
		P95Millis:     p95,
		P99Millis:     p99,
		ErrorRate:     errorRate,
		TotalRequests: total,
		Cancelled:     cancelled,
	}, Bucketize(latencies), series
}

// buildRun assembles the final Run from a summary.
