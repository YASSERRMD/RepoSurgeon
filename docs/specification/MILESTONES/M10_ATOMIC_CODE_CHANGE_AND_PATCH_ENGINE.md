# RepoSurgeon — M10: Atomic Code Change and Patch Engine

> Execution order: M09 → M10. Required branch: `milestone/m10-atomic-code-change-and-patch-engine`. Execute one phase fully; do not claim completion until gates are actually green.
>
> Mandatory before starting: read `CLAUDE.md`, `RUN_FIRST.md`, `MASTER_IMPLEMENTATION_PLAN.md`, `ARCHITECTURE.md`, `GIT_WORKFLOW.md`, `SECURITY_GATES.md`, `API_CONTRACTS.md`, and all previous milestone completion reports. Follow the existing repository architecture where compatible; do not overwrite existing work.

## Objective and tangible outcome

Apply real cross-file source changes reliably and preserve an inspectable Git diff and reversible checkpoint.

**Definition of done:** A verified user-visible vertical slice running against actual Go backend + PostgreSQL + Vue frontend, with real tests and artifacts. No TODO implementations, mock production APIs, static demo data, unexplained skipped tests, or artificial success labels.

## Detailed implementation tasks — complete every item

1. Implement exact file read, search-and-replace and unified diff patch application with input hash/commit SHA preconditions.
2. Support adding/deleting/renaming files, executable bit changes, files with Unicode paths, multiple hunks and line ending preservation.
3. Reject patching protected files without explicit policy exception and reject symlink escapes, binary edits and size bombs by default.
4. Capture before/after hashes, patch hunks and affected symbols; maintain a checkpoint per attempted fix for rollback/retry.
5. Let Step 5 generate changes only through structured tool calls; do not treat Markdown patches as trusted shell scripts.
6. Run formatting only through allowed verified commands in sandbox and show exact format diffs.
7. Build diff viewer with side-by-side changes, per-file status, patch failures and undo/retry controls.
8. Add multi-module fixture requiring coordinated Go API, SQL migration and Vue client changes; validate all expected files changed.

## Required end-to-end interfaces

**Backend and OpenAPI:** POST /api/v1/runs/{id}/patches; GET /api/v1/runs/{id}/diff; POST /api/v1/runs/{id}/checkpoints/{checkpoint_id}/restore.

**Persistence and migration:** Patch attempts, file hashes, checkpoint manifests and diff artifacts.

**Frontend and interaction:** Real changed-file tree, syntax diff, apply/reject and restore interface.

For every new endpoint define request/response/error examples and access controls in OpenAPI; regenerate typed Vue clients and include drift checks. Use real PostgreSQL transactions and never invent successful adapter responses.

## Negative cases and security constraints

- No direct edits in base branch or outside task workspace.
- A patch applied successfully is not a completed task; verification phases are mandatory.

## Mandatory verification scenarios

- [ ] Apply multi-hunk patches and changes across multiple file types.
- [ ] Reject stale hash and symlink traversal without partial changes.
- [ ] Restore rollback to pre-patch SHA exactly after injected failure.
- [ ] Diff viewer reports same changes as `git diff --no-ext-diff`.

Also run the repository-wide regression suite, Go formatting/vet/lint/race tests, frontend formatting/lint/type-check/build/tests, generated contract drift, integration tests, secret scan, dependency/security audits, Docker image/Compose smoke, and existing E2E tests. A command that cannot run because of missing prerequisites is **BLOCKED**, not passed. Fix any introduced regression before creating a PR.

## Atomic commit map (suggested minimum)

1. `m10: patch parser/precondition checks`
2. `m10: atomic apply and rollback`
3. `m10: multi-file editing tool adapter`
4. `m10: diff and checkpoints UI`
5. `m10: replay/path safety suite`

Write each commit only after its contained work and relevant tests pass. Check `git log` for correct `yasserrmd` author and committer, permitted email, and zero AI/co-author trailers. No squash. Never commit directly to main.

## PR, merge, and completion evidence

1. Push `milestone/m10-atomic-code-change-and-patch-engine`; create a PR into `main` with scope, code evidence, linked acceptance items and test results.
2. Wait for **actual** required CI checks and any branch-protection reviews. Fix on the same milestone branch using additional atomic commits; do not fake or bypass required checks.
3. Once green and authorized, merge through GitHub; verify merged SHA on main, pull updated main, and delete local and remote milestone branches.
4. Create `docs/milestone-reports/M10.md` from the supplied template with PR URL, merge SHA, actual commits, checks, evidence and risk summary. Ensure the report itself is versioned in the PR **before merging**, not written only after the branch is deleted.
5. Update `docs/PHASE_STATUS.md` and continue automatically to the next file. If external authorization/credential is missing, record `BLOCKED` and precise remediation, work only on independent safe tasks, and never report unperformed PR/merge as done.

**Explicit handoff to next phase:** This milestone is complete only when the delivered capability can be used in the UI/API and its full positive/negative tests pass with verifiable artifacts.
