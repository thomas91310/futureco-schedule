---
name: Schedule HTTP API
overview: Add a versioned net/http JSON API on the existing Postgres schema, verified only against the local Docker database. Leave Vercel and Neon e2e as TODO checkboxes in the README, and do not write or run those tests.
todos:
  - id: scaffold
    content: Add go.mod, .gitignore, main.go, pgx pool (MaxConns 3 with a comment on why it is low), /api/health, and /api/v1 routes
    status: completed
  - id: schedule
    content: Implement slot generation and parameterized pgx queries with constraint-to-status mapping
    status: completed
  - id: handlers
    content: JSON handlers for available times, create, and cursor-paged list; error status_code equals the HTTP status for that response
    status: completed
  - id: observe
    content: slog JSON, Prometheus HTTP and pool metrics, OTel spans, alerts file, optional Pyroscope
    status: completed
  - id: swagger
    content: swag annotations, swag init, /swagger/ UI, README regen steps
    status: completed
  - id: tests
    content: Table-driven unit tests and Docker Postgres integration tests. Do not add e2e Go tests
    status: completed
  - id: readme
    content: Document local API run, routes, tests, pool size, and a TODO checklist for the later Vercel and Neon e2e flow
    status: completed
isProject: false
---

# Schedule HTTP API

There is no Go code yet. [`schema.sql`](schema.sql) already fixes the domain: every appointment is 30 minutes, `started_at` falls on a Unix half-hour, and `(trainer_id, started_at)` is unique. The JSON API will use the names you asked for (`starts_at`, `ends_at`) and map them to `started_at` / `ended_at`. That also matches the API skill’s rule that the HTTP shape should not copy the table.

## Conflicts where repo rules win

- Errors stay flat: `{"status_code": <code>, "error": "<message>"}`. `<code>` is that response’s HTTP status, not a fixed 400. Invalid input is 400, a missing trainer or user is 404, a taken slot is 409, an unexpected failure is 500, and a failed health check is 503. The REST skill’s nested `error.code` / `details` object and **422** are not used. Swagger documents one `ErrorResponse` and a separate `@Failure` for each status, with an example whose `status_code` matches that status.
- Tests do not mock the database. Pure logic is a normal unit test. SQL and handlers run as `//go:build integration` tests against the Docker Postgres in [`compose.yaml`](compose.yaml) (`localhost:5433`). That local database is the only database this work connects to. The testing skill’s build tag still applies, so `go test -race $(go list ./... | grep -v /e2e)` stays fast and does not need Docker.
- The `pgx` pool uses `MaxConns` **3**. The database skill’s example of 25 open connections is for one long-running server. This API is a Vercel function: each isolate opens its own pool, and the e2e skill keeps that pool small so many isolates do not exhaust Neon through the pooler. The number and that reason sit in a comment next to the pool config, and the README repeats it.
- In-memory rate limits are not added. They do not hold across Vercel isolates, and there is no shared store. Availability windows are capped at 31 days instead.
- PostHog, Grafana, and public pprof are not wired in. Metrics, alert PromQL, and the Grafana dashboard IDs from the observability skill are documented so they can be attached later. pprof stays off the public mux.

## Routes

`main.go` listens on `PORT` (default `8080`) and reads `DATABASE_URL`. [`scripts/e2e.sh`](scripts/e2e.sh) already warms up `GET /api/health`, so that path stays unversioned and runs `SELECT 1`. The schedule routes are under `/api/v1`.

```mermaid
flowchart LR
  client[Client]
  mux[net/http mux]
  slots[Slot generator]
  store[pgx store]
  db[(Postgres)]
  client --> mux
  mux --> slots
  mux --> store
  slots --> store
  store --> db
```

