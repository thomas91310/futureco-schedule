#!/usr/bin/env bash
# Throwaway e2e: temp Neon branch -> Vercel preview -> go test -> cleanup.
# Required env: NEON_PROJECT_ID, NEON_API_KEY, VERCEL_TOKEN
# Optional env: VERCEL_AUTOMATION_BYPASS_SECRET, VERCEL_ORG_ID, VERCEL_PROJECT_ID
# NOTE: neonctl flags change between versions; confirm with `neonctl --help`.
set -euo pipefail

: "${NEON_PROJECT_ID:?set NEON_PROJECT_ID (use the dedicated TEST project)}"
: "${NEON_API_KEY:?set NEON_API_KEY}"
: "${VERCEL_TOKEN:?set VERCEL_TOKEN}"

BRANCH="e2e-$(date +%s)"
URL=""

cleanup() {
  echo "Cleaning up..."
  neonctl branches delete "$BRANCH" --project-id "$NEON_PROJECT_ID" || true
  if [ -n "$URL" ]; then
    vercel remove "$URL" --yes --token "$VERCEL_TOKEN" || true
  fi
}
trap cleanup EXIT

# 1. Create branch and get the pooled connection string (never echo it)
neonctl branches create --name "$BRANCH" --project-id "$NEON_PROJECT_ID"
DB_URL=$(neonctl connection-string "$BRANCH" --project-id "$NEON_PROJECT_ID" --pooled)

# 2. Load schema (+ optional seed data)
psql "$DB_URL" -v ON_ERROR_STOP=1 -f schema.sql
[ -f seed.sql ] && psql "$DB_URL" -v ON_ERROR_STOP=1 -f seed.sql

# 3. Deploy a preview with the branch DB injected at runtime
URL=$(vercel deploy --yes --token "$VERCEL_TOKEN" -e DATABASE_URL="$DB_URL")
echo "Deployed to $URL"

# 4. Warm up (cold starts on Vercel and Neon scale-to-zero)
curl -sS -m 30 -o /dev/null \
  -H "x-vercel-protection-bypass: ${VERCEL_AUTOMATION_BYPASS_SECRET:-}" \
  "$URL/api/health" || true

# 5. Run the tests
BASE_URL="$URL" go test ./e2e -v -count=1
