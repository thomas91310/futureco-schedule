#!/usr/bin/env bash
# Load appointments.json into the local Docker Postgres. Splits any span
# longer than 30 minutes into consecutive 30-minute rows.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"

count="$(docker compose exec -T postgres psql -U futureco -d futureco -v ON_ERROR_STOP=1 -tA -c "select count(*) from appointments")"
count="${count//$'\r'/}"
if [[ "$count" != "0" ]]; then
  echo "appointments already has rows" >&2
  exit 1
fi

python3 - "$root/appointments.json" <<'PY' | docker compose exec -T postgres psql -U futureco -d futureco -v ON_ERROR_STOP=1
import json
import sys
from datetime import datetime, timedelta

path = sys.argv[1]
with open(path, encoding="utf-8") as fh:
    appointments = json.load(fh)

if not appointments:
    raise SystemExit("appointments.json is empty")


def parse_ts(value):
    text = value
    if len(text) >= 6 and text[-3] == ":" and text[-6] in "+-":
        text = text[:-3] + text[-2:]
    return datetime.strptime(text, "%Y-%m-%dT%H:%M:%S%z")


def ensure_half_hour(dt, label):
    epoch = int(dt.timestamp())
    if dt.microsecond != 0 or epoch % 1800 != 0:
        raise SystemExit(f"{label} is not on a UTC half-hour: {dt.isoformat()}")


rows = []
max_id = max(int(item["id"]) for item in appointments)
next_extra = max_id + 1

for item in appointments:
    source_id = int(item["id"])
    trainer_id = int(item["trainer_id"])
    user_id = int(item["user_id"])
    start = parse_ts(item["started_at"])
    end = parse_ts(item["ended_at"])
    ensure_half_hour(start, f"appointment {source_id} started_at")
    delta = end - start
    total = delta.days * 86400 + delta.seconds
    if delta.microseconds != 0 or total < 1800 or total % 1800 != 0:
        raise SystemExit(
            f"appointment {source_id} length must be a positive multiple of 30 minutes"
        )
    slots = total // 1800
    for i in range(slots):
        slice_start = start + timedelta(minutes=30 * i)
        slice_end = slice_start + timedelta(minutes=30)
        ensure_half_hour(slice_start, f"appointment {source_id} slice {i}")
        if i == 0:
            row_id = source_id
        else:
            row_id = next_extra
            next_extra += 1
        rows.append((row_id, trainer_id, user_id, slice_start, slice_end))

trainer_ids = sorted({row[1] for row in rows})
user_ids = sorted({row[2] for row in rows})


def people_values(ids, first_name, dob):
    return ",\n".join(
        f"  ({person_id}, '{first_name}', '{person_id}', '{dob}')" for person_id in ids
    )


def appointment_values(appointment_rows):
    lines = []
    for row_id, trainer_id, user_id, start, end in appointment_rows:
        lines.append(
            f"  ({row_id}, {trainer_id}, {user_id}, '{start.isoformat()}', '{end.isoformat()}')"
        )
    return ",\n".join(lines)


sys.stdout.write(
    f"""begin;
insert into trainers (id, first_name, last_name, dob)
overriding system value
values
{people_values(trainer_ids, "Trainer", "1980-01-01")};
insert into users (id, first_name, last_name, dob)
overriding system value
values
{people_values(user_ids, "User", "1990-01-01")};
insert into appointments (id, trainer_id, user_id, started_at, ended_at)
overriding system value
values
{appointment_values(rows)};
select setval(pg_get_serial_sequence('trainers', 'id')::regclass, (select max(id) from trainers));
select setval(pg_get_serial_sequence('users', 'id')::regclass, (select max(id) from users));
select setval(pg_get_serial_sequence('appointments', 'id')::regclass, (select max(id) from appointments));
commit;
"""
)
PY
