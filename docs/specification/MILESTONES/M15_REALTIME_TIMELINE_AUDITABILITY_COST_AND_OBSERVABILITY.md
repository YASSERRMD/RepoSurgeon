# RepoSurgeon — M15: Realtime Timeline, Auditability, Cost, and Observability

> Execution order: M14 → M15. Required branch: `milestone/m15-realtime-timeline-auditability-cost-and-observability`. Execute one phase fully; do not claim completion until gates are actually green.
>
> Mandatory before starting: read `CLAUDE.md`, `RUN_FIRST.md`, `MASTER_IMPLEMENTATION_PLAN.md`, `ARCHITECTURE.md`, `GIT_WORKFLOW.md`, `SECURITY_GATES.md`, `API_CONTRACTS.md`, and all previous milestone completion reports. Follow the existing repository architecture where compatible; do not overwrite existing work.

## Objective and tangible outcome

Make agent execution explainable with durable events and accurate operational metrics.

**Definition of done:** A verified user-visible vertical slice running against actual Go backend + PostgreSQL + Vue frontend, with real tests and artifacts. No TODO implementations, mock production APIs, static demo data, unexplained skipped tests, or artificial success labels.

## Detailed implementation tasks — complete every item

1. Emit structured run events with sequence ID, timestamp, causation ID and sanitized payload; persist first, stream second.
2. Expose SSE feed with Last-Event-ID resume, heartbeat and cursor pagination; reconnect without event gaps or duplicates.
3. Show step, tool, test, approval, PR and recovery status on a live timeline grounded in events, not hidden model chain-of-thought.
4. Instrument OpenTelemetry spans across HTTP, DB, queue, model and runner; expose health, latency and failure metrics.
5. Compute measured token counts and USD cost using model usage; separate cached, uncached input and output where provider reports them.
6. Set alert conditions for stuck leases, growing queue age, failed CI, model rate-limit pressure, unexpected secret denials and runaway cost.
7. Support exporting sanitized run trace in JSON plus summary Markdown with evidence and exact git/PR references.
8. Apply explicit telemetry retention and redaction controls; restrict access per project membership.

## Required end-to-end interfaces

**Backend and OpenAPI:** GET /api/v1/runs/{id}/events?after=; GET /api/v1/runs/{id}/events/stream; GET /api/v1/metrics/cost; GET /api/v1/runs/{id}/export.

**Persistence and migration:** Append-only sequenced events, usage/cost records, export metadata and retention policy.

**Frontend and interaction:** Live timeline + real usage/cost and trace export, keyboard accessible and reconnecting.

For every new endpoint define request/response/error examples and access controls in OpenAPI; regenerate typed Vue clients and include drift checks. Use real PostgreSQL transactions and never invent successful adapter responses.

## Negative cases and security constraints

- Do not expose private internal model reasoning or hidden chain-of-thought as a product requirement.
- Never store raw credentials, full auth headers or sensitive data in traces.

## Mandatory verification scenarios

- [ ] Drop/reconnect SSE and verify sequence continuity, replay and no lost events.
- [ ] Cross-tenant event feed and export denied.
- [ ] Compare token/cost aggregation with fixture provider usage exactly.
- [ ] Run metrics and tracing smoke under realistic task execution.

Also run the repository-wide regression suite, Go formatting/vet/lint/race tests, frontend formatting/lint/type-check/build/tests, generated contract drift, integration tests, secret scan, dependency/security audits, Docker image/Compose smoke, and existing E2E tests. A command that cannot run because of missing prerequisites is **BLOCKED**, not passed. Fix any introduced regression before creating a PR.

## Atomic commit map (suggested minimum)

1. `m15: event store and SSE resume`
2. `m15: cost ledger and analytics`
3. `m15: telemetry and alerts`
4. `m15: timeline/export UX`
5. `m15: SSE resilience + authorization tests`

Write each commit only after its contained work and relevant tests pass. Check `git log` for correct `yasserrmd` author and committer, permitted email, and zero AI/co-author trailers. No squash. Never commit directly to main.

## PR, merge, and completion evidence

1. Push `milestone/m15-realtime-timeline-auditability-cost-and-observability`; create a PR into `main` with scope, code evidence, linked acceptance items and test results.
2. Wait for **actual** required CI checks and any branch-protection reviews. Fix on the same milestone branch using additional atomic commits; do not fake or bypass required checks.
3. Once green and authorized, merge through GitHub; verify merged SHA on main, pull updated main, and delete local and remote milestone branches.
4. Create `docs/milestone-reports/M15.md` from the supplied template with PR URL, merge SHA, actual commits, checks, evidence and risk summary. Ensure the report itself is versioned in the PR **before merging**, not written only after the branch is deleted.
5. Update `docs/PHASE_STATUS.md` and continue automatically to the next file. If external authorization/credential is missing, record `BLOCKED` and precise remediation, work only on independent safe tasks, and never report unperformed PR/merge as done.

**Explicit handoff to next phase:** This milestone is complete only when the delivered capability can be used in the UI/API and its full positive/negative tests pass with verifiable artifacts.
