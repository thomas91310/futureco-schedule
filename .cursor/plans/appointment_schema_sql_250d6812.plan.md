---
name: Appointment schema SQL
overview: Write schema.sql, a local Postgres Dockerfile and compose file, and scripts that start that database and load appointments.json, splitting any booking longer than 30 minutes into consecutive 30-minute rows.
todos:
  - id: write-schema
    content: Add schema.sql with trainers and users (first_name, last_name, dob), appointments, half-hour and 30-minute checks, unique (trainer_id, started_at), and an index on user_id
    status: completed
  - id: local-postgres
    content: Add a Postgres 18 Dockerfile and compose.yaml that publish localhost:5432, persist data, and apply schema.sql on an empty volume
    status: completed
  - id: load-script
    content: Add scripts/load_appointments.sh to insert trainers, users, and appointments from appointments.json, splitting spans longer than 30 minutes
    status: completed
  - id: setup-and-verify
    content: Add scripts/setup.sh, run it, and leave the local database up with the loaded rows
    status: completed
isProject: false
---

# Appointment schema

Write [schema.sql](schema.sql), the local Postgres [Dockerfile](Dockerfile) and [compose.yaml](compose.yaml), [scripts/load_appointments.sh](scripts/load_appointments.sh), and [scripts/setup.sh](scripts/setup.sh). No Go API. The loader is local Docker only. Do not wire it into [scripts/e2e.sh](scripts/e2e.sh) or point it at Neon.

Skills: `supabase-postgres-best-practices` for the schema (identity primary keys, `timestamptz`, lowercase identifiers, index every foreign key). `golang-database` for how a local database is applied later. Project rules win on two conflicts with that skill: it says not to author schema SQL and to avoid hand-rolled migrators, but [AGENTS.md](AGENTS.md) requires a root [schema.sql](schema.sql) and a [Dockerfile](Dockerfile) that is the local Postgres instance. `neon-postgres` is not installed and is not needed here; this database is local Docker, not Neon. Local connections use `sslmode=disable`. Neon stays `sslmode=require`.

## Model

Availability is derived: any half-hour instant not already booked for that trainer. No slots or availability table.

```mermaid
erDiagram
  trainers ||--o{ appointments : books
  users ||--o{ appointments : books
  trainers {
    bigint id PK
    text first_name
    text last_name
    date dob
  }
  users {
    bigint id PK
    text first_name
    text last_name
    date dob
  }
  appointments {
    bigint id PK
    bigint trainer_id FK
    bigint user_id FK
    timestamptz started_at
    timestamptz ended_at
  }
```

`trainers` and `users` each have `first_name`, `last_name`, and `dob`. [appointments.json](appointments.json) only has ids, so the loader supplies fixture names and birth dates for those rows.

## Why overlap does not need an exclusion constraint

Every stored appointment is exactly 30 minutes and starts on a UTC half-hour (`:00` or `:30`, zero seconds). On that grid, two ranges overlap only when they share `started_at`. A unique `(trainer_id, started_at)` is enough and needs no `btree_gist` extension. Touching slots (`09:00–09:30` and `09:30–10:00`) stay allowed.

A user may book two trainers at the same time. The prompt only forbids trainer overlap.

## Constraints and indexes

- Primary keys: `bigint generated always as identity`.
- `first_name` and `last_name`: `text not null`, with a check that each value is not blank after `btrim`. `text` rather than `varchar`.
- `dob`: `date not null`. A date of birth has no time and no zone. A check against `current_date` is not valid in Postgres because that function is not immutable.
- `appointments.trainer_id` and `appointments.user_id`: `not null`, foreign keys, `on delete restrict`.
- `started_at` and `ended_at`: `timestamptz not null`.
- Duration: `ended_at = started_at + interval '30 minutes'`.
- Grid, independent of session `TimeZone`: `(extract(epoch from started_at) % 1800) = 0`. Epoch is UTC, so this matches the `-08:00` instants in the fixture (`09:00` and `09:30` local stay on a UTC half-hour because the offset is a whole hour).
- Unique `(trainer_id, started_at)`. That index also covers trainer-id lookups, so no second index on `trainer_id`.
- Separate index on `user_id` (foreign key; not a prefix of the unique index).

No RLS. Access is the Go API, not Supabase auth.

## DDL to put in schema.sql

```sql
create table trainers (
  id bigint generated always as identity primary key,
  first_name text not null,
  last_name text not null,
  dob date not null,
  constraint trainers_first_name_not_blank check (btrim(first_name) <> ''),
  constraint trainers_last_name_not_blank check (btrim(last_name) <> '')
);

create table users (
  id bigint generated always as identity primary key,
  first_name text not null,
  last_name text not null,
  dob date not null,
  constraint users_first_name_not_blank check (btrim(first_name) <> ''),
  constraint users_last_name_not_blank check (btrim(last_name) <> '')
);

create table appointments (
  id bigint generated always as identity primary key,
  trainer_id bigint not null references trainers (id) on delete restrict,
  user_id bigint not null references users (id) on delete restrict,
  started_at timestamptz not null,
  ended_at timestamptz not null,
  constraint appointments_duration_30_minutes
    check (ended_at = started_at + interval '30 minutes'),
  constraint appointments_started_at_half_hour
    check ((extract(epoch from started_at) % 1800) = 0),
  constraint appointments_trainer_started_at_key
    unique (trainer_id, started_at)
);

create index appointments_user_id_idx on appointments (user_id);
```

