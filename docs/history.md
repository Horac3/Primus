# Run history search

The **History** list on the home page has an **Ask** box. Type a question in
plain language and the list shows only the runs that match:

- `failed burst runs last week`
- `slowest 5 runs against login`
- `soak runs with error rate above 2%`
- `p95 over 500ms in the last 3 days`
- `runs without errors yesterday`

Clear the box and press Search to get the full list back.

## Clearing history

History can be cleared for one endpoint (button in its panel), for one
collection (Collections panel), or for everything (**Clear all history** above the
list). Each asks first and says how many runs it will remove. Clearing removes the
runs and their insights; endpoints and saved test configs stay. It is refused
while a run in that scope is active, and says so if there is nothing to clear.

## How a question is read

Primus never runs a question directly. It first turns the question into a
fixed filter, then checks the filter and applies it to the stored runs itself.
The line above the results shows how the question was read and what read it:

```
Interpreted as: burst runs · failed · last 168h · newest first · up to 20 · by rules
```

If the reading is wrong, rephrase the question with the words from the table
below.

| Filter | Example words | Meaning |
|---|---|---|
| Mode | `load`, `burst`, `soak`, `rate` | Runs of that mode. Several modes can be named. |
| Outcome | `failed`, `passing`, `no failures`, `no assertions` | `failed` covers every failing verdict, including no completed requests and no successful logins. |
| Stopped | `stopped`, `cancelled`, `not stopped` | Runs ended early with Stop, or runs that were not. |
| Time | `last 30 minutes`, `last 3 days`, `past week`, `today`, `yesterday` | Runs started in that window. Windows are whole hours (30 minutes reads as the last hour); a week is 168 hours and a month is 30 days. |
| Latency | `p95 over 500ms`, `p99 > 1.5s`, `median under 80 ms` | Bounds in milliseconds. `latency` alone means p95. Runs with no completed requests have no latency, so they never match a latency bound and sort last under `fastest`. |
| Error rate | `error rate above 5%`, `error rate 5%`, `without errors`, `high error rate` | Bounds in percent of fired requests. A bare number is a minimum; `high error rate` sorts by error rate. |
| Volume | `at least 1000 requests` | Minimum fired requests. |
| Target | `against login`, `for /v1/users`, `on "Sign in"` | Text found in the endpoint name, its URL or the test config ID. Quote names that have spaces. |
| Order | `slowest`, `fastest`, `oldest`, `most errors` | Sort by p95, by start time or by error rate. Newest first by default. |
| Count | `top 5`, `10 runs` | At most this many runs, between 1 and 100. The default is 20. |

A question that matches none of these lists every run, with a note saying it
was not understood. Questions are limited to 500 characters.

## With a local model

With `PRIMUS_ANALYZER=ollama` and the `ai` compose profile (see "Local Ollama
service" on the CLI page), the local model reads the question, so phrasing can be
looser. The model fills in the same filter, and Primus rejects any field or
value outside it. The line above the results then says `by ollama`.

The model gets only the question, today's date and the description of the
filter. Run results, endpoint names and URLs, request bodies and credentials
are never sent; Primus does the matching itself.

If the model is not running, fails, takes longer than 20 seconds or returns
something outside the filter, the built-in rules answer instead, and the line
says `by rules (ollama unavailable)`.
