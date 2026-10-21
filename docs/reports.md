# Run reports

Every finished run can be downloaded as a file. Open the run page (`/runs/<id>`,
or click a run in History) and use the **Download** links under the run ID.

| File | URL | What it is |
|---|---|---|
| HTML report | `/runs/<id>/report.html` | One self-contained file you can email or attach to a ticket. It opens offline: inline styles and charts, no scripts, no links to Primus. |
| Markdown summary | `/runs/<id>/report.md` | The same facts as tables, without charts. Paste it into a pull request or an issue. |
| CSV time series | `/runs/<id>/series.csv` | One row per time slice: `elapsed_ms,avg_latency_ms,rps,error_rate`. A run with no series gets the header row only. |

## What the report contains

- The verdict, with the same wording as the run page: passed, failed (and why),
  or completed with no assertions configured.
- The endpoint as `METHOD scheme://host/path`, or `endpoint deleted` if the
  request was removed after the run.
- Mode, concurrency, start time and duration.
- Latency percentiles (p50, p95, p99), requests, error count and rate, and
  cancelled requests. Rate-mode runs also show target rate, achieved rate and
  dropped requests.
- Status code counts, failure causes and assertions.
- The login summary when a credential pool ran.
- The time-series charts and latency buckets (HTML only).
- The insight and which analyzer wrote it (`rules` or the Ollama model), or
  `No insight generated` if you never asked for one. Generate the insight first
  on the run page if you want it in the file.

## What is left out

Reports never contain a URL query string, credentials in a URL, headers,
request bodies, global auth, or credential pool values. URLs inside failure
causes are cut down to `scheme://host/path`, because the underlying network
error text includes the full address. The run page applies the same rule.

Reports are built when you download them, from the stored run. Nothing is
saved on disk, and runs stored by earlier versions are covered too.
