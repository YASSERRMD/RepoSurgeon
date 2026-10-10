# RepoSurgeon — M06: Durable Agent Run State Machine and Worker Scheduling

> Execution order: M05 → M06. Required branch: `milestone/m06-durable-agent-run-state-machine-and-worker-scheduling`. Execute one phase fully; do not claim completion until gates are actually green.
>
> Mandatory before starting: read `CLAUDE.md`, `RUN_FIRST.md`, `MASTER_IMPLEMENTATION_PLAN.md`, `ARCHITECTURE.md`, `GIT_WORKFLOW.md`, `SECURITY_GATES.md`, `API_CONTRACTS.md`, and all previous milestone completion reports. Follow the existing repository architecture where compatible; do not overwrite existing work.

## Objective and tangible outcome

Execute long-horizon tasks across restarts with resumability, explicit states, approvals and correct concurrency.

**Definition of done:** A verified user-visible vertical slice running against actual Go backend + PostgreSQL + Vue frontend, with real tests and artifacts. No TODO implementations, mock production APIs, static demo data, unexplained skipped tests, or artificial success labels.

## Detailed implementation tasks — complete every item

1. Implement persisted states `queued`, `intaking`, `analyzing`, `planning`, `awaiting_approval`, `editing`, `testing`, `reviewing`, `pr_ready`, `pr_open`, `completed`, `failed`, `cancelled`, `blocked` with legal transitions.
2. Implement DB-backed queue using `FOR UPDATE SKIP LOCKED`, per-project concurrency limits, leases, heartbeat, retries and durable cancel/pause/resume commands.
3. Persist each step attempt, tool call, result summary, artifact references, timeout, model usage and correlation ID before exposing user status.
4. Use explicit transaction boundaries and deduplication keys so worker crashes cannot duplicate tool effects, commits or PRs.
5. Define budget policies for wall-clock, tokens, provider cost, tool count and retry count; enforce them server-side independent of prompt instructions.
6. Implement scheduler claiming fairness across tenants and per-run resource locking to prevent two workers mutating the same worktree.
7. Implement task creation, start, stop, retry and state history UI with optimistic client updates reconciled against server truth.
8. Add chaos tests that kill a worker at each state boundary then restart it and verify event/outcome consistency.

## Required end-to-end interfaces

**Backend and OpenAPI:** POST /api/v1/runs; POST /api/v1/runs/{id}/start|pause|resume|cancel; GET /api/v1/runs/{id}; GET /api/v1/runs/{id}/steps.

**Persistence and migration:** Job queue, state transition ledger, leases, heartbeat and unique effect IDs.

**Frontend and interaction:** Functional run queue, status history, start/pause/resume/cancel controls bound to real backend.

For every new endpoint define request/response/error examples and access controls in OpenAPI; regenerate typed Vue clients and include drift checks. Use real PostgreSQL transactions and never invent successful adapter responses.

## Negative cases and security constraints

- Do not transition to success based only on the model assertion.
- Approvals and merge require authoritative server-side checks, not a state stored in an assistant message.

## Mandatory verification scenarios

- [ ] Worker restart recovers queued, running and interrupted tasks with no duplicate operations.
- [ ] Concurrent workers cannot claim same run/worktree.
- [ ] Cancel and budget expiration terminate execution and clean workspace.
- [ ] Playwright verifies live states and control permissions.

Also run the repository-wide regression suite, Go formatting/vet/lint/race tests, frontend formatting/lint/type-check/build/tests, generated contract drift, integration tests, secret scan, dependency/security audits, Docker image/Compose smoke, and existing E2E tests. A command that cannot run because of missing prerequisites is **BLOCKED**, not passed. Fix any introduced regression before creating a PR.

## Atomic commit map (suggested minimum)

1. `m06: state machine and event schema`
2. `m06: queue/leases worker`
3. `m06: budgets/cancellation`
4. `m06: run controls UI`
5. `m06: crash and concurrency suite`

Write each commit only after its contained work and relevant tests pass. Check `git log` for correct `yasserrmd` author and committer, permitted email, and zero AI/co-author trailers. No squash. Never commit directly to main.

## PR, merge, and completion evidence

1. Push `milestone/m06-durable-agent-run-state-machine-and-worker-scheduling`; create a PR into `main` with scope, code evidence, linked acceptance items and test results.
2. Wait for **actual** required CI checks and any branch-protection reviews. Fix on the same milestone branch using additional atomic commits; do not fake or bypass required checks.
3. Once green and authorized, merge through GitHub; verify merged SHA on main, pull updated main, and delete local and remote milestone branches.
4. Create `docs/milestone-reports/M06.md` from the supplied template with PR URL, merge SHA, actual commits, checks, evidence and risk summary. Ensure the report itself is versioned in the PR **before merging**, not written only after the branch is deleted.
5. Update `docs/PHASE_STATUS.md` and continue automatically to the next file. If external authorization/credential is missing, record `BLOCKED` and precise remediation, work only on independent safe tasks, and never report unperformed PR/merge as done.

**Explicit handoff to next phase:** This milestone is complete only when the delivered capability can be used in the UI/API and its full positive/negative tests pass with verifiable artifacts.
