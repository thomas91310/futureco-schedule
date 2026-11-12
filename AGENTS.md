# AGENTS.md

Go API deployed on Vercel, backed by Postgres on Neon. Keep this file short:
it is loaded into context for every task. Detail belongs in the skills it points to.

## Project layout

- `main.go`, `go.mod`: the API (`net/http`, `pgx`)
- `Dockerfile`: local postgres instance
- `schema.sql`: Postgres schema (repo root)
- `e2e/`: end-to-end tests run against a deployed preview
- `scripts/e2e.sh`: temp Neon branch -> Vercel preview -> `go test ./e2e` -> cleanup
- `.github/workflows/`: CI
- `.agents/skills/`: installed skills (see `skills-lock.json`)

## Work-type routing

Before starting a task of one of these types, find the row, then read and follow
the listed skills. If a listed skill is not installed, say so and continue with
the project rules below instead of guessing.

| When the task is about | Use these skills | Also read |
|---|---|---|
| Database schema: tables, types, indexes, constraints, migrations, `schema.sql` | `supabase-postgres-best-practices` | Schema rules |
| Neon specifics: pooling, branching, connection strings | `neon-postgres` | Safety rules |
| API design: endpoints, request/response shapes, status codes, errors | `api-design-principles` | API rules |
| API DB Connection: how the go code connects to the DB | `golang-database` | APIDB rules |
| Go linting: golangci-lint, lint errors, lint config | `golang-lint` | Lint rules |
| Go unit tests: table tests, test helpers, mocking | `golang-testing` | Testing rules |
| CI: GitHub Actions workflows, security scanning, releases | `golang-continuous-integration` | CI rules |
| E2E testing against Vercel + Neon, the e2e workflow | `e2e-vercel-neon` | Safety rules |
| Swagger docs: api documentation | `golang-swagger` | none
| Observability: logs, metrics, traces | `golang-observability` | none |

If a task spans several rows (for example a new endpoint plus a new table), use
the skills from each row.

Safety rules and Conventions apply to every task, whichever row it matches.

Skills give general best practices. The rules sections below ("Also read" column)
are this repo's own directives. Follow both, and if a skill's advice conflicts with
a rule below, the rule below wins. Mention the conflict in your reply.

## Skill sources

Skills are installed in `.agents/skills/` and read from there. If a skill is
missing, do NOT install it and do NOT use a web page as a substitute (web pages
omit bundled files). Tell the user which skill is missing and give the install
command below.

| Skill | Install command |
|---|---|
| `supabase-postgres-best-practices` | `npx skills add https://github.com/supabase/agent-skills --skill supabase-postgres-best-practices` |
| `neon-postgres` | `npx skills add neondatabase/agent-skills --skill neon-postgres` |
| `api-design-principles` | `npx skills add https://github.com/wshobson/agents --skill api-design-principles` |
| `golang-swagger` | `npx skills add https://github.com/samber/cc-skills-golang --skill golang-swagger` |
| `golang-database` | `npx skills add https://github.com/samber/cc-skills-golang --skill golang-database` |
| `golang-lint` | `npx skills add https://github.com/samber/cc-skills-golang --skill golang-lint` |
| `golang-testing` | `npx skills add https://github.com/samber/cc-skills-golang --skill golang-testing` |
| `golang-continuous-integration` | `npx skills add https://github.com/samber/cc-skills-golang --skill golang-continuous-integration` |
| `golang-observability` | `npx skills add https://github.com/samber/cc-skills-golang --skill golang-observability` |
| `e2e-vercel-neon` | local skill, committed in this repo (no install) |

## Schema rules

- Review output: list issues grouped by impact (critical, high, medium, low),
  then give suggested DDL.
- Propose migrations only. Never run destructive DDL (`DROP`, `TRUNCATE`,
  unscoped `DELETE`) without explicit confirmation.
- Index every foreign key column. Use `TIMESTAMPTZ` for timestamps.

## Lint rules

- Run `golangci-lint run ./...`, using the repo's `.golangci.yml` if present.
- Fix findings instead of adding `//nolint`. Ask before disabling a linter.

## Testing rules

- Prefer unit tests that exercise real code paths. Avoid mocking where you can.
- Mock only external services (for example the Neon or Vercel APIs). Do not mock
  the database layer when a test database can be used.
- If a test needs Postgres, use a local postgres Dockerfile
- Only use the Neon test project while end to end testing

## API rules

- Responses are JSON. Errors use `{"status_code": <code>, "error": "<message>"}`.
- Use the standard HTTP status that fits (for example 400 invalid input,
  404 missing, 500 server error). `status_code` in the body must equal the HTTP
  response status.
- New or changed endpoints need a matching test in `e2e/` that asserts both the
  HTTP status and `status_code` in the body.
- Always return JSON even on 404 errors

## APIDB rules

- For testing, use the local postgres Dockerfile that the local API can connect to

## CI rules

- Use the current stable major version of every GitHub Action.
- The e2e job runs on PRs to `main` and manual dispatch, never on fork PRs.

## Safety rules (always apply)

- Never use production database credentials for tests. E2E uses a separate Neon
  test project with fake data only.
- Never put `NEON_API_KEY` in Vercel env vars or Go code. It only lives in the
  shell or CI secrets.
- Use the pooled Neon connection string (`-pooler` host) with `sslmode=require`.
- Never print or commit secrets, connection strings, or tokens. Do not ask for
  secret values in chat.
- Do not run deploys, create Neon branches, or delete anything unless asked.
- When dealing with the e2e test, add the connection strings for my Neon project hidden-wave-35996154 in organization org-plain-block-94162020 to my app by following the instructions at https://neon.com/get-started/prompt.md

## Conventions (always apply)

- Document in a README.md file everything that pertains to setups (local db access, run loading scripts...)
- Before finishing a code change, run unit tests only, excluding the e2e package:
  `go test -race $(go list ./... | grep -v /e2e)`
- E2E tests need a deployed preview and `BASE_URL`. Run them only through
  `scripts/e2e.sh` or the e2e CI job, never as part of the unit test run.
- Do not edit installed skills in `.agents/skills/` unless asked; project-specific
  guidance goes in this file.
- add .gitignore and update as you go
