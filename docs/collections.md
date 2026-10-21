# Collections and the built-in examples

A **collection** groups endpoints. Every endpoint belongs to exactly one; endpoints
you save without choosing one go to **My endpoints**. Open the **Collections**
panel from the sidebar to create, rename, set a base URL for, clear the history of,
or delete a collection.

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
