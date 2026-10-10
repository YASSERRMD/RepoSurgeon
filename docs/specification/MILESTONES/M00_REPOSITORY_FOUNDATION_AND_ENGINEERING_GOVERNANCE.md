# RepoSurgeon — M00: Repository Foundation and Engineering Governance

> Execution order: None → M00. Required branch: `milestone/m00-repository-foundation-and-engineering-governance`. Execute one phase fully; do not claim completion until gates are actually green.
>
> Mandatory before starting: read `CLAUDE.md`, `RUN_FIRST.md`, `MASTER_IMPLEMENTATION_PLAN.md`, `ARCHITECTURE.md`, `GIT_WORKFLOW.md`, `SECURITY_GATES.md`, `API_CONTRACTS.md`, and all previous milestone completion reports. Follow the existing repository architecture where compatible; do not overwrite existing work.

## Objective and tangible outcome

Create a runnable, governed monorepo and enforce every execution and Git rule before the agent features begin.

**Definition of done:** A verified user-visible vertical slice running against actual Go backend + PostgreSQL + Vue frontend, with real tests and artifacts. No TODO implementations, mock production APIs, static demo data, unexplained skipped tests, or artificial success labels.

## Detailed implementation tasks — complete every item

1. Inspect the existing repository, remote, current branch, language files, tests, protection rules and open PRs. Preserve legitimate existing work; never rewrite or reset history to force this plan.
2. Create a Go workspace with `cmd/api`, `cmd/worker`, `internal/`, `migrations/`, `api/openapi.yaml`; Vue 3 + TypeScript Vite app in `web/`; and `infra/`, `scripts/`, `docs/`, `e2e/`, `fixtures/`.
3. Implement a real `/health/live` and `/health/ready`; readiness must verify database connectivity and pending critical migrations. Add frontend home page that checks readiness via a typed API client and shows actionable states.
4. Configure deterministic local development with Docker Compose for PostgreSQL, API, worker, web and a separated executor interface. Add `.env.example`; reject missing secrets in non-test modes.
5. Enforce Git identity and metadata rules through tracked pre-commit / commit-msg hooks and CI inspection of new commits; keep remote URL and authentication out of logs.
6. Define engineering standards (strict TypeScript; Go formatting/vet/lint; OpenAPI contract-first; meaningful tests), test helpers, code owners and branch/PR templates.
7. Set up GitHub Actions jobs for formatting, lint, Go tests/race, API contract drift, web type-check/tests/build, secret scan, dependency audit, image build and Compose smoke; jobs must run real commands.
8. Establish ADR-001 modular monolith and isolated runner boundary, threat-model starter, milestone tracker and immutable release evidence templates.

## Required end-to-end interfaces

**Backend and OpenAPI:** GET /health/live, GET /health/ready, GET /api/v1/system/info; OpenAPI 3.1 contract and typed web client.

**Persistence and migration:** Baseline versioned migration for schema_migrations plus an application metadata table; migration up/down tests.

**Frontend and interaction:** Light themed, responsive operational landing view with real connected/disconnected statuses, not fake dashboard metrics.

For every new endpoint define request/response/error examples and access controls in OpenAPI; regenerate typed Vue clients and include drift checks. Use real PostgreSQL transactions and never invent successful adapter responses.

## Negative cases and security constraints

- Missing remote or GitHub permission is a documented external blocker, not an invented successful PR.
- Never place `.env`, keys, tokens or the Docker socket in application containers.

## Mandatory verification scenarios

- [ ] Run Go format, vet, lint, unit tests and `go test -race ./...`.
- [ ] Run frontend lint, format check, type-check, unit tests and production build.
- [ ] Run Compose start/stop + real browser Playwright readiness smoke.
- [ ] Break PostgreSQL and prove readiness transitions from ready to unavailable.
- [ ] Open a PR with all CI required checks green; enforce identity and coauthor checks.

Also run the repository-wide regression suite, Go formatting/vet/lint/race tests, frontend formatting/lint/type-check/build/tests, generated contract drift, integration tests, secret scan, dependency/security audits, Docker image/Compose smoke, and existing E2E tests. A command that cannot run because of missing prerequisites is **BLOCKED**, not passed. Fix any introduced regression before creating a PR.

## Atomic commit map (suggested minimum)

1. `m00: initialize directory/build/module manifests`
2. `m00: implement health probes and typed client`
3. `m00: build real UI readiness view and tests`
4. `m00: add Docker Compose and CI quality/security gates`
5. `m00: write governance docs and verify full smoke`

Write each commit only after its contained work and relevant tests pass. Check `git log` for correct `yasserrmd` author and committer, permitted email, and zero AI/co-author trailers. No squash. Never commit directly to main.

## PR, merge, and completion evidence

1. Push `milestone/m00-repository-foundation-and-engineering-governance`; create a PR into `main` with scope, code evidence, linked acceptance items and test results.
2. Wait for **actual** required CI checks and any branch-protection reviews. Fix on the same milestone branch using additional atomic commits; do not fake or bypass required checks.
3. Once green and authorized, merge through GitHub; verify merged SHA on main, pull updated main, and delete local and remote milestone branches.
4. Create `docs/milestone-reports/M00.md` from the supplied template with PR URL, merge SHA, actual commits, checks, evidence and risk summary. Ensure the report itself is versioned in the PR **before merging**, not written only after the branch is deleted.
5. Update `docs/PHASE_STATUS.md` and continue automatically to the next file. If external authorization/credential is missing, record `BLOCKED` and precise remediation, work only on independent safe tasks, and never report unperformed PR/merge as done.

**Explicit handoff to next phase:** This milestone is complete only when the delivered capability can be used in the UI/API and its full positive/negative tests pass with verifiable artifacts.
