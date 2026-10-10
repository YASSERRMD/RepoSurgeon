# RepoSurgeon — M04: GitHub Integration, Repository Registration, and Issue Sync

> Execution order: M03 → M04. Required branch: `milestone/m04-github-integration-repository-registration-and-issue-sync`. Execute one phase fully; do not claim completion until gates are actually green.
>
> Mandatory before starting: read `CLAUDE.md`, `RUN_FIRST.md`, `MASTER_IMPLEMENTATION_PLAN.md`, `ARCHITECTURE.md`, `GIT_WORKFLOW.md`, `SECURITY_GATES.md`, `API_CONTRACTS.md`, and all previous milestone completion reports. Follow the existing repository architecture where compatible; do not overwrite existing work.

## Objective and tangible outcome

Connect authorized repositories and import issues with accurate permission, ownership and synchronization semantics.

**Definition of done:** A verified user-visible vertical slice running against actual Go backend + PostgreSQL + Vue frontend, with real tests and artifacts. No TODO implementations, mock production APIs, static demo data, unexplained skipped tests, or artificial success labels.

## Detailed implementation tasks — complete every item

1. Implement GitHub App authorization/install flow using least-required permissions; verify signatures and state/nonce where applicable; bind installations to organizations and repos.
2. Discover accessible repos, default branch, protected-branch settings, issue labels, issue comments and PR status without broad organization access.
3. Support selecting a repository and importing issues by URL, ID or list; normalize title, body, labels, comments, assignees and timestamps.
4. Verify webhook HMAC signatures, reject replayed deliveries, deduplicate delivery IDs, queue sync jobs and handle deletion/uninstallation cleanly.
5. Use ETags/conditional requests and rate-limit handling for rescans; respect GitHub permission errors and expose them in UI.
6. Implement repository picker, import/sync UX, issue detail view with actual body/comments and status.
7. Ensure untrusted issue descriptions are marked as data and cannot authorize execution or change system policy.
8. Build fake GitHub server + webhook fixtures to exercise real request/response paths without tokens in CI.

## Required end-to-end interfaces

**Backend and OpenAPI:** POST /api/v1/github/installations/connect; GET /api/v1/github/repos; POST /api/v1/projects/{id}/repository; POST /api/v1/issues/import; POST /api/v1/webhooks/github.

**Persistence and migration:** GitHub installation/repository bindings, issue snapshots, external event IDs and sync cursors.

**Frontend and interaction:** Installation status, searchable repo picker, issue import, refresh and permission failure explanations.

For every new endpoint define request/response/error examples and access controls in OpenAPI; regenerate typed Vue clients and include drift checks. Use real PostgreSQL transactions and never invent successful adapter responses.

## Negative cases and security constraints

- Do not start code execution from webhooks without a separately authorized task start.
- Do not infer GitHub App identity from configured local Git author name.

## Mandatory verification scenarios

- [ ] End-to-end mock installation, repo selection, issue import and webhook update.
- [ ] Invalid signatures/replayed IDs do not mutate state.
- [ ] Cross-org GitHub installation and repo access denied.
- [ ] GitHub 403/404/429 and renamed/archived repos handled.

Also run the repository-wide regression suite, Go formatting/vet/lint/race tests, frontend formatting/lint/type-check/build/tests, generated contract drift, integration tests, secret scan, dependency/security audits, Docker image/Compose smoke, and existing E2E tests. A command that cannot run because of missing prerequisites is **BLOCKED**, not passed. Fix any introduced regression before creating a PR.

## Atomic commit map (suggested minimum)

1. `m04: GitHub adapter and auth handshake`
2. `m04: repository and issue sync schemas`
3. `m04: webhook verifier and replay protection`
4. `m04: repo/issue Vue workflows`
5. `m04: integration and negative tests`

Write each commit only after its contained work and relevant tests pass. Check `git log` for correct `yasserrmd` author and committer, permitted email, and zero AI/co-author trailers. No squash. Never commit directly to main.

## PR, merge, and completion evidence

1. Push `milestone/m04-github-integration-repository-registration-and-issue-sync`; create a PR into `main` with scope, code evidence, linked acceptance items and test results.
2. Wait for **actual** required CI checks and any branch-protection reviews. Fix on the same milestone branch using additional atomic commits; do not fake or bypass required checks.
3. Once green and authorized, merge through GitHub; verify merged SHA on main, pull updated main, and delete local and remote milestone branches.
4. Create `docs/milestone-reports/M04.md` from the supplied template with PR URL, merge SHA, actual commits, checks, evidence and risk summary. Ensure the report itself is versioned in the PR **before merging**, not written only after the branch is deleted.
5. Update `docs/PHASE_STATUS.md` and continue automatically to the next file. If external authorization/credential is missing, record `BLOCKED` and precise remediation, work only on independent safe tasks, and never report unperformed PR/merge as done.

**Explicit handoff to next phase:** This milestone is complete only when the delivered capability can be used in the UI/API and its full positive/negative tests pass with verifiable artifacts.
