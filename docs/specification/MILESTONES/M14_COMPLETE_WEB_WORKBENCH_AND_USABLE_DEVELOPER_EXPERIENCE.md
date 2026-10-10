# RepoSurgeon — M14: Complete Web Workbench and Usable Developer Experience

> Execution order: M13 → M14. Required branch: `milestone/m14-complete-web-workbench-and-usable-developer-experience`. Execute one phase fully; do not claim completion until gates are actually green.
>
> Mandatory before starting: read `CLAUDE.md`, `RUN_FIRST.md`, `MASTER_IMPLEMENTATION_PLAN.md`, `ARCHITECTURE.md`, `GIT_WORKFLOW.md`, `SECURITY_GATES.md`, `API_CONTRACTS.md`, and all previous milestone completion reports. Follow the existing repository architecture where compatible; do not overwrite existing work.

## Objective and tangible outcome

Unify previously built vertical slices into a genuinely usable premium light-theme developer console.

**Definition of done:** A verified user-visible vertical slice running against actual Go backend + PostgreSQL + Vue frontend, with real tests and artifacts. No TODO implementations, mock production APIs, static demo data, unexplained skipped tests, or artificial success labels.

## Detailed implementation tasks — complete every item

1. Deliver navigation for Overview, Projects, Issues, Runs, Workspaces, Models, Approvals, Evaluations, Settings and Audit.
2. Build responsive list/detail layouts and global search with URL-addressable filters and pagination sourced from real APIs.
3. Provide an issue-to-run wizard: choose project, issue, branch, model, effort, budget and policy; review then start.
4. Implement file browser, issue context, plan DAG, diffs, checks, approval inbox and PR status as coordinated real screens.
5. Maintain controlled state via typed API/query client and server reconciliation; proper loading, empty, failure and retry states throughout.
6. Apply brand palette off-white, navy `#0A1F44`, gold `#C9A227` for sparse highlights, grey `#E5E7EB`; readable typography and keyboard focus.
7. Support accessible keyboard navigation, WCAG-minded contrast, responsive desktop/tablet layouts and persistent user view preferences.
8. Add unsaved edit guards, secure Markdown rendering, virtualized logs, large diff pagination and no client-side exposure of secrets.

## Required end-to-end interfaces

**Backend and OpenAPI:** Ensure all existing endpoints are covered by generated strict client; implement `/api/v1/dashboard` aggregate for actual metrics if needed.

**Persistence and migration:** Persist user view settings; all dashboard metrics derived from real run and repository tables.

**Frontend and interaction:** Complete polished SPA, no static screenshots or arbitrary fake counts.

For every new endpoint define request/response/error examples and access controls in OpenAPI; regenerate typed Vue clients and include drift checks. Use real PostgreSQL transactions and never invent successful adapter responses.

## Negative cases and security constraints

- UI must never grant permission that backend does not independently verify.
- No fake generated timeline events or mock results presented as real.

## Mandatory verification scenarios

- [ ] Playwright from login to import issue to inspect run/check/PR on fixture backend.
- [ ] Accessibility tests for major workflows; keyboard navigation and responsive screenshots.
- [ ] No console errors, no mock data in production bundle, no fake success states.
- [ ] Typecheck, lint, unit, build and contracted API client drift all green.

Also run the repository-wide regression suite, Go formatting/vet/lint/race tests, frontend formatting/lint/type-check/build/tests, generated contract drift, integration tests, secret scan, dependency/security audits, Docker image/Compose smoke, and existing E2E tests. A command that cannot run because of missing prerequisites is **BLOCKED**, not passed. Fix any introduced regression before creating a PR.

## Atomic commit map (suggested minimum)

1. `m14: app shell/navigation/design tokens`
2. `m14: run creation wizard and full details`
3. `m14: search/tables/diffs/test UX`
4. `m14: accessibility/responsive finishing`
5. `m14: full Playwright and contract tests`

Write each commit only after its contained work and relevant tests pass. Check `git log` for correct `yasserrmd` author and committer, permitted email, and zero AI/co-author trailers. No squash. Never commit directly to main.

## PR, merge, and completion evidence

1. Push `milestone/m14-complete-web-workbench-and-usable-developer-experience`; create a PR into `main` with scope, code evidence, linked acceptance items and test results.
2. Wait for **actual** required CI checks and any branch-protection reviews. Fix on the same milestone branch using additional atomic commits; do not fake or bypass required checks.
3. Once green and authorized, merge through GitHub; verify merged SHA on main, pull updated main, and delete local and remote milestone branches.
4. Create `docs/milestone-reports/M14.md` from the supplied template with PR URL, merge SHA, actual commits, checks, evidence and risk summary. Ensure the report itself is versioned in the PR **before merging**, not written only after the branch is deleted.
5. Update `docs/PHASE_STATUS.md` and continue automatically to the next file. If external authorization/credential is missing, record `BLOCKED` and precise remediation, work only on independent safe tasks, and never report unperformed PR/merge as done.

**Explicit handoff to next phase:** This milestone is complete only when the delivered capability can be used in the UI/API and its full positive/negative tests pass with verifiable artifacts.
