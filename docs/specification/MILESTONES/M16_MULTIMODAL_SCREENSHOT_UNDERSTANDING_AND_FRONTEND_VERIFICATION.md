# RepoSurgeon — M16: Multimodal Screenshot Understanding and Frontend Verification

> Execution order: M15 → M16. Required branch: `milestone/m16-multimodal-screenshot-understanding-and-frontend-verification`. Execute one phase fully; do not claim completion until gates are actually green.
>
> Mandatory before starting: read `CLAUDE.md`, `RUN_FIRST.md`, `MASTER_IMPLEMENTATION_PLAN.md`, `ARCHITECTURE.md`, `GIT_WORKFLOW.md`, `SECURITY_GATES.md`, `API_CONTRACTS.md`, and all previous milestone completion reports. Follow the existing repository architecture where compatible; do not overwrite existing work.

## Objective and tangible outcome

Test Step 5 vision features through actual screenshots and verified UI bugfix tasks.

**Definition of done:** A verified user-visible vertical slice running against actual Go backend + PostgreSQL + Vue frontend, with real tests and artifacts. No TODO implementations, mock production APIs, static demo data, unexplained skipped tests, or artificial success labels.

## Detailed implementation tasks — complete every item

1. Create browser screenshot capture in sandbox for authorized test applications with bounded images and stable viewport settings.
2. Send screenshot to Step 5 through supported image_url data/URL payload along with DOM/accessibility evidence and task instructions.
3. Generate hypotheses about visible UI defects, identify relevant code, patch and run component/browser checks.
4. Compare baseline vs changed screenshots using deterministic pixel/structural metrics and thresholds; use model opinion only as supplementary explanation.
5. Add screenshot attachments, viewport variants and accessible visual issue reports to run artifacts.
6. Implement screenshot redaction before provider transfer for potentially private content, with explicit approval where needed.
7. Create 3 demo fixture bugs: clipped dialog, broken mobile layout, misaligned action button; verify real changes repair both behavior and visual checks.
8. Add frontend side-by-side image results and regression verdicts to the workbench.

## Required end-to-end interfaces

**Backend and OpenAPI:** POST /api/v1/runs/{id}/screenshots/capture; GET /api/v1/runs/{id}/visual-results.

**Persistence and migration:** Screenshot artifact metadata, viewport, baseline/after revision, visual diff metrics and evidence.

**Frontend and interaction:** Before/after screenshot comparer, attached visual defect and confidence explanations.

For every new endpoint define request/response/error examples and access controls in OpenAPI; regenerate typed Vue clients and include drift checks. Use real PostgreSQL transactions and never invent successful adapter responses.

## Negative cases and security constraints

- Image interpretation is not a substitute for test verdict or human privacy review.
- Never capture third-party UIs or private pages without permission.

## Mandatory verification scenarios

- [ ] Capture and inspect real generated screenshots across viewport sizes.
- [ ] Seed visual defects; fix in code and require deterministic visual+functional passing checks.
- [ ] Redaction/consent tests ensure sensitive screens not shipped to model unexpectedly.
- [ ] Provider vision unavailable returns explicit failed/blocked state, not fabricated inspection.

Also run the repository-wide regression suite, Go formatting/vet/lint/race tests, frontend formatting/lint/type-check/build/tests, generated contract drift, integration tests, secret scan, dependency/security audits, Docker image/Compose smoke, and existing E2E tests. A command that cannot run because of missing prerequisites is **BLOCKED**, not passed. Fix any introduced regression before creating a PR.

## Atomic commit map (suggested minimum)

1. `m16: screenshot runner`
2. `m16: multimodal adapter and data handling`
3. `m16: visual comparison test protocol`
4. `m16: before-after UI`
5. `m16: three reproducible visual fixtures`

Write each commit only after its contained work and relevant tests pass. Check `git log` for correct `yasserrmd` author and committer, permitted email, and zero AI/co-author trailers. No squash. Never commit directly to main.

## PR, merge, and completion evidence

1. Push `milestone/m16-multimodal-screenshot-understanding-and-frontend-verification`; create a PR into `main` with scope, code evidence, linked acceptance items and test results.
2. Wait for **actual** required CI checks and any branch-protection reviews. Fix on the same milestone branch using additional atomic commits; do not fake or bypass required checks.
3. Once green and authorized, merge through GitHub; verify merged SHA on main, pull updated main, and delete local and remote milestone branches.
4. Create `docs/milestone-reports/M16.md` from the supplied template with PR URL, merge SHA, actual commits, checks, evidence and risk summary. Ensure the report itself is versioned in the PR **before merging**, not written only after the branch is deleted.
5. Update `docs/PHASE_STATUS.md` and continue automatically to the next file. If external authorization/credential is missing, record `BLOCKED` and precise remediation, work only on independent safe tasks, and never report unperformed PR/merge as done.

**Explicit handoff to next phase:** This milestone is complete only when the delivered capability can be used in the UI/API and its full positive/negative tests pass with verifiable artifacts.
