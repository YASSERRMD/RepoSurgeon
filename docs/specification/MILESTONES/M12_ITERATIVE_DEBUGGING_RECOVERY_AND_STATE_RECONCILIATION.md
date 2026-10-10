# RepoSurgeon — M12: Iterative Debugging, Recovery, and State Reconciliation

> Execution order: M11 → M12. Required branch: `milestone/m12-iterative-debugging-recovery-and-state-reconciliation`. Execute one phase fully; do not claim completion until gates are actually green.
>
> Mandatory before starting: read `CLAUDE.md`, `RUN_FIRST.md`, `MASTER_IMPLEMENTATION_PLAN.md`, `ARCHITECTURE.md`, `GIT_WORKFLOW.md`, `SECURITY_GATES.md`, `API_CONTRACTS.md`, and all previous milestone completion reports. Follow the existing repository architecture where compatible; do not overwrite existing work.

## Objective and tangible outcome

Make the agent recover from broken patches, tool errors and model failures without losing correctness or control.

**Definition of done:** A verified user-visible vertical slice running against actual Go backend + PostgreSQL + Vue frontend, with real tests and artifacts. No TODO implementations, mock production APIs, static demo data, unexplained skipped tests, or artificial success labels.

## Detailed implementation tasks — complete every item

1. Implement feedback loop: observe failing diagnostics → formulate focused hypothesis → inspect evidence → patch → rerun relevant checks → expand/full checks.
2. Set bounded retry policies per error class and track fix attempts, repeated edits, failure fingerprints and progress metric per attempt.
3. Detect loops (identical patch repeatedly applied, no test delta, unchanged tool error) and stop as `blocked` with evidence instead of burning tokens.
4. Provide deterministic recovery for HTTP 429/timeouts, interrupted SSE, expired GitHub credentials, dead worker leases and lost containers.
5. Ensure idempotency and effect receipts prevent duplicate patch commits, tool executions and PR creation after replay.
6. Introduce explicit safe rollback to checkpoints when later iterations deteriorate the test suite.
7. Surface recovery timeline, reason, actions and user pause/stop/retry options in UI.
8. Build fault injection scenarios and concurrency tests; compare state before and after restart and measure task outcome.

## Required end-to-end interfaces

**Backend and OpenAPI:** POST /api/v1/runs/{id}/recover; GET /api/v1/runs/{id}/recovery; POST /api/v1/runs/{id}/rollback.

**Persistence and migration:** Failure fingerprint, attempt budget, state recovery journal and effect receipts.

**Frontend and interaction:** Recovery and retries timeline, blocked action details, checkpoint rollback.

For every new endpoint define request/response/error examples and access controls in OpenAPI; regenerate typed Vue clients and include drift checks. Use real PostgreSQL transactions and never invent successful adapter responses.

## Negative cases and security constraints

- Do not hide unresolved faults in success dashboards.
- After repeated failures, keep branch and evidence intact; do not merge.

## Mandatory verification scenarios

- [ ] Seed first-edit failure then verify second fix is correct and full suite passes.
- [ ] Provider 429 and worker crash recover without repeated side effects.
- [ ] Identical broken action reaches blocked state within bounded attempts.
- [ ] Rollback restores known-good state and changes verdict accordingly.

Also run the repository-wide regression suite, Go formatting/vet/lint/race tests, frontend formatting/lint/type-check/build/tests, generated contract drift, integration tests, secret scan, dependency/security audits, Docker image/Compose smoke, and existing E2E tests. A command that cannot run because of missing prerequisites is **BLOCKED**, not passed. Fix any introduced regression before creating a PR.

## Atomic commit map (suggested minimum)

1. `m12: failure classifier/retry state`
2. `m12: diagnostic feedback loop`
3. `m12: checkpoint rollback and receipts`
4. `m12: recovery UI`
5. `m12: fault-injection suite`

Write each commit only after its contained work and relevant tests pass. Check `git log` for correct `yasserrmd` author and committer, permitted email, and zero AI/co-author trailers. No squash. Never commit directly to main.

## PR, merge, and completion evidence

1. Push `milestone/m12-iterative-debugging-recovery-and-state-reconciliation`; create a PR into `main` with scope, code evidence, linked acceptance items and test results.
2. Wait for **actual** required CI checks and any branch-protection reviews. Fix on the same milestone branch using additional atomic commits; do not fake or bypass required checks.
3. Once green and authorized, merge through GitHub; verify merged SHA on main, pull updated main, and delete local and remote milestone branches.
4. Create `docs/milestone-reports/M12.md` from the supplied template with PR URL, merge SHA, actual commits, checks, evidence and risk summary. Ensure the report itself is versioned in the PR **before merging**, not written only after the branch is deleted.
5. Update `docs/PHASE_STATUS.md` and continue automatically to the next file. If external authorization/credential is missing, record `BLOCKED` and precise remediation, work only on independent safe tasks, and never report unperformed PR/merge as done.

**Explicit handoff to next phase:** This milestone is complete only when the delivered capability can be used in the UI/API and its full positive/negative tests pass with verifiable artifacts.