- `GET /api/health` — 200 `{"status_code":200,"status":"ok"}` when the database answers. When it does not, 503 and `{"status_code":503,"error":"..."}`.
- `GET /api/v1/trainers/{trainer_id}/available-times?starts_at=&ends_at=` — unbooked slots.
- `POST /api/v1/appointments` — create one 30-minute appointment. 201, `Location: /api/v1/appointments/{id}`.
- `GET /api/v1/trainers/{trainer_id}/appointments?limit=&cursor=` — that trainer’s bookings, oldest first.

Success bodies include `status_code` equal to the HTTP status. Error bodies use the same number in `status_code` and a short `error` string, so a client can branch on 400, 404, 409, 500, or 503 without parsing the message. The local integration tests assert the HTTP status and that body field for each of those cases. The same checks are README TODOs for a later Vercel run, not Go files in `e2e/` yet.

**Available times.** `starts_at` and `ends_at` are RFC3339. A slot is returned when it starts on a half-hour, lasts 30 minutes, starts at or after `starts_at`, ends at or before `ends_at`, falls in business hours, and no appointment exists for that trainer at that start. Business hours are Monday–Friday, 8:00am–5:00pm America/Los_Angeles: the slot must start at or after 8:00am and end at or before 5:00pm, so the last start of the day is 4:30pm. Weekends and times outside that range are omitted, and an empty list is still 200. Unknown trainer is 404. Missing times, `ends_at` before `starts_at`, or a window longer than 31 days is 400. Booked starts are loaded with one parameterized query on `(trainer_id, started_at)`; the free slots are computed in Go so the rule can be unit-tested.

**Create.** Body is `trainer_id`, `user_id`, `starts_at`, `ends_at`. Reject in the handler unless the span is exactly 30 minutes and the start’s Unix time is divisible by 1800 (400), and unless it falls in those Pacific business hours (400). One `INSERT`. Postgres `23505` on `(trainer_id, started_at)` is 409. `23503` (missing trainer or user) is 404. `23514` is 400. Anything else from the database is 500. Each of those responses sets `status_code` to that same number. The unique index is the concurrency control, so this is not wrapped in a multi-statement transaction. This is an API rule, not a new check constraint: `appointments.json` already contains Saturday rows, and a check would reject the local load.

**List.** Unknown trainer is 404. `limit` defaults to 50 and caps at 100. The cursor is the opaque `(started_at, id)` position from the REST skill’s cursor example, queried as a keyset (`>` the cursor, `ORDER BY started_at, id`) so the database skill’s “avoid OFFSET” rule holds. Response is `items`, `next_cursor`, and `has_more`.

## Code layout

- [`main.go`](main.go), `go.mod`, `.gitignore` — module `futureco-schedule`, `pgxpool`, route wiring, `PORT`. Ignore the built binary, coverage output, and `.env`. Commit generated `docs/`.
- `internal/schedule` — slot math and the `pgx` queries (`Query` / `Exec` with `context`, `pgx.CollectRows`, `defer rows` close via collect, errors wrapped, no string-built SQL).
- `internal/httpapi` — handlers, the shared error writer, `httptest` coverage.
- `internal/observability` — logger, metrics, tracer.
- `e2e/` — left as it is (`e2e/e2e-skill.md`, `e2e/e2e.yml`). No new Go tests in this pass.

## Docs

Follow [`golang-swagger`](.agents/skills/golang-swagger/SKILL.md): general info on `main.go` (`@title`, `@version`, `@BasePath /api/v1`), `@Summary` / `@Param` / `@Success` / `@Failure` / `@Router` on each handler, named request and response structs (body params are structs, not maps). `swag init` writes `docs/`, imported as `_ "futureco-schedule/docs"`. UI at `/swagger/` via `github.com/swaggo/http-swagger`. No `@Security` (no auth). README records `go install github.com/swaggo/swag/cmd/swag@latest` and re-running `swag init` after annotation changes.

## Observability

Follow [`golang-observability`](.agents/skills/golang-observability/SKILL.md) for the three signals this process can actually emit:

