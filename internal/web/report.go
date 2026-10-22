package web

import (
	"bytes"
	"database/sql"
	"encoding/csv"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Horac3/Primus/internal/insight"
	"github.com/Horac3/Primus/internal/model"
	"github.com/Horac3/Primus/internal/store"
)

// ---- GET /runs/{id}/series.csv — time series download ----------------------

func seriesCSVHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		run, err := store.GetRun(db, r.PathValue("id"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		setAttachment(w, "text/csv; charset=utf-8", run.ID, "csv")
		cw := csv.NewWriter(w)
		_ = cw.Write([]string{"elapsed_ms", "avg_latency_ms", "rps", "error_rate"})
		for _, p := range run.TimeSeries {
			_ = cw.Write([]string{
				strconv.FormatInt(p.ElapsedMs, 10),
				strconv.FormatFloat(p.AvgLatencyMs, 'f', -1, 64),
				strconv.FormatFloat(p.RPS, 'f', -1, 64),
				strconv.FormatFloat(p.ErrorRate, 'f', -1, 64),
			})
		}
		cw.Flush()
		if err := cw.Error(); err != nil {
			log.Printf("series csv for run %s: %v", run.ID, err)
		}
	}
}

// ---- GET /runs/{id}/report.md — markdown summary --------------------------

// reportData is everything a report states about a run. It holds no request
// URL query, header, body or credential: the endpoint is already reduced.
type reportData struct {
	model.Run
	Endpoint string
	Insight  *store.InsightRecord
	// Sections is nil for an insight saved before sections existed; reports then print its text.
	Sections *insight.Sections
}

func buildReportData(db *sql.DB, run model.Run) reportData {
	rd := reportData{Run: run, Endpoint: endpointLabel(db, run.TestConfigID)}
	if rec, err := store.GetInsight(db, run.ID); err == nil {
		rd.Insight = &rec
		rd.Sections = decodeSections(rec)
	}
	return rd
}

// endpointLabel names the tested endpoint as "METHOD scheme://host/path".
// Query, fragment and userinfo are dropped; a missing config or request gives
// "endpoint deleted".
func endpointLabel(db *sql.DB, configID string) string {
	cfg, err := store.GetTestConfig(db, configID)
	if err != nil {
		return "endpoint deleted"
	}
	req, err := store.GetRequestDefinition(db, cfg.RequestID)
	if err != nil {
		return "endpoint deleted"
	}
	return req.Method + " " + redactURLs(cutAt(req.URLTemplate, "?#"))
}

func reportMarkdownHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		run, err := store.GetRun(db, r.PathValue("id"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		setAttachment(w, "text/markdown; charset=utf-8", run.ID, "md")
		if _, err := io.WriteString(w, renderMarkdown(buildReportData(db, run))); err != nil {
			log.Printf("markdown report for run %s: %v", run.ID, err)
		}
	}
}

// ---- GET /runs/{id}/report.html — self-contained html report ---------------

func reportHTMLHandler(db *sql.DB, tmpl *template.Template) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		run, err := store.GetRun(db, r.PathValue("id"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		var buf bytes.Buffer
		if err := tmpl.ExecuteTemplate(&buf, "report.html", buildReportData(db, run)); err != nil {
			log.Printf("html report for run %s: %v", run.ID, err)
			http.Error(w, "could not render report", http.StatusInternalServerError)
			return
		}
		setAttachment(w, "text/html; charset=utf-8", run.ID, "html")
		if _, err := w.Write(buf.Bytes()); err != nil {
			log.Printf("html report for run %s: %v", run.ID, err)
		}
	}
}

