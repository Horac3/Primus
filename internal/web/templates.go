package web

import (
	"fmt"
	"html/template"
	"strconv"
	"strings"
	"time"

	"github.com/Horac3/Primus/internal/model"
)

// last4 returns the last 4 chars of s for masked display.
func last4(s string) string {
	if len(s) <= 4 {
		return s
	}
	return s[len(s)-4:]
}

// outcomeLabel is the verdict wording shown on the run page and in reports.
func outcomeLabel(o model.RunOutcome) string {
	switch o {
	case model.OutcomePass:
		return "✓ PASSED"
	case model.OutcomeNoAssertions:
		return "◎ Completed — no assertions configured"
	case model.OutcomeFailNoRequests:
		return "✗ FAILED — no requests completed"
	case model.OutcomeFailNoLogins:
		return "✗ FAILED — no successful logins"
	case model.OutcomeFailRateNotSustained:
		return "✗ FAILED — target rate not sustained"
	default:
		return "✗ FAILED"
	}
}

// templateFuncs are helpers available inside every template.
var templateFuncs = template.FuncMap{
	"mul":        func(a, b float64) float64 { return a * b },
	"redact":     redactURLs,
	"readingKey": readingKey,
	"reportDuration": func(r model.Run) string {
		return r.FinishedAt.Sub(r.StartedAt).Round(time.Millisecond).String()
	},
	"outcomeClass": func(o model.RunOutcome) string {
		switch o {
		case model.OutcomePass:
			return "badge-pass"
		case model.OutcomeNoAssertions:
			return "badge-neutral"
		default:
			return "badge-fail"
		}
	},
	"outcomeLabel": outcomeLabel,
	"authSummary": func(a model.AuthConfig) string {
		switch a.Type {
		case model.AuthTypeBearer:
			return "Bearer ••••" + last4(a.Token)
		case model.AuthTypeAPIKey:
			h := a.Header
			if h == "" {
				h = "key"
			}
			return "API key (" + h + ")"
		case model.AuthTypeBasic:
			return "Basic (" + a.Username + ")"
		case model.AuthTypeLoginChained:
			return "Login-chained"
		default:
			return "Not set"
		}
	},
	"bucketLabel": func(i int) string {
		return bucketLabel(i)
	},
	"requestsPerSec": func(r model.Run) string {
		secs := r.FinishedAt.Sub(r.StartedAt).Seconds()
		if secs <= 0 {
			secs = 0.001
		}
		return strconv.FormatFloat(float64(r.Summary.TotalRequests)/secs, 'f', 1, 64)
	},
	"histogramBars": func(buckets []int) []bucketBar {
		max := 0
		for _, c := range buckets {
			if c > max {
				max = c
			}
		}
		out := make([]bucketBar, 0, len(buckets))
		for i, c := range buckets {
			h := 0
			if max > 0 {
				h = (c*100 + max/2) / max
			}
			out = append(out, bucketBar{Label: bucketLabel(i), Height: h, Count: c})
		}
		return out
	},
	"latencyPolyline": func(points []model.Point) string {
		return polylineForSeries(points, func(p model.Point) float64 { return p.AvgLatencyMs }, 280, 60)
	},
	"rpsPolyline": func(points []model.Point) string {
		return polylineForSeries(points, func(p model.Point) float64 { return p.RPS }, 280, 60)
	},
	"latencyAxis": func(points []model.Point) chartAxis {
		return seriesAxis(points, func(p model.Point) float64 { return p.AvgLatencyMs })
	},
	"rpsAxis": func(points []model.Point) chartAxis {
		return seriesAxis(points, func(p model.Point) float64 { return p.RPS })
	},
	"elapsedTicks": elapsedTicks,
	"inc":          func(i int) int { return i + 1 },
}

// bucketLabel names bucket i of the fixed latency edges ("0–49", …, "≥600").
func bucketLabel(i int) string {
	edges := model.LatencyBucketEdges
	if i < 0 || i > len(edges) {
		return "?"
	}
	if i == len(edges) {
		return "≥" + strconv.Itoa(edges[len(edges)-1])
	}
	lo := 0
	if i > 0 {
		lo = edges[i-1]
	}
	return strconv.Itoa(lo) + "–" + strconv.Itoa(edges[i]-1)
}

type chartAxis struct {
	Max string
	Min string
}

type elapsedAxis struct {
	Start string
	Mid   string
	End   string
}

func seriesAxis(points []model.Point, value func(model.Point) float64) chartAxis {
	if len(points) == 0 {
		return chartAxis{}
	}
	min, max := value(points[0]), value(points[0])
	for _, point := range points[1:] {
		v := value(point)
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}
	if max == min {
		if max == 0 {
			max = 1
		} else {
			min = max - 1
		}
	}
	return chartAxis{Max: formatAxisValue(max), Min: formatAxisValue(min)}
}

func elapsedTicks(points []model.Point) elapsedAxis {
	if len(points) == 0 {
		return elapsedAxis{}
	}
	start := points[0].ElapsedMs
	end := points[len(points)-1].ElapsedMs
	mid := start + (end-start)/2
	return elapsedAxis{
		Start: formatElapsed(start),
		Mid:   formatElapsed(mid),
		End:   formatElapsed(end),
	}
}

func formatAxisValue(value float64) string {
	formatted := strconv.FormatFloat(value, 'f', 1, 64)
	formatted = strings.TrimSuffix(strings.TrimSuffix(formatted, "0"), ".")
	return formatted
}

func formatElapsed(milliseconds int64) string {
	formatted := strconv.FormatFloat(float64(milliseconds)/1000, 'f', 1, 64)
	formatted = strings.TrimSuffix(strings.TrimSuffix(formatted, "0"), ".")
	return formatted + "s"
}

func polylineForSeries(points []model.Point, value func(model.Point) float64, width, height int) string {
	if len(points) == 0 {
		return ""
	}
	min := value(points[0])
	max := min
	for _, p := range points[1:] {
		v := value(p)
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}
	if max == min {
		if max == 0 {
			max = 1
		} else {
			min = max - 1
		}
	}
	coords := make([]string, len(points))
	firstElapsed := points[0].ElapsedMs
	lastElapsed := points[len(points)-1].ElapsedMs
	for i, p := range points {
		x := 0.0
		if len(points) > 1 {
			if lastElapsed > firstElapsed {
				x = float64(p.ElapsedMs-firstElapsed) * float64(width) / float64(lastElapsed-firstElapsed)
			} else {
				x = float64(i) * float64(width) / float64(len(points)-1)
			}
		}
		y := float64(height) - ((value(p)-min)/(max-min))*float64(height)
		coords[i] = fmt.Sprintf("%g,%g", x, y)
	}
	return strings.Join(coords, " ")
}

// bucketBar is one histogram bar: Label is the ms range, Height is percent
// of the tallest bar (0-100, rendered as px), Count the raw bucket count.
type bucketBar struct {
	Label  string
	Height int
	Count  int
}