## Local Postgres

[AGENTS.md](AGENTS.md) already names [Dockerfile](Dockerfile) as the local Postgres instance the API and tests connect to. Add that image plus [compose.yaml](compose.yaml) so `docker compose up` is the run path (published port, named volume, healthcheck).

Image: `postgres:18` (current stable major). The Dockerfile copies [schema.sql](schema.sql) to `/docker-entrypoint-initdb.d/001_schema.sql`. The official entrypoint runs that only when the data directory is empty, so the first start of a new volume creates the three tables.

Local-only credentials, set as image env so `docker run -p 5432:5432` works without extra flags. These are not Neon or Vercel secrets:

- user `futureco`
- password `futureco`
- database `futureco`

Connection string:

`postgres://futureco:futureco@localhost:5432/futureco?sslmode=disable`

[compose.yaml](compose.yaml):

- build the Dockerfile
- publish `5432:5432`
- named volume `pgdata` on `/var/lib/postgresql/data` so restarts keep data and do not re-run init
- mount `./migrations` at `/migrations` (read-only) so later SQL is visible inside the container
- healthcheck: `pg_isready -U futureco -d futureco`

No custom entrypoint. No migration tool installed in the image.

## Migrations after this

Do not add a `migrations/` history or a migrate binary in this change. [schema.sql](schema.sql) is the full schema a new volume gets.

Init scripts do not run again on an existing volume. The next schema change should:

1. Add a golang-migrate pair under `migrations/` (`NNNN_name.up.sql` and `.down.sql`) with only the delta. `golang-database` calls for golang-migrate or Flyway, not a shell loop.
2. Update [schema.sql](schema.sql) to the new full schema so a fresh volume matches.
3. Apply the delta to a database that already ran init with the migrate CLI against the local URL above. A new volume should not also run that same delta, or table creation will run twice.

Reset the local database with `docker compose down -v`, then `docker compose up`. That recreates the volume and reapplies [schema.sql](schema.sql).

## Load appointments.json

[scripts/load_appointments.sh](scripts/load_appointments.sh) reads [appointments.json](appointments.json) and loads one transaction through `docker compose exec -T` and `psql -v ON_ERROR_STOP=1`. It uses `python3` from the host to turn the JSON into SQL. It does not open a Neon connection.

The schema still stores only 30-minute rows. The script is what accepts a longer source interval. For each JSON object:

- Reject a row whose length is less than 30 minutes, or whose length is not an exact multiple of 30 minutes.
- Reject a `started_at` that is not on a half-hour with zero seconds.
- When the length is 30 minutes, insert that one row.
- When the length is longer, insert consecutive 30-minute slices that cover the original span. Same `trainer_id` and `user_id` on every slice. A 60-minute row becomes two rows: `[started_at, started_at + 30 minutes)` and `[started_at + 30 minutes, started_at + 60 minutes)`.

Row 9 is `2019-01-26T13:00:00-08:00` to `2019-01-26T14:00:00-08:00` for trainer 3 and user 9. The script stores `13:00–13:30` and `13:30–14:00`. Row 10 stays `14:00–14:30` for user 10, so the slices do not share a trainer start time.

Ids:

- Insert each distinct `trainer_id` and `user_id` into `trainers` and `users` before appointments. The JSON has no names or birth dates, so the loader fills required columns with fixture values: trainers get `first_name` `Trainer`, `last_name` equal to the id, and `dob` `1980-01-01`; users get `first_name` `User`, `last_name` equal to the id, and `dob` `1990-01-01`.
- Keep the JSON `id` on the first slice.
- Give each extra slice the next integer after the highest JSON `id`, in file order. This file's extra slice is id 11.
- Insert explicit ids with `overriding system value`, then `setval` each identity sequence to `max(id)` so a later insert continues past 11.

The script refuses to load when `appointments` already has rows. It does not delete or truncate.

Expected result for this file: 3 trainers, 10 users, 11 appointments.

## Setup

[scripts/setup.sh](scripts/setup.sh) follows the `set -euo pipefail` style of [scripts/e2e.sh](scripts/e2e.sh). It runs `docker compose up -d --wait`, then [scripts/load_appointments.sh](scripts/load_appointments.sh), and leaves the container running.

## Check

Run [scripts/setup.sh](scripts/setup.sh). Confirm trainers = 3, users = 10, and appointments = 11. Confirm trainer 3 has user 9 at `13:00–13:30` and `13:30–14:00`, and user 10 at `14:00–14:30`. Leave the database up.
