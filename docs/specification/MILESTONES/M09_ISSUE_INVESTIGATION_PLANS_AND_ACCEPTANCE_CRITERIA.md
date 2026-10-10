# RepoSurgeon — M09: Issue Investigation, Plans, and Acceptance Criteria

> Execution order: M08 → M09. Required branch: `milestone/m09-issue-investigation-plans-and-acceptance-criteria`. Execute one phase fully; do not claim completion until gates are actually green.
>
> Mandatory before starting: read `CLAUDE.md`, `RUN_FIRST.md`, `MASTER_IMPLEMENTATION_PLAN.md`, `ARCHITECTURE.md`, `GIT_WORKFLOW.md`, `SECURITY_GATES.md`, `API_CONTRACTS.md`, and all previous milestone completion reports. Follow the existing repository architecture where compatible; do not overwrite existing work.

## Objective and tangible outcome

Turn an issue into a structured, inspectable implementation plan with testable evidence and exact change scope.

**Definition of done:** A verified user-visible vertical slice running against actual Go backend + PostgreSQL + Vue frontend, with real tests and artifacts. No TODO implementations, mock production APIs, static demo data, unexplained skipped tests, or artificial success labels.

## Detailed implementation tasks — complete every item

1. Build issue triage classifier (bug/feature/refactor/security) and deterministic project policy lookup.
2. Use Step 5 to propose a typed plan containing root-cause hypotheses, evidence links, file list, dependencies, steps, tests, risk rating and success criteria.
3. Enforce JSON Schema validation and reject unsupported/empty plans; plan must cite actual repository paths and file revisions.
4. Run a verification pass that checks that referenced files/functions exist and that the plan addresses all acceptance conditions from the issue.
5. Represent plan as a DAG with prerequisites and independent substeps; reserve concurrency only for disjoint edit scopes.
6. Allow user to inspect/amend scope, record versioned approval on high-risk plans, and replan after changed evidence.
7. Add explicit out-of-scope and unresolved assumptions rather than silently treating uncertainty as truth.
8. Provide a plan preview with evidence, file tree targets, tests and actual approval state.

## Required end-to-end interfaces

**Backend and OpenAPI:** POST /api/v1/runs/{id}/plan; GET/PATCH /api/v1/runs/{id}/plan; POST /api/v1/runs/{id}/plan/approve.

**Persistence and migration:** Versioned plan DAG nodes, acceptance criteria, verification findings and plan approvals.

**Frontend and interaction:** Editable structured plan and evidence-linked investigation board.

For every new endpoint define request/response/error examples and access controls in OpenAPI; regenerate typed Vue clients and include drift checks. Use real PostgreSQL transactions and never invent successful adapter responses.

## Negative cases and security constraints

- A generated plan is a proposal, not evidence that root cause is correct.
- No planning state can bypass runner permissions or branch protection.

## Mandatory verification scenarios

- [ ] Seed known buggy repositories and verify plan includes file paths and reproduction tests.
- [ ] Invalid/irrelevant plans are rejected and retried with bounded error.
- [ ] High-risk plan cannot progress without recorded approval.
- [ ] Plans change when issue or code SHA changes, with outdated approvals invalidated.

Also run the repository-wide regression suite, Go formatting/vet/lint/race tests, frontend formatting/lint/type-check/build/tests, generated contract drift, integration tests, secret scan, dependency/security audits, Docker image/Compose smoke, and existing E2E tests. A command that cannot run because of missing prerequisites is **BLOCKED**, not passed. Fix any introduced regression before creating a PR.

## Atomic commit map (suggested minimum)

1. `m09: typed plan schema`
2. `m09: triage/investigation service`
3. `m09: schema/evidence verifier`
4. `m09: plan review UI`
5. `m09: end-to-end triage and approval tests`

Write each commit only after its contained work and relevant tests pass. Check `git log` for correct `yasserrmd` author and committer, permitted email, and zero AI/co-author trailers. No squash. Never commit directly to main.

## PR, merge, and completion evidence

1. Push `milestone/m09-issue-investigation-plans-and-acceptance-criteria`; create a PR into `main` with scope, code evidence, linked acceptance items and test results.
2. Wait for **actual** required CI checks and any branch-protection reviews. Fix on the same milestone branch using additional atomic commits; do not fake or bypass required checks.
3. Once green and authorized, merge through GitHub; verify merged SHA on main, pull updated main, and delete local and remote milestone branches.
4. Create `docs/milestone-reports/M09.md` from the supplied template with PR URL, merge SHA, actual commits, checks, evidence and risk summary. Ensure the report itself is versioned in the PR **before merging**, not written only after the branch is deleted.
5. Update `docs/PHASE_STATUS.md` and continue automatically to the next file. If external authorization/credential is missing, record `BLOCKED` and precise remediation, work only on independent safe tasks, and never report unperformed PR/merge as done.

**Explicit handoff to next phase:** This milestone is complete only when the delivered capability can be used in the UI/API and its full positive/negative tests pass with verifiable artifacts.
