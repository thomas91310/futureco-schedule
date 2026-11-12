---
name: e2e-vercel-neon
description: Run throwaway end-to-end tests for a Go API against a real Vercel preview deployment backed by a temporary Neon Postgres branch. Use whenever the user wants to e2e test, smoke test, or validate API responses on a deployed environment, deploy a preview to Vercel with a database, create or delete Neon branches for tests, wire the e2e job into GitHub Actions, or debug 401s, cold starts, or connection errors in this flow, even if they don't name Vercel or Neon explicitly.
---

# E2E testing: Go API on Vercel + temporary Neon branch

Deploy the API as a Vercel preview, point it at a short-lived Neon branch,
run `go test ./e2e` against the preview URL, then delete the branch.
Only the API is deployed. The database is provisioned (a branch), not deployed.

## Flow

1. Create a Neon branch in the **dedicated test project**.
2. Get that branch's **pooled** connection string.
3. Load `schema.sql` (and `seed.sql` if present) into the branch with `psql`.
4. `vercel deploy` with `DATABASE_URL` set to that string; capture the preview URL.
5. Run `BASE_URL=<preview-url> go test ./e2e -v -count=1`.
6. Delete the branch (and optionally the deployment), even if tests fail.

`scripts/e2e.sh` implements this end to end. Prefer running or adapting that
script over re-deriving the steps.

## Hard rules

- **Never use the production Neon project or its credentials.** Always use a
  separate Neon project that holds only fake data. A branch can inherit its
  parent's roles and passwords, so branching from real data leaks real access
  if the deployment's env leaks.
- **`NEON_API_KEY` stays out of Vercel and out of Go code.** It only lives in
  the shell or CI secrets where the script runs. The deployed API only ever
  gets `DATABASE_URL`.
- **Use the pooled connection string** (host contains `-pooler`) with
  `sslmode=require`, and keep the pgx pool small (`MaxConns` about 3).
- **Always clean up** with a shell `trap` (or `if: always()` in CI). The Neon
  Free plan allows 10 branches per project and has no paid overflow, so leaked
  branches eventually block new runs.
- Never print the connection string or tokens in logs.

## Required environment

| Variable | Where it comes from |
|---|---|
| `NEON_PROJECT_ID` | Neon test project settings |
| `NEON_API_KEY` | Neon account settings, API keys |
| `VERCEL_TOKEN` | Vercel account tokens |
| `VERCEL_ORG_ID`, `VERCEL_PROJECT_ID` | `.vercel/project.json` after `vercel link` (needed in CI) |
| `VERCEL_AUTOMATION_BYPASS_SECRET` | Project settings, Deployment Protection (if available) |

Tools: `neonctl`, `vercel`, `psql`, `go`. The `neonctl` flags have changed
between versions, so check `neonctl --help` and `neonctl branches --help`
before changing any command.

## Test conventions (`./e2e`)

- Read `BASE_URL` from the environment. Fail fast if it is empty.
- Send `x-vercel-protection-bypass: $VERCEL_AUTOMATION_BYPASS_SECRET` when set.
- Use an HTTP client timeout of at least 20s (cold starts on Vercel and Neon
  scale-to-zero).
- Use unique data per run (for example a timestamp in names) so reruns never
  collide, and never depend on pre-existing rows.
- Cover at least: `GET /api/health` (runs `SELECT 1`), create-then-fetch,
  404 for a missing id, 400 for invalid input.
- Use `-count=1` so results are never cached.

## Troubleshooting

- **401 on the preview URL:** Deployment Protection is blocking the request.
  Send the bypass header. If the bypass secret is not available on the plan,
  disable Vercel Authentication on the test project only.
- **First request is slow or times out:** cold start. Add a warm-up call to
  `/api/health` before the real tests and raise the timeout.
- **`too many connections` or pool errors:** use the `-pooler` host and lower
  `MaxConns`.
- **Deploy does not detect Go:** the Go framework preset needs a root `go.mod`
  and `main.go` (or `cmd/api/main.go`, `cmd/server/main.go`) that listens on
  `PORT`; try `{"framework": "go"}` in `vercel.json`. If the preset is
  unavailable on the account, use the `/api/*.go` function layout instead.
- **Branch creation fails:** the project hit its branch limit. List with
  `neonctl branches list` and delete stale `e2e-*` branches.

## Running in CI

Use `assets/e2e.yml` as a starting workflow (copy it to
`.github/workflows/e2e.yml`). Secrets live in GitHub repo secrets. Run it on
PRs to `main` and on manual dispatch, not on every push, because it uses Neon
and Vercel quota. Do not run it for PRs from forks (secrets are withheld
there, and untrusted code should not get them).

For general Go CI (test, lint, security scanning, coverage, Dependabot),
defer to the installed `golang-continuous-integration` skill
(upstream: `samber/cc-skills-golang`, `skills/golang-continuous-integration`).
This skill covers only the e2e job. Check each GitHub Action's current major
version before committing the workflow.

## Out of scope

Observability setup, release pipelines, and security findings belong to other
skills. Do not add them to this flow unless asked.
