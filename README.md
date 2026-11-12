# futureco-schedule

Local setup uses Docker Postgres.
There has been a planning effort to do a end to end test using Vercel + Neon but ended up depreciating this to a TODO (bottom of the readme).

## Requirements

- Docker with Compose v2
- `python3` on the host (used by the load script)
- Go 1.26. The Prometheus client requires it. If your `go` binary is older, the toolchain downloads 1.26 automatically.

## Start the database and load data

From the repo root:

```bash
scripts/setup.sh
```

That builds the Postgres 18 image, starts it, waits until it is healthy, and loads `appointments.json`. The container keeps running.

The image applies `schema.sql` only the first time the data volume is empty. Later starts keep the existing data.

## Connect

Host port `5433` maps to port `5432` inside the container, so this database can run next to another Postgres already bound to `5432`.

```text
postgres://futureco:futureco@localhost:5433/futureco?sslmode=disable
```

A shell inside the container:

```bash
docker compose exec postgres psql -U futureco -d futureco
```

## Load script

`scripts/load_appointments.sh` loads `appointments.json` into the running database. `scripts/setup.sh` calls it after the database is healthy. Run it on its own only when the database is already up and `appointments` is empty.

The script exits with an error when `appointments` already has rows. It does not delete or truncate.

`appointments.json` has no names or birth dates. The loader inserts one trainer and one user row for each distinct id:

- trainers: first name `Trainer`, last name equal to the id, date of birth `1980-01-01`
- users: first name `User`, last name equal to the id, date of birth `1990-01-01`

Each stored appointment is 30 minutes and starts on a UTC half-hour. A source span that is an exact multiple of 30 minutes is stored as consecutive 30-minute rows with the same trainer and user. The first slice keeps the JSON id. Each extra slice gets the next id after the highest id in the file. The 60-minute row (`13:00`–`14:00`, trainer 3, user 9) becomes `13:00`–`13:30` (id 9) and `13:30`–`14:00` (id 11).

After a successful load this file produces 3 trainers, 10 users, and 11 appointments.

## Reset the local database

This deletes the local Docker volume and the loaded rows:

```bash
docker compose down -v
scripts/setup.sh
```

## Later schema changes

`schema.sql` is the full schema a new volume gets. Files under `migrations/` are mounted at `/migrations` in the container for later deltas. Init does not re-run them on a volume that already has data. Apply a later change to an existing database with golang-migrate against the local URL, and update `schema.sql` so a new volume matches.

## Run the API

Start the database first (`scripts/setup.sh`), then from the repo root:

```bash
export DATABASE_URL='postgres://futureco:futureco@localhost:5433/futureco?sslmode=disable'
go run .
```

### Open locally

These use the default port and trainer 1 from the loaded file. Thursday 24 January 2019 is a business day. That trainer is already booked at 9:00–9:30am and 10:00–10:30am Pacific, so those two slots are absent from available times and present in the appointment list.

Open in a browser:

- http://localhost:8080/api/health
- http://localhost:8080/swagger/index.html
- http://localhost:8080/metrics
- http://localhost:8080/api/v1/trainers/1/appointments
- http://localhost:8080/api/v1/trainers/1/available-times?starts_at=2019-01-24T08:00:00-08:00&ends_at=2019-01-24T17:00:00-08:00

`POST /api/v1/appointments` is not a browser URL. This books 8:00–8:30am Pacific that Thursday for trainer 1 and user 1, and writes a row in the local database:

```bash
curl -s -X POST http://localhost:8080/api/v1/appointments \
  -H 'Content-Type: application/json' \
  -d '{"trainer_id":1,"user_id":1,"starts_at":"2019-01-24T16:00:00Z","ends_at":"2019-01-24T16:30:00Z"}'
```

### Routes

| Method | Path | Purpose |
| --- | --- | --- |
| GET | `/api/health` | `SELECT 1`. 200 when the database answers, 503 when it does not. |
| GET | `/api/v1/trainers/{trainer_id}/available-times?starts_at=&ends_at=` | Unbooked 30-minute slots in the window. |
| POST | `/api/v1/appointments` | Book one slot. JSON body: `trainer_id`, `user_id`, `starts_at`, `ends_at`. |
| GET | `/api/v1/trainers/{trainer_id}/appointments?limit=&cursor=` | That trainer's bookings, oldest first. |

`starts_at` and `ends_at` are RFC3339. A slot starts on a Unix half-hour, lasts 30 minutes, and must fall on Monday through Friday between 8:00am and 5:00pm Pacific (`America/Los_Angeles`). The last bookable start is 4:30pm, because the appointment has to end by 5:00pm. The availability window must be 31 days or less. `limit` defaults to 50 and is capped at 100. `cursor` is the opaque `next_cursor` from the previous page.

Success and error bodies include `status_code`, and that number is the HTTP status for the response:

```json
{"status_code": 409, "error": "slot already booked"}
```

- 400 is a bad window, a span that is not one half-hour slot, or a time outside Monday–Friday 8:00am–5:00pm Pacific.
- 404 is an unknown path, an unknown trainer, or an unknown user. An unknown path returns `{"status_code":404,"error":"resource not found"}`. A known path called with the wrong method returns `{"status_code":405,"error":"method not allowed"}`.
- 409 is a trainer who already has that start.
- 500 is an unexpected database failure.
- 503 is a failed health check.

### Swagger

With the API running, open `http://localhost:8080/swagger/index.html`.

The spec uses base path `/` so `GET /api/health` stays at that path. The schedule routes are still under `/api/v1`. Regenerate `docs/` after annotation changes:

```bash
go install github.com/swaggo/swag/cmd/swag@latest
swag init -g main.go -o docs
```

### Tests

Unit tests do not need Docker:

```bash
go test -race $(go list ./... | grep -v /e2e)
```

Integration tests use the local Docker Postgres and roll back the rows they insert:

```bash
go test -race -tags=integration ./internal/...
```

### Observability

`GET /metrics` exposes Prometheus metrics. Request labels use the route pattern, not the raw path or an id. `observability/alerts.yml` has the 5xx ratio, P99 latency, and pool saturation rules.

Grafana dashboards 21221, 6671, and 10826 read the default Go runtime metrics once a Prometheus source is scraping this process.

Set `OTEL_EXPORTER_OTLP_ENDPOINT` to export traces over OTLP gRPC. The sampler keeps about 10 percent of traces. When the variable is unset, tracing is a no-op so local runs do not need a collector.

Set `PROFILING_ENABLED=true` and `PYROSCOPE_URL` to start continuous profiling. `/debug/pprof` is not mounted.

## With more time (e2e test with Vercel + Neon DB):

The Vercel and Neon end-to-end flow is not part of local development. Do not run it from the unit tests.

- [ ] Add `e2e` Go tests that read `BASE_URL`, send `x-vercel-protection-bypass` when set, use a client timeout of at least 20s, and avoid depending on existing rows.
- [ ] Assert HTTP status and body `status_code` for `GET /api/health`, create-then-list, available times after a booking, 404 for a missing trainer, 400 for invalid input, and 409 for a taken slot.
- [ ] Copy `e2e/e2e.yml` to `.github/workflows/e2e.yml` after checking each Action's current major version. Keep it on pull requests to `main` and manual dispatch, never on fork pull requests.
- [ ] Run the flow only through `scripts/e2e.sh` against a temporary Neon branch and a Vercel preview. Do not run it as part of local development or the unit test command.
