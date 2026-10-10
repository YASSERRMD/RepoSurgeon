# RepoSurgeon — M03: Step 5 Preview Model Gateway and Tool-Calling Protocol

> Execution order: M02 → M03. Required branch: `milestone/m03-step-5-preview-model-gateway-and-tool-calling-protocol`. Execute one phase fully; do not claim completion until gates are actually green.
>
> Mandatory before starting: read `CLAUDE.md`, `RUN_FIRST.md`, `MASTER_IMPLEMENTATION_PLAN.md`, `ARCHITECTURE.md`, `GIT_WORKFLOW.md`, `SECURITY_GATES.md`, `API_CONTRACTS.md`, and all previous milestone completion reports. Follow the existing repository architecture where compatible; do not overwrite existing work.

## Objective and tangible outcome

Establish a robust, measured, configurable StepFun client with real function-call loops and offline contract tests.

**Definition of done:** A verified user-visible vertical slice running against actual Go backend + PostgreSQL + Vue frontend, with real tests and artifacts. No TODO implementations, mock production APIs, static demo data, unexplained skipped tests, or artificial success labels.

## Detailed implementation tasks — complete every item

1. Implement `ModelProvider` interface and `StepFunProvider` HTTP client for `POST https://api.stepfun.ai/v1/chat/completions` with model ID `step-5-preview`; no hard-coded key in source.
2. Support chat messages, tool declarations, reasoning_effort low/medium/high, max_tokens, structured JSON mode, SSE streaming and cancellation.
3. Parse multi-part streaming tool calls by index/ID, assemble fragmented JSON arguments, validate against registered schemas and safely handle malformed responses.
4. Loop assistant tool calls through the trusted tool broker, append tool responses with matched `tool_call_id`, enforce maximum steps and budgets, and return normalized provider errors.
5. Capture provider request IDs where available, token usage (including cached_tokens when sent), model ID, effort, elapsed time, retries, estimated cost and redacted message metadata.
6. Implement retry with bounded exponential backoff for transient status/rate errors, Retry-After interpretation, deadline propagation and idempotent retry rules; never execute a tool twice on response retransmission.
7. Expose a model connectivity test with a cheap prompt and cost estimate; surface actual status in UI and securely hide secrets.
8. Provide a deterministic fake provider and recorded protocol fixtures for CI; optional live test uses a real user-supplied key and consumes usage budget.
9. Create a provider conformance suite so Step 3.7 Flash or other future providers can be compared without changing the agent or runner.

## Required end-to-end interfaces

**Backend and OpenAPI:** POST /api/v1/model-connections/{id}/test; GET /api/v1/models; POST /api/v1/model-sessions (sandboxed test only).

**Persistence and migration:** Model configuration records, usage ledger and provider request status; store only redacted message metadata by default.

**Frontend and interaction:** Model connection health, effort selector, usage/cost status and live response tester.

For every new endpoint define request/response/error examples and access controls in OpenAPI; regenerate typed Vue clients and include drift checks. Use real PostgreSQL transactions and never invent successful adapter responses.

## Negative cases and security constraints

- Step 5 does not have a built-in web_search tool; web search must be a separate app-provided tool if added.
- Do not treat a natural-language response as verified execution or a tool request as trusted permission.

## Mandatory verification scenarios

- [ ] Mock HTTP integration: success, tool calls, fragmented tool delta, schema errors, empty completion, timeout, 429, 500, cancel.
- [ ] Real live smoke only with configured StepFun key; record model/endpoint and outcome, never fake success.
- [ ] Verify repeated tool-call IDs cannot cause duplicate mutation; apply forced interruption tests.
- [ ] Browser model test screen displays actual data and no key leakage.

Also run the repository-wide regression suite, Go formatting/vet/lint/race tests, frontend formatting/lint/type-check/build/tests, generated contract drift, integration tests, secret scan, dependency/security audits, Docker image/Compose smoke, and existing E2E tests. A command that cannot run because of missing prerequisites is **BLOCKED**, not passed. Fix any introduced regression before creating a PR.

## Atomic commit map (suggested minimum)

1. `m03: provider interface/types and protocol fixtures`
2. `m03: StepFun chat/stream parser`
3. `m03: tool-call bridge and schema validation`
4. `m03: retries/usage and budget controls`
5. `m03: UI connector test and complete tests`

Write each commit only after its contained work and relevant tests pass. Check `git log` for correct `yasserrmd` author and committer, permitted email, and zero AI/co-author trailers. No squash. Never commit directly to main.

## PR, merge, and completion evidence

1. Push `milestone/m03-step-5-preview-model-gateway-and-tool-calling-protocol`; create a PR into `main` with scope, code evidence, linked acceptance items and test results.
2. Wait for **actual** required CI checks and any branch-protection reviews. Fix on the same milestone branch using additional atomic commits; do not fake or bypass required checks.
3. Once green and authorized, merge through GitHub; verify merged SHA on main, pull updated main, and delete local and remote milestone branches.
4. Create `docs/milestone-reports/M03.md` from the supplied template with PR URL, merge SHA, actual commits, checks, evidence and risk summary. Ensure the report itself is versioned in the PR **before merging**, not written only after the branch is deleted.
5. Update `docs/PHASE_STATUS.md` and continue automatically to the next file. If external authorization/credential is missing, record `BLOCKED` and precise remediation, work only on independent safe tasks, and never report unperformed PR/merge as done.

**Explicit handoff to next phase:** This milestone is complete only when the delivered capability can be used in the UI/API and its full positive/negative tests pass with verifiable artifacts.
