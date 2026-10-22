# Collections and the built-in examples

A **collection** groups endpoints. Every endpoint belongs to exactly one; endpoints
you save without choosing one go to **My endpoints**. Open the **Collections**
panel from the sidebar to create, rename, set a base URL for, clear the history of,
or delete a collection.

## The sidebar and the collection page

The sidebar lists endpoints under their collection. Click a collection's name to
collapse or expand it; Primus remembers that after a reload. An empty collection
shows "no endpoints yet" and an **Add endpoint** button that opens the builder with
that collection already chosen.

**Overview** opens the collection page: a table with one row per endpoint showing the
verdict, p95, error rate and start time (UTC) of its latest run, and a link to that
run's report. An endpoint that has never run shows `-` in every column.

## Run all

**Run all** on the collection page runs every endpoint in the collection one after
another, each with the test config it was last run (or saved) with. Only one endpoint
runs at a time. An endpoint with no saved test config is listed as skipped, with the
reason, and the sequence moves on. Progress updates every second, with a link to each
run's report as it finishes.

**Stop all** cancels the run in progress, which is recorded as stopped, and marks the
endpoints that have not started as stopped; no further runs start. Starting **Run all**
on a collection that is already running shows the sequence in progress instead of
starting a second one. If you delete the collection or an endpoint mid-sequence, the
affected endpoints are skipped.

## Importing an OpenAPI spec

Importing a spec creates a new collection named after the spec's `info.title` (or
"Imported API" when it has none) and puts the imported endpoints in it. If a collection
with that name exists, the new one gets a number: "Pet API 2". Importing the same spec
twice therefore gives two independent copies.

## Base URL

A collection can have a base URL such as `https://api.example.com`. An endpoint
whose URL starts with `{{base_url}}` uses it, so `{{base_url}}/orders` becomes
`https://api.example.com/orders`. Change the base URL once and every such endpoint
in the collection points at the new host. The placeholder only works at the start of
the URL, and a base URL cannot hold credentials, a query string or a fragment.

## The built-in JSONPlaceholder collection

A fresh install contains a **JSONPlaceholder** collection of 21 endpoints, so there
is something to run before you have an API of your own. It covers `/posts`,
`/comments`, `/albums`, `/photos`, `/todos` and `/users`:

- list and get-by-id reads for each resource
- a 404 case, `/posts/9999`
- nested routes, `/posts/1/comments` and `/users/1/posts`
- query filters, `/comments?postId=1` and `/todos?completed=false`
- POST, PUT, PATCH and DELETE on `/posts`

Each endpoint has a saved test config: a load test of 20 requests at concurrency 2,
with assertions that match what the service really does: 200 for reads, 201 for the
create, 404 for the missing id, and a loose p95 limit. The large `/photos` list
returns about 1 MB per response, so it gets a higher latency limit. Every
endpoint has a short note in the builder explaining what it shows.

The endpoint builder does not load a saved config yet: it opens with its own form
defaults (concurrency 5, 50 requests, no assertions). Set the form to the values
above before you run an example against the live service.

The collection's base URL is `https://jsonplaceholder.typicode.com`. You can edit
the collection, its endpoints and their configs like any other; Primus never
overwrites your edits.

### The writes are fake

JSONPlaceholder pretends to accept writes. A POST returns `201` with a new id, but
nothing is stored, so a GET of that id returns `404` afterwards. PUT, PATCH and
DELETE answer `200` and change nothing. The assertions expect those status codes.

### It is someone else's service

JSONPlaceholder ([jsonplaceholder.typicode.com](https://jsonplaceholder.typicode.com))
is a free third-party service run by someone else, not by Primus. Keep the load low,
keep to the 20-request, concurrency-2 settings unless you have a reason, and only
test services you are allowed to test. For heavier experiments, point the
collection's base URL at an API of your own.

## Deleting and restoring

Deleting a collection removes its endpoints, saved configs, runs and insights, after
a confirmation that says how many there are. It is refused while a run in the
collection is active.

A deleted built-in collection stays deleted, including after a restart. To get it
back, press **Restore examples** in the Collections panel. If the collection is
already there, Primus says so and changes nothing. If you have your own collection
named JSONPlaceholder, rename it first.