- JSON `slog` to stdout at Info (`LOG_FORMAT=text` for local text logs). `slog.*Context` only. Log a request failure once in HTTP middleware; lower layers return wrapped errors and do not also log them. No trainer or user names in logs.
- `go.opentelemetry.io/contrib/bridges/otelslog` so log lines carry `trace_id`.
- Prometheus counter `futureco_http_requests_total{method,route,status}` and histogram `futureco_http_request_duration_seconds{method,route}` with `prometheus.DefBuckets`. `route` is the mux pattern, never the raw path or an id. Pool gauges from `pgxpool.Stat()`. PromQL and alert comments sit above each metric. `GET /metrics` uses `promhttp`.
- OpenTelemetry tracer started in `main` before serving. `otelhttp` wraps the mux. Spans on each store method and the slot calculation; errors call `span.RecordError` and `SetStatus`. OTLP gRPC export only when `OTEL_EXPORTER_OTLP_ENDPOINT` is set, with `ParentBased(TraceIDRatioBased(0.1))`. Otherwise a no-op provider so local and Vercel boot without a collector.
- `observability/alerts.yml` holds the 5xx-ratio and P99 rules from those comments. README points at Grafana dashboards 21221, 6671, and 10826 for the default Go runtime metrics. `PROFILING_ENABLED=true` may start Pyroscope; `/debug/pprof` is not mounted.

## Tests

- Unit, table-driven, `t.Parallel()`, testify `assert` created inside each subtest: slot generation (alignment, window edges, booked subtraction, 31-day cap) and handler cases that fail before any query (bad JSON, bad window, wrong duration). `goleak.VerifyTestMain` on that package. One `Example` for a free slot list.
- Integration (`go test -tags=integration ./internal/...`): insert a trainer, user, and appointment in the local database, then assert available times omit that slot, create returns 201 then 409 on the same slot, unknown trainer is 404, and the list cursor pages in `started_at` order. Each test truncates only the rows it created, inside a transaction that rolls back, so it does not depend on `appointments.json` and does not wipe the loaded dataset.
- No `e2e` Go tests in this pass. [`scripts/e2e.sh`](scripts/e2e.sh) and [`e2e/e2e.yml`](e2e/e2e.yml) stay as they are. The workflow file is not copied into `.github/workflows/`. Nothing deploys to Vercel, creates a Neon branch, or calls `scripts/e2e.sh`.

Before finishing: `go test -race $(go list ./... | grep -v /e2e)` and, with Docker Postgres up, `go test -race -tags=integration ./internal/...`. `golangci-lint` is run only if the binary is already installed. The `golang-lint` skill is not installed, so no lint config is added.

## README

Document local development only: `scripts/setup.sh`, then the API against `postgres://futureco:futureco@localhost:5433/futureco?sslmode=disable`. Include the three routes, the error shape with `status_code` set to the real HTTP status, Swagger, and the unit and integration test commands. Explain `MaxConns = 3`: a later Vercel deploy runs many short-lived isolates, each with its own pool, and a larger pool per isolate would exhaust Neon even though the database skill’s long-running example uses 25. The local URL is the documented dev default, not a Neon secret.

Add a `## TODO` section with unchecked boxes for the later Vercel and Neon flow. Do not implement these items:

- Add `e2e` Go tests that read `BASE_URL`, send `x-vercel-protection-bypass` when set, use a client timeout of at least 20s, and avoid depending on existing rows.
- Assert HTTP status and body `status_code` for `GET /api/health`, create-then-list, available times after a booking, 404 for a missing trainer, 400 for invalid input, and 409 for a taken slot.
- Copy [`e2e/e2e.yml`](e2e/e2e.yml) to `.github/workflows/e2e.yml` after checking each Action’s current major version. Keep it on PRs to `main` and manual dispatch, never on fork PRs.
- Run the flow only through [`scripts/e2e.sh`](scripts/e2e.sh) against a temporary Neon branch and a Vercel preview. Do not run it as part of local development or the unit test command.