// mdCell makes free text safe inside a Markdown table cell.
func mdCell(s string) string {
	return strings.NewReplacer(`\`, `\\`, "|", `\|`, "\r", " ", "\n", " ").Replace(redactURLs(s))
}

func renderMarkdown(rd reportData) string {
	var b strings.Builder
	s := rd.Summary
	fmt.Fprintf(&b, "# Primus run %s\n\n", rd.ID)
	fmt.Fprintf(&b, "**Verdict:** %s\n\n", outcomeLabel(rd.Outcome))
	if rd.Stopped {
		b.WriteString("_The run was stopped before reaching its own stop condition._\n\n")
	}
	b.WriteString("| | |\n|---|---|\n")
	fmt.Fprintf(&b, "| Endpoint | %s |\n", mdCell(rd.Endpoint))
	fmt.Fprintf(&b, "| Mode | %s |\n", rd.Mode)
	fmt.Fprintf(&b, "| Concurrency | %d |\n", rd.Concurrency)
	fmt.Fprintf(&b, "| Started | %s |\n", rd.StartedAt.UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "| Finished | %s |\n", rd.FinishedAt.UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "| Duration | %s |\n\n", rd.FinishedAt.Sub(rd.StartedAt).Round(time.Millisecond))

	b.WriteString("## Metrics\n\n| Metric | Value |\n|---|---|\n")
	fmt.Fprintf(&b, "| Total requests | %d |\n", s.TotalRequests)
	fmt.Fprintf(&b, "| Errors | %d (%.2f%%) |\n", s.ErrorCount, s.ErrorRate*100)
	fmt.Fprintf(&b, "| p50 | %d ms |\n| p95 | %d ms |\n| p99 | %d ms |\n", s.P50Millis, s.P95Millis, s.P99Millis)
	fmt.Fprintf(&b, "| Cancelled | %d |\n", s.Cancelled)
	if rd.Mode == model.ModeRate {
		fmt.Fprintf(&b, "| Dropped | %d |\n| Target rate | %.1f req/s |\n| Achieved rate | %.1f req/s |\n",
			s.Dropped, s.TargetRate, s.AchievedRate)
	}
	b.WriteString("\n")

	if len(s.StatusCounts) > 0 {
		codes := make([]int, 0, len(s.StatusCounts))
		for code := range s.StatusCounts {
			codes = append(codes, code)
		}
		sort.Ints(codes)
		b.WriteString("## Status codes\n\n| Status | Count |\n|---|---|\n")
		for _, code := range codes {
			fmt.Fprintf(&b, "| %d | %d |\n", code, s.StatusCounts[code])
		}
		b.WriteString("\n")
	}

	b.WriteString("## Assertions\n\n")
	if len(rd.AssertionResults) == 0 {
		b.WriteString("No assertions configured.\n\n")
	} else {
		b.WriteString("| Assertion | Result |\n|---|---|\n")
		for _, a := range rd.AssertionResults {
			result := "✗ failed"
			if a.Passed {
				result = "✓ passed"
			}
			fmt.Fprintf(&b, "| %s | %s |\n", mdCell(a.Name), result)
		}
		b.WriteString("\n")
	}

	if len(s.ErrorDetails) > 0 {
		b.WriteString("## Failure causes\n\n| Cause | Count |\n|---|---|\n")
		for _, e := range s.ErrorDetails {
			fmt.Fprintf(&b, "| %s | %d |\n", mdCell(e.Cause), e.Count)
		}
		b.WriteString("\n")
	}

	if l := rd.Login; l != nil {
		b.WriteString("## Logins\n\n")
		fmt.Fprintf(&b, "Pool %s: %d attempted, %d succeeded, %d failed.\n\n", mdCell(l.PoolName), l.Attempted, l.Succeeded, l.Failed)
		if len(l.FailureReasons) > 0 {
			b.WriteString("| Login failure | Count |\n|---|---|\n")
			for _, e := range l.FailureReasons {
				fmt.Fprintf(&b, "| %s | %d |\n", mdCell(e.Cause), e.Count)
			}
			b.WriteString("\n")
		}
	}

	switch {
	case rd.Insight != nil && rd.Insight.Status == store.InsightDone:
		who := rd.Insight.Analyzer
		if rd.Insight.Model != "" {
			who += " / " + rd.Insight.Model
		}
		if s := rd.Sections; s != nil {
			fmt.Fprintf(&b, "## Insight (%s)\n\n", who)
			if s.FallbackFrom != "" {
				fmt.Fprintf(&b, "Written by the rules analyzer because %s failed or gave an answer that did not match the run's numbers.\n\n", s.FallbackFrom)
			}
			writeInsightList(&b, "What the numbers say", s.Numbers)
			writeInsightList(&b, "Likely causes", s.Causes)
			writeInsightList(&b, "What to try next", s.NextSteps)
			if len(s.Comparison) > 0 {
				writeInsightList(&b, "Compared with the previous run", s.Comparison)
			}
		} else {
			fmt.Fprintf(&b, "## Insight (%s)\n\n%s\n", who, redactURLs(rd.Insight.Text))
		}
	default:
		b.WriteString("## Insight\n\nNo insight generated.\n")
	}
	writeKeyMarkdown(&b, rd.Login)
	return b.String()
}

func writeInsightList(b *strings.Builder, heading string, lines []string) {
	fmt.Fprintf(b, "### %s\n\n", heading)
	for _, line := range lines {
		fmt.Fprintf(b, "- %s\n", redactURLs(line))
	}
	b.WriteString("\n")
}

var unsafeFilenameChars = regexp.MustCompile(`[^A-Za-z0-9_-]`)

// setAttachment marks the response as a download named primus-run-<id>.<ext>.
func setAttachment(w http.ResponseWriter, contentType, runID, ext string) {
	name := unsafeFilenameChars.ReplaceAllString(runID, "_")
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", "primus-run-"+name+"."+ext))
}

var urlInText = regexp.MustCompile(`(?i)https?://[^\s"'<>]+`)

// redactURLs reduces every URL found in free text to scheme://host/path. Query
// strings, fragments and userinfo can carry credentials, and Go transport and
// request-parse errors embed the URL as typed. It works on the text rather
// than url.Parse, because a URL that fails to parse must be redacted too.
func redactURLs(s string) string {
	return urlInText.ReplaceAllStringFunc(s, func(raw string) string {
		schemeEnd := strings.Index(raw, "://") + len("://")
		rest := cutAt(raw[schemeEnd:], "?#")
		authority, path := rest, ""
		if i := strings.IndexByte(rest, '/'); i >= 0 {
			authority, path = rest[:i], rest[i:]
		}
		if i := strings.LastIndexByte(authority, '@'); i >= 0 {
			authority = authority[i+1:]
		}
		return raw[:schemeEnd] + authority + path
	})
}

// cutAt drops everything from the first occurrence of any byte in seps.
func cutAt(s, seps string) string {
	for i := 0; i < len(s); i++ {
		for j := 0; j < len(seps); j++ {
			if s[i] == seps[j] {
				return s[:i]
			}
		}
	}
	return s
}
