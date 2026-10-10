# RepoSurgeon — M05: Safe Repository Checkout, Branching, and Worktree Management

> Execution order: M04 → M05. Required branch: `milestone/m05-safe-repository-checkout-branching-and-worktree-management`. Execute one phase fully; do not claim completion until gates are actually green.
>
> Mandatory before starting: read `CLAUDE.md`, `RUN_FIRST.md`, `MASTER_IMPLEMENTATION_PLAN.md`, `ARCHITECTURE.md`, `GIT_WORKFLOW.md`, `SECURITY_GATES.md`, `API_CONTRACTS.md`, and all previous milestone completion reports. Follow the existing repository architecture where compatible; do not overwrite existing work.

## Objective and tangible outcome

Create isolated, reproducible source workspaces without corrupting main or exposing host credentials.

**Definition of done:** A verified user-visible vertical slice running against actual Go backend + PostgreSQL + Vue frontend, with real tests and artifacts. No TODO implementations, mock production APIs, static demo data, unexplained skipped tests, or artificial success labels.

## Detailed implementation tasks — complete every item

1. Build a git workspace manager that fetches a selected commit SHA using a short-lived installation token in a trusted fetch service; strip it before entering the executor.
2. Pin immutable baseline commit SHA and default branch; create deterministic per-task branch `reposurgeon/task-<short-id>` and a separate git worktree.
3. Check branch collisions, dirty worktree, divergent default branch, nested repositories, submodules, large-files and repository size limits.
4. Persist workspace lifecycle (`requested`, `cloning`, `ready`, `dirty`, `archived`, `failed`) and map owner/task to workspace; clean up on cancel and timeout.
5. Implement strict canonical path resolution including symlinks, path traversal and protected directories; record file hashing and Git status before edits.
6. Support Go, TypeScript and Python fixtures and monorepos, with correctly detected root commands; do not invent unverified build commands.
7. Provide browsable repository tree, branch/base SHA and real file preview UI, with safe limits on binary/huge files.
8. Test failed clones, renames, submodules, file mode changes, and cleanup even after worker crash.

## Required end-to-end interfaces

**Backend and OpenAPI:** POST /api/v1/workspaces; GET /api/v1/workspaces/{id}/tree; GET /api/v1/workspaces/{id}/files?path=; DELETE /api/v1/workspaces/{id}.

**Persistence and migration:** Workspace IDs, pinned SHA, branch, file-index metadata, status transitions and cleanup leases.

**Frontend and interaction:** Repository tree and syntax-highlighted safe file preview; visible SHA and worktree status.

For every new endpoint define request/response/error examples and access controls in OpenAPI; regenerate typed Vue clients and include drift checks. Use real PostgreSQL transactions and never invent successful adapter responses.

## Negative cases and security constraints

- No direct modification or push to `main`; all task changes stay isolated.
- No model-supplied shell command is granted GitHub authentication.

## Mandatory verification scenarios

- [ ] Clone from fixture Git repository, branch, edit and clean up; assert source default branch unchanged.
- [ ] Reject `../`, symlink escapes and prohibited root paths.
- [ ] Simulate interrupted checkout and recover / clean orphaned worktree.
- [ ] Browser browse tree and file details on actual fixture repo.

Also run the repository-wide regression suite, Go formatting/vet/lint/race tests, frontend formatting/lint/type-check/build/tests, generated contract drift, integration tests, secret scan, dependency/security audits, Docker image/Compose smoke, and existing E2E tests. A command that cannot run because of missing prerequisites is **BLOCKED**, not passed. Fix any introduced regression before creating a PR.

## Atomic commit map (suggested minimum)

1. `m05: git workspace lifecycle`
2. `m05: checkout/branch and cleanup`
3. `m05: path-safety and crash recovery`
4. `m05: read-only repo browser`
5. `m05: integration threat cases`

Write each commit only after its contained work and relevant tests pass. Check `git log` for correct `yasserrmd` author and committer, permitted email, and zero AI/co-author trailers. No squash. Never commit directly to main.

## PR, merge, and completion evidence

1. Push `milestone/m05-safe-repository-checkout-branching-and-worktree-management`; create a PR into `main` with scope, code evidence, linked acceptance items and test results.
2. Wait for **actual** required CI checks and any branch-protection reviews. Fix on the same milestone branch using additional atomic commits; do not fake or bypass required checks.
3. Once green and authorized, merge through GitHub; verify merged SHA on main, pull updated main, and delete local and remote milestone branches.
4. Create `docs/milestone-reports/M05.md` from the supplied template with PR URL, merge SHA, actual commits, checks, evidence and risk summary. Ensure the report itself is versioned in the PR **before merging**, not written only after the branch is deleted.
5. Update `docs/PHASE_STATUS.md` and continue automatically to the next file. If external authorization/credential is missing, record `BLOCKED` and precise remediation, work only on independent safe tasks, and never report unperformed PR/merge as done.

**Explicit handoff to next phase:** This milestone is complete only when the delivered capability can be used in the UI/API and its full positive/negative tests pass with verifiable artifacts.
