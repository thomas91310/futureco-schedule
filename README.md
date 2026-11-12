# futureco-schedule

Local setup uses Docker Postgres. The API and the Neon end-to-end flow are separate. `scripts/e2e.sh` needs its own Neon test project and is not part of this local database.

## Requirements

- Docker with Compose v2
- `python3` on the host (used by the load script)

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

User, password, and database name are the local image defaults (`futureco`). They are not Neon credentials. Neon connections use `sslmode=require`.

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
