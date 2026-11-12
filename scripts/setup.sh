#!/usr/bin/env bash
# Start the local Postgres and load appointments.json.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"

docker compose up -d --wait
"$root/scripts/load_appointments.sh"
