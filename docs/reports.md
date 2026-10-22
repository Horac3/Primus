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
- A **How to read this** key at the end. See below.

## How to read this

Every report, and the run page, ends with the same key. It defines:

- **p50, p95, p99**: the latency that 50%, 95% and 99% of completed responses
  stayed under. Transport errors have no latency and are left out.
- **Error rate**: the share of completed requests that failed, either a
  transport error or timeout, or a status outside the expected statuses when
  your assertions set them. Cancelled requests are not counted.
- **Throughput**: completed requests per second over the run.
- **Concurrency vs requests per second**: concurrency is how many requests are
  in flight at once, not a rate. In a normal load run throughput is
  concurrency ÷ latency, so a slower server gets less traffic. Only fixed-rate
  mode sends a set number of requests per second, and there concurrency is the
  cap on requests in flight.
- **Cancelled**: requests still in flight when the run was stopped or ended.
  They are never counted as errors.
- **Login latency**: how long each account took to log in. It appears only when
  a [credential pool](credential-pools.md) ran, and is kept apart from request
  latency.
- **The three verdicts**: ✓ PASSED, ✗ FAILED, and ◎ Completed — no assertions
  configured.

The key is fixed text. It is the same for every endpoint and no model writes it.

## Per-endpoint report

Open a collection and click **endpoint report** beside an endpoint. The page
shows:

- The method, the URL without its query string, and the collection.
- The latest run: verdict, requests, p50/p95/p99, error rate, throughput and
  cancelled requests, with a link to the full run.
- A table of the last 10 runs, newest first, so you can see a regression
  appear: time, requests, p50, p95, p99, error %, throughput, cancelled and
  verdict.
- The latest run's findings: the insight if you generated one, or a button to
  generate it.
- The key above.

An endpoint with no runs shows **no runs yet** and a **Run** button that opens
its builder. The numbers and the table come straight from stored runs; a model
only ever writes the findings text.

## What is left out

Reports never contain a URL query string, credentials in a URL, headers,
request bodies, global auth, or credential pool values. URLs inside failure
causes are cut down to `scheme://host/path`, because the underlying network
error text includes the full address. The run page applies the same rule.

Reports are built when you download them, from the stored run. Nothing is
saved on disk, and runs stored by earlier versions are covered too.
