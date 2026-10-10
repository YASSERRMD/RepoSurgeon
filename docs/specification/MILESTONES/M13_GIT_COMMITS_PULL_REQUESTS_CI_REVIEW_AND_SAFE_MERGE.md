# RepoSurgeon — M13: Git Commits, Pull Requests, CI Review, and Safe Merge

> Execution order: M12 → M13. Required branch: `milestone/m13-git-commits-pull-requests-ci-review-and-safe-merge`. Execute one phase fully; do not claim completion until gates are actually green.
>
> Mandatory before starting: read `CLAUDE.md`, `RUN_FIRST.md`, `MASTER_IMPLEMENTATION_PLAN.md`, `ARCHITECTURE.md`, `GIT_WORKFLOW.md`, `SECURITY_GATES.md`, `API_CONTRACTS.md`, and all previous milestone completion reports. Follow the existing repository architecture where compatible; do not overwrite existing work.

## Objective and tangible outcome

Complete issue-to-PR lifecycle with real GitHub operations, protected-branch rules and accurate merge outcomes.

**Definition of done:** A verified user-visible vertical slice running against actual Go backend + PostgreSQL + Vue frontend, with real tests and artifacts. No TODO implementations, mock production APIs, static demo data, unexplained skipped tests, or artificial success labels.

## Detailed implementation tasks — complete every item

1. Implement task-branch commit composition with per-logical-change atomic commits; verify author/committer identity configured for RepoSurgeon-controlled commits.
2. Before push, require clean required local checks, changed-file allowlist, secret scan and unchanged pinned base or explicit rebase/retest.
3. Push branch using short-lived GitHub App installation token only through trusted Git operations service; never expose token to runner.
4. Create a single idempotent GitHub PR referencing issue, structured summary, test evidence, known risks and generated change list.
5. Poll or webhook-track real GitHub required checks, review approvals, mergeability status and branch protection; distinguish pending, failed, blocked, approved.
6. Require explicit merge authorization by a permitted human/repo policy, and do not bypass protections even if model says merge; no direct writes to main.
7. On successful confirmed merge, verify resulting merge SHA is on main and delete remote/local feature branch; never delete an unmerged branch.
8. Link task ↔ PR ↔ CI ↔ merge SHA ↔ deleted branch status in database and frontend; preserve all audit records.
9. Handle PR conflict, external commits, failed review, stale checks and rate limits; no retries that duplicate PRs or merge prematurely.

## Required end-to-end interfaces

**Backend and OpenAPI:** POST /api/v1/runs/{id}/pr; GET /api/v1/runs/{id}/pr; POST /api/v1/runs/{id}/merge; POST /api/v1/runs/{id}/abandon.

**Persistence and migration:** Git commit metadata, PR ref, CI check runs, required reviews, approved merge action and merge SHA.

**Frontend and interaction:** PR lifecycle view with exact GitHub link, check status, risk review, approval and merge result.

For every new endpoint define request/response/error examples and access controls in OpenAPI; regenerate typed Vue clients and include drift checks. Use real PostgreSQL transactions and never invent successful adapter responses.

## Negative cases and security constraints

- Git commit identity is not GitHub account authentication; use only currently authenticated authorized identity.
- For project-building milestone PRs, follow the stricter CLAUDE.md Git lifecycle.

## Mandatory verification scenarios

- [ ] Use disposable GitHub fixture repo for real smoke or a faithful GitHub fake for CI contract suite.
- [ ] Confirm create PR → failing CI → blocked merge → green CI → approval → merge → branch deletion.
- [ ] No PR/merge on non-authorized repo, unreviewed changes, or missing checks.
- [ ] Simulate repeated webhooks, concurrent merge requests and network retry; exactly one PR/merge.

Also run the repository-wide regression suite, Go formatting/vet/lint/race tests, frontend formatting/lint/type-check/build/tests, generated contract drift, integration tests, secret scan, dependency/security audits, Docker image/Compose smoke, and existing E2E tests. A command that cannot run because of missing prerequisites is **BLOCKED**, not passed. Fix any introduced regression before creating a PR.

## Atomic commit map (suggested minimum)

1. `m13: commit composition and identity enforcement`
2. `m13: GitHub push/PR adapter`
3. `m13: CI checks and policy merge gate`
4. `m13: PR UI and deletion tracking`
5. `m13: real disposable-repo or contract E2E`

Write each commit only after its contained work and relevant tests pass. Check `git log` for correct `yasserrmd` author and committer, permitted email, and zero AI/co-author trailers. No squash. Never commit directly to main.

## PR, merge, and completion evidence

1. Push `milestone/m13-git-commits-pull-requests-ci-review-and-safe-merge`; create a PR into `main` with scope, code evidence, linked acceptance items and test results.
2. Wait for **actual** required CI checks and any branch-protection reviews. Fix on the same milestone branch using additional atomic commits; do not fake or bypass required checks.
3. Once green and authorized, merge through GitHub; verify merged SHA on main, pull updated main, and delete local and remote milestone branches.
4. Create `docs/milestone-reports/M13.md` from the supplied template with PR URL, merge SHA, actual commits, checks, evidence and risk summary. Ensure the report itself is versioned in the PR **before merging**, not written only after the branch is deleted.
5. Update `docs/PHASE_STATUS.md` and continue automatically to the next file. If external authorization/credential is missing, record `BLOCKED` and precise remediation, work only on independent safe tasks, and never report unperformed PR/merge as done.

**Explicit handoff to next phase:** This milestone is complete only when the delivered capability can be used in the UI/API and its full positive/negative tests pass with verifiable artifacts.
