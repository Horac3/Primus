package engine

import (
	"context"
	"time"
)

// MinTargetRate is the smallest accepted target_rate (one request every
// 100s). Far smaller rates would overflow the due-time arithmetic.
const MinTargetRate = 0.01

// schedule is the open-loop dispatcher for rate mode (D-012). The n-th
// request is due at start + n/rate, anchored to the start so the schedule
// never drifts. At each due time it takes an in-flight slot without
// blocking (slots is a semaphore, one per worker, that workers free
// once a request's result is delivered) and queues the request's intended
// send time on jobs; with every slot busy the request is dropped. jobs
// must hold cap(slots) entries so a send never blocks. A late wake-up
// offers every overdue slot at once; none is skipped. Slots due at or
// after end are never offered. Closes jobs and returns the number dropped
// once the schedule ends or ctx is done. A job still queued, or taken by a
// worker after ctx is done, was never sent and is counted nowhere, so on
// Stop or the deadline released + dropped can fall a little short of the
// slots due.
func schedule(ctx context.Context, jobs chan<- time.Time, slots chan<- struct{}, rate float64, start, end time.Time) (dropped int) {
	defer close(jobs)
	timer := time.NewTimer(0)
	defer timer.Stop()
	<-timer.C
	for n := 0; ; n++ {
		due := start.Add(time.Duration(float64(n) * float64(time.Second) / rate))
		if !due.Before(end) {
			return dropped
		}
		if wait := time.Until(due); wait > 0 {
			timer.Reset(wait)
			select {
			case <-ctx.Done():
				return dropped
			case <-timer.C:
			}
		}
		if ctx.Err() != nil {
			return dropped
		}
		select {
		case slots <- struct{}{}:
			jobs <- due
		default:
			// A Stop landing after the check above leaves busy slots
			// behind; that is not a drop.
			if ctx.Err() == nil {
				dropped++
			}
		}
	}
}
