package web

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Horac3/Primus/internal/model"
)

func TestRegistryRunningSnapshot_GrowsMonotonically(t *testing.T) {
	// Arrange
	reg := NewRegistry(func(r model.Run) error { return nil })
	id := "live-run"
	handle := &runHandle{done: make(chan struct{})}

	go func() {
		reg.startWithHandle(context.Background(), id, handle, func(ctx context.Context, h *runHandle) (model.Run, error) {
			points := []model.Point{
				{ElapsedMs: 1000, AvgLatencyMs: 50, RPS: 2, ErrorRate: 0.1},
				{ElapsedMs: 2000, AvgLatencyMs: 60, RPS: 3, ErrorRate: 0.08},
			}
			h.snapshot.Store(&points)
			return model.Run{ID: id, TimeSeries: points}, nil
		})
	}()

	// Act
	var points []model.Point
	for i := 0; i < 50; i++ {
		time.Sleep(5 * time.Millisecond)
		snapshot := handle.snapshot.Load()
		if snapshot != nil {
			points = *snapshot
			break
		}
	}

	// Assert
	if len(points) == 0 {
		t.Fatal("snapshot did not become available while run was still active")
	}
	for i := 1; i < len(points); i++ {
		if points[i].ElapsedMs <= points[i-1].ElapsedMs {
			t.Fatalf("points not monotonic: got %+v", points)
		}
	}
}

func TestPolylineForSeries_ScalesKnownValues(t *testing.T) {
	// Arrange
	series := []model.Point{
		{AvgLatencyMs: 0},
		{AvgLatencyMs: 100},
	}

	// Act
	got := polylineForSeries(series, func(p model.Point) float64 { return p.AvgLatencyMs }, 100, 50)

	// Assert
	if got != "0,50 100,0" {
		t.Fatalf("got %q, want %q", got, "0,50 100,0")
	}
}

func TestPolylineForSeries_UsesElapsedTime(t *testing.T) {
	// Arrange
	series := []model.Point{
		{ElapsedMs: 1000, AvgLatencyMs: 10},
		{ElapsedMs: 2000, AvgLatencyMs: 20},
		{ElapsedMs: 5000, AvgLatencyMs: 30},
	}

	// Act
	got := polylineForSeries(series, func(p model.Point) float64 { return p.AvgLatencyMs }, 280, 60)

	// Assert
	if got != "0,60 70,30 280,0" {
		t.Fatalf("got %q, want elapsed-time-scaled points", got)
	}
}

func TestTimeSeriesTemplateLabelsUnitsAndElapsedTime(t *testing.T) {
	// Arrange
	run := model.Run{TimeSeries: []model.Point{
		{ElapsedMs: 1000, AvgLatencyMs: 25, RPS: 4},
		{ElapsedMs: 3000, AvgLatencyMs: 75, RPS: 8},
		{ElapsedMs: 5000, AvgLatencyMs: 50, RPS: 6},
	}}
	var output bytes.Buffer

	// Act
	if err := loadEmbeddedTemplates().ExecuteTemplate(&output, "timeseries", run); err != nil {
		t.Fatalf("execute timeseries template: %v", err)
	}

	// Assert
	for _, want := range []string{"Average latency (ms)", "Requests per second (req/s)", "Elapsed time", "25", "75", "1s", "3s", "5s"} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("timeseries output missing %q", want)
		}
	}
}
