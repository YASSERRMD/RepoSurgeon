# RepoSurgeon — M01: Domain Model, Persistence, and Project Isolation

> Execution order: M00 → M01. Required branch: `milestone/m01-domain-model-persistence-and-project-isolation`. Execute one phase fully; do not claim completion until gates are actually green.
>
> Mandatory before starting: read `CLAUDE.md`, `RUN_FIRST.md`, `MASTER_IMPLEMENTATION_PLAN.md`, `ARCHITECTURE.md`, `GIT_WORKFLOW.md`, `SECURITY_GATES.md`, `API_CONTRACTS.md`, and all previous milestone completion reports. Follow the existing repository architecture where compatible; do not overwrite existing work.

## Objective and tangible outcome

Deliver persisted workspaces, projects, settings, runs, activities and reproducible migrations with strong tenant boundaries.

**Definition of done:** A verified user-visible vertical slice running against actual Go backend + PostgreSQL + Vue frontend, with real tests and artifacts. No TODO implementations, mock production APIs, static demo data, unexplained skipped tests, or artificial success labels.

## Detailed implementation tasks — complete every item

1. Define durable entities: users, organizations, memberships, projects, repository_connections, issues, tasks, runs, steps, tool_calls, artifacts, approvals, credentials metadata, model_usage and audit_events.
2. Implement numbered, reversible migrations with FK constraints, partial indexes, uniqueness, created/updated timestamps and soft-deletion policy where necessary.
3. Use pgx with an explicit repository layer, bounded transactions, query timeouts and typed domain errors. No business logic in HTTP handlers.
4. Enforce organization/project scoping on every query (including joins and listings); implement cursor pagination, ownership checks, consistent sort and server-side filtering.
5. Implement project CRUD + organization switching + settings forms in web, using real persisted API responses.
6. Add idempotency keys for state-changing operations, optimistic concurrency for settings and migration version detection.
7. Create seeded fixtures in test-only migrations; ensure production deployments never install seeded accounts or fake projects.
8. Record request correlation IDs with safely redacted audit events for create/change/delete paths.

## Required end-to-end interfaces

**Backend and OpenAPI:** POST/GET /api/v1/organizations; /api/v1/projects; GET/PATCH /api/v1/projects/{id}; GET /api/v1/audit?project_id=

**Persistence and migration:** Typed schemas and indexes for identity, repos, issues, tasks, runs, step events, usage, approvals, audits; migration up/down evidence.

**Frontend and interaction:** Functional workspace/project list, creation/edit forms and empty/error/loading states; no static records.

For every new endpoint define request/response/error examples and access controls in OpenAPI; regenerate typed Vue clients and include drift checks. Use real PostgreSQL transactions and never invent successful adapter responses.

## Negative cases and security constraints

- All foreign identifiers must be checked for cross-tenant access, not only top-level project IDs.
- No plaintext token material or unredacted stack traces in audit tables.

## Mandatory verification scenarios

- [ ] Migrate new and upgrade existing database; reverse migration in disposable database.
- [ ] Test org A cannot read/write org B project by ID enumeration.
- [ ] Create project from browser; reload and verify persistence and filtering.
- [ ] Run transaction rollback and idempotent repeated-request tests.

Also run the repository-wide regression suite, Go formatting/vet/lint/race tests, frontend formatting/lint/type-check/build/tests, generated contract drift, integration tests, secret scan, dependency/security audits, Docker image/Compose smoke, and existing E2E tests. A command that cannot run because of missing prerequisites is **BLOCKED**, not passed. Fix any introduced regression before creating a PR.

## Atomic commit map (suggested minimum)

1. `m01: schema and migration tests`
2. `m01: repositories and tenant query scoping`
3. `m01: project REST contracts and tests`
4. `m01: Vue project screens and Playwright flows`
5. `m01: audit/idempotency hardening`

Write each commit only after its contained work and relevant tests pass. Check `git log` for correct `yasserrmd` author and committer, permitted email, and zero AI/co-author trailers. No squash. Never commit directly to main.

## PR, merge, and completion evidence

1. Push `milestone/m01-domain-model-persistence-and-project-isolation`; create a PR into `main` with scope, code evidence, linked acceptance items and test results.
2. Wait for **actual** required CI checks and any branch-protection reviews. Fix on the same milestone branch using additional atomic commits; do not fake or bypass required checks.
3. Once green and authorized, merge through GitHub; verify merged SHA on main, pull updated main, and delete local and remote milestone branches.
4. Create `docs/milestone-reports/M01.md` from the supplied template with PR URL, merge SHA, actual commits, checks, evidence and risk summary. Ensure the report itself is versioned in the PR **before merging**, not written only after the branch is deleted.
5. Update `docs/PHASE_STATUS.md` and continue automatically to the next file. If external authorization/credential is missing, record `BLOCKED` and precise remediation, work only on independent safe tasks, and never report unperformed PR/merge as done.

**Explicit handoff to next phase:** This milestone is complete only when the delivered capability can be used in the UI/API and its full positive/negative tests pass with verifiable artifacts.
