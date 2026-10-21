package web

import (
	"database/sql"
	"encoding/csv"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"

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

var unsafeFilenameChars = regexp.MustCompile(`[^A-Za-z0-9_-]`)

// setAttachment marks the response as a download named primus-run-<id>.<ext>.
func setAttachment(w http.ResponseWriter, contentType, runID, ext string) {
	name := unsafeFilenameChars.ReplaceAllString(runID, "_")
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", "primus-run-"+name+"."+ext))
}

var urlInText = regexp.MustCompile(`https?://[^\s"'<>]+`)

// redactURLs reduces every URL found in free text to scheme://host/path. Query
// strings, fragments and userinfo can carry credentials, and Go transport
// errors embed the full target URL.
func redactURLs(s string) string {
	return urlInText.ReplaceAllStringFunc(s, func(raw string) string {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			return cutAt(raw, "?#")
		}
		return u.Scheme + "://" + u.Host + u.EscapedPath()
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
