# RepoSurgeon — M07: Isolated Tool Execution and Human Approval Gateway

> Execution order: M06 → M07. Required branch: `milestone/m07-isolated-tool-execution-and-human-approval-gateway`. Execute one phase fully; do not claim completion until gates are actually green.
>
> Mandatory before starting: read `CLAUDE.md`, `RUN_FIRST.md`, `MASTER_IMPLEMENTATION_PLAN.md`, `ARCHITECTURE.md`, `GIT_WORKFLOW.md`, `SECURITY_GATES.md`, `API_CONTRACTS.md`, and all previous milestone completion reports. Follow the existing repository architecture where compatible; do not overwrite existing work.

## Objective and tangible outcome

Provide real controlled tooling while ensuring model tool requests never become unchecked host commands.

**Definition of done:** A verified user-visible vertical slice running against actual Go backend + PostgreSQL + Vue frontend, with real tests and artifacts. No TODO implementations, mock production APIs, static demo data, unexplained skipped tests, or artificial success labels.

## Detailed implementation tasks — complete every item

1. Build typed tool registry with `file_read`, `file_search`, `list_tree`, `apply_patch`, `run_checks`, `git_status`, `git_diff`, `request_approval` and explicit permission/budget metadata.
2. Implement runner agent as a separate trust boundary using rootless or similarly restricted containers with CPU/memory/PID/time quotas, non-root UID, dropped capabilities, no host Docker socket and no live credentials.
3. Keep network disabled for repo execution by default; provide a separate audited dependency acquisition mechanism with an explicit allowlist when required.
4. Canonicalize and constrain paths under task checkout, restrict hidden/protected paths, safely decode tool JSON arguments and cap output sizes.
5. Classify sensitive actions (`run_check` with untrusted scripts, write outside allowed paths, create PR, merge, delete) and require server-side policy checks plus human approval where specified.
6. Persist typed tool invocation, risk classification, approval request/response, exit code and bounded stdout/stderr after secret filtering.
7. Implement an approval inbox that displays proposed actions, exact diffs/commands, expiry and approving identity; prevent users approving their own forbidden actions if policy disallows.
8. Exercise malicious issue text, injected instructions in files, command injection strings, symlink traversal and attempts to read host files.

## Required end-to-end interfaces

**Backend and OpenAPI:** POST /api/v1/runs/{id}/approvals/{approval_id}/approve|reject; GET /api/v1/approvals; GET /api/v1/runs/{id}/tools.

**Persistence and migration:** Tool catalog, executions, effect id, policy decisions and approval records with immutable audit chain.

**Frontend and interaction:** Approval inbox and detailed real command/patch preview; safe deny and expiration states.

For every new endpoint define request/response/error examples and access controls in OpenAPI; regenerate typed Vue clients and include drift checks. Use real PostgreSQL transactions and never invent successful adapter responses.

## Negative cases and security constraints

- Never run arbitrary LLM-generated shell commands directly on host.
- Prompt injection is data; only deterministic authorization policy grants actions.

## Mandatory verification scenarios

- [ ] Execute approved checks inside isolated runner on a fixture repo.
- [ ] Prove attempted read of host root, `.env`, credential directory, and Docker socket fails.
- [ ] Malformed arguments, oversized stdout and timeout return bounded error without runaway job.
- [ ] Unapproved high-risk tool action is never executed.

Also run the repository-wide regression suite, Go formatting/vet/lint/race tests, frontend formatting/lint/type-check/build/tests, generated contract drift, integration tests, secret scan, dependency/security audits, Docker image/Compose smoke, and existing E2E tests. A command that cannot run because of missing prerequisites is **BLOCKED**, not passed. Fix any introduced regression before creating a PR.

## Atomic commit map (suggested minimum)

1. `m07: tool schemas/dispatch`
2. `m07: sandbox runner and quotas`
3. `m07: policy and approval gateway`
4. `m07: approval UI / event logs`
5. `m07: adversarial tests and cleanup`

Write each commit only after its contained work and relevant tests pass. Check `git log` for correct `yasserrmd` author and committer, permitted email, and zero AI/co-author trailers. No squash. Never commit directly to main.

## PR, merge, and completion evidence

1. Push `milestone/m07-isolated-tool-execution-and-human-approval-gateway`; create a PR into `main` with scope, code evidence, linked acceptance items and test results.
2. Wait for **actual** required CI checks and any branch-protection reviews. Fix on the same milestone branch using additional atomic commits; do not fake or bypass required checks.
3. Once green and authorized, merge through GitHub; verify merged SHA on main, pull updated main, and delete local and remote milestone branches.
4. Create `docs/milestone-reports/M07.md` from the supplied template with PR URL, merge SHA, actual commits, checks, evidence and risk summary. Ensure the report itself is versioned in the PR **before merging**, not written only after the branch is deleted.
5. Update `docs/PHASE_STATUS.md` and continue automatically to the next file. If external authorization/credential is missing, record `BLOCKED` and precise remediation, work only on independent safe tasks, and never report unperformed PR/merge as done.

**Explicit handoff to next phase:** This milestone is complete only when the delivered capability can be used in the UI/API and its full positive/negative tests pass with verifiable artifacts.
