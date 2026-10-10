# RepoSurgeon — Master Product and Phase-by-Phase Implementation Plan

## 1. Product vision

RepoSurgeon is an autonomous software engineering workspace. A developer imports a real GitHub issue into a connected project; Step 5 Preview investigates authorized source code, constructs a grounded plan, produces a controlled patch using structured tools, runs reproducible tests and fixes regressions, then prepares a PR. CI and branch protection remain authoritative. Authorized humans can approve merge; RepoSurgeon verifies merge completion and branch cleanup. Every important action is auditable and measurable.

**Not:** a chat wrapper, a prompt collection, a static demo, a replacement for CI, or an agent free to run host commands.

## 2. Success flows

**Happy path:** connect GitHub → choose repo → import issue → create run → pin branch/base SHA → analyze real files → plan with cited evidence → select approved tools → edit files → execute tests → recover failed tests → commit atomic changes → push branch → open real PR → observe CI → authorized review/merge → verify main SHA → remove merged branch → archive evidence.

**Failure path:** invalid key, repo unauthorized, token expired, tool argument malformed, prompt injection, stalled model, sandbox timeout, failed test, patch conflict, reviewer rejects PR, CI fails, protected branch cannot merge. RepoSurgeon must show failed/blocked with preserved artifacts; it must not invent a successful result.

**Model comparison path:** choose a fixed evaluation dataset and two providers; run fresh isolated repos with the same tools, budget and limits; record actual tests, success, latency, token usage, cost and recovery. Publish evidence rather than rankings unsupported by data.

## 3. Technical stack and choices

| Layer | Design decision |
|---|---|
| Web | Vue 3 + TypeScript strict + Vite + Pinia/TanStack Query as needed; Playwright E2E |
| API | Go modular monolith, explicit HTTP routes and OpenAPI; typed adapters and middleware |
| Background work | Go worker with PostgreSQL durable queue (`FOR UPDATE SKIP LOCKED`), leases and heartbeats |
| Database | PostgreSQL, numbered migrations, pgx, tenant-scoped repositories |
| Agent model | StepFun Step 5 Preview, chat completions, tool calls, SSE, structured JSON outputs, reasoning effort |
| Provider isolation | `ModelProvider` interface, fake protocol conformance provider for CI, Step 3.7 Flash for baseline |
| Code tools | Explicit tool broker, worktree operations, safe structured patches, bounded test runner |
| Execution trust boundary | Dedicated non-root, isolated runner with no host Docker socket or secrets; deny network by default |
| GitHub | Least-privilege GitHub App; signed webhooks; PR/CI/merge policy adapter |
| Telemetry | Append-only run event feed, SSE resume, structured redacted logs, OpenTelemetry |
| Delivery | Docker Compose for local; production deployment reference with separated runner resources |

Do not bolt on heavyweight platforms before they are justified. Favor clear Go services, PostgreSQL and established libraries. Keep the model independent and swappable.

## 4. Product capabilities

- Organization and project isolation; users, roles, memberships and secure credential lifecycle.
- GitHub repository discovery and issue sync, protected-branch detection and webhooks.
- Worktree pinning, real file indexing, symbol/path search and evidence-linked context building.
- Persisted agent state machine with queue leases, restart recovery, budgets, pause/cancel/retry.
- Model gateway with tool-call JSON assembly, structured output, `reasoning_effort`, cached token usage, streaming and model request metadata.
- Tool permissions, approvals, sandbox execution, and reproducible commands / test evidence.
- Typed plans, patch engine, checkpoints, test discovery, iterative fault remediation.
- PR creation, CI tracking, protected branch policies, verified merge and branch cleanup.
- Responsive rich UI for projects, issues, plans, patches, live execution, approvals, tests and run history.
- Screenshot-based visual regression workflow using Step 5 vision input.
- Evaluation lab with objective reusable scenarios and model comparisons.
- Security hardening, backup/restore, observability, deployment documentation, and actual golden E2E.

## 5. Milestone sequence and complete release gates

| Milestone | Capability shipped | Primary real proof |
|---|---|---|
| M00 | Repository Foundation and Engineering Governance | Run Go format, vet, lint, unit tests and `go test -race ./...`. |
| M01 | Domain Model, Persistence, and Project Isolation | Migrate new and upgrade existing database; reverse migration in disposable database. |
| M02 | Authentication, Authorization, and Secure Credentials | Integration tests for positive/negative role matrix across all protected routes. |
| M03 | Step 5 Preview Model Gateway and Tool-Calling Protocol | Mock HTTP integration: success, tool calls, fragmented tool delta, schema errors, empty completion, timeout, 429, 500, cancel. |
| M04 | GitHub Integration, Repository Registration, and Issue Sync | End-to-end mock installation, repo selection, issue import and webhook update. |
| M05 | Safe Repository Checkout, Branching, and Worktree Management | Clone from fixture Git repository, branch, edit and clean up; assert source default branch unchanged. |
| M06 | Durable Agent Run State Machine and Worker Scheduling | Worker restart recovers queued, running and interrupted tasks with no duplicate operations. |
| M07 | Isolated Tool Execution and Human Approval Gateway | Execute approved checks inside isolated runner on a fixture repo. |
| M08 | Repository Discovery, Code Search, and Long-Context Builder | Search returns correct files and line ranges in Go/Vue/Python fixtures. |
| M09 | Issue Investigation, Plans, and Acceptance Criteria | Seed known buggy repositories and verify plan includes file paths and reproduction tests. |
| M10 | Atomic Code Change and Patch Engine | Apply multi-hunk patches and changes across multiple file types. |
| M11 | Test Discovery, Execution, and Evidence Collection | Run actual Go and Vue fixture test suites, including a seeded failing test. |
| M12 | Iterative Debugging, Recovery, and State Reconciliation | Seed first-edit failure then verify second fix is correct and full suite passes. |
| M13 | Git Commits, Pull Requests, CI Review, and Safe Merge | Use disposable GitHub fixture repo for real smoke or a faithful GitHub fake for CI contract suite. |
| M14 | Complete Web Workbench and Usable Developer Experience | Playwright from login to import issue to inspect run/check/PR on fixture backend. |
| M15 | Realtime Timeline, Auditability, Cost, and Observability | Drop/reconnect SSE and verify sequence continuity, replay and no lost events. |
| M16 | Multimodal Screenshot Understanding and Frontend Verification | Capture and inspect real generated screenshots across viewport sizes. |
| M17 | Benchmark Laboratory and Model Comparison | All 12 seeded scenarios run in CI with fake provider protocol and expected scoring. |
| M18 | Security Red Team, Compliance, and Reliability Hardening | All critical/high exploitable findings remediated; rerun attacks and scans. |
| M19 | Release, Deployment, Full Journey Verification, and Handover | Fresh-machine Compose installation and first-user setup works from documented commands. |

**Progression policy:** Complete M00 before M01, and each subsequent milestone after its predecessor is demonstrably ready. Each milestone must deliver a working vertical slice and preserve earlier capabilities. For all M00–M19 run the same repository-wide regression/security baseline. Do not substitute documentation-only work for working features.

## 6. Domain and lifecycle

Organizations own repositories and projects. A project may have several synchronized issues; an issue may spawn multiple task runs, each pinned to a specific Git SHA and explicit user/model/budget/policy. Each run contains plan revisions, steps, tool calls, patches, checks, approvals, PR linkage, model usage and append-only audit events. Tool execution outcomes are authoritative facts and are never inferred from natural-language assertions.

State transitions are legal only through a Go state machine. A run that is interrupted resumes from durable checkpoints and does not re-perform an external mutation without checking its effect receipt. A run waiting for merge approval is explicitly blocked/waiting rather than silently merging.

## 7. Agent orchestration algorithm

1. **Intake:** authenticate caller and repo; import issue; pin repo branch and base SHA; initialize budget.
2. **Observe:** collect project policy, repository graph, relevant code, tests and issue evidence. Mark all external repo text as untrusted.
3. **Plan:** ask Step 5 for typed investigation/plan; validate schema, sources, file existence and action authority.
4. **Authorize:** deterministic policy engine validates proposed tool/command/file scope; route sensitive operations to approval.
5. **Execute:** run allowable file/search/patch/test tools in isolated worktree. Persist input/output/result before proceeding.
6. **Verify:** use deterministic test/check processes; gather exit codes and diagnostics; if fail return to observe/repair within bounded budget.
7. **Prepare PR:** verify diff and all local gates, build atomic source commits, push a task branch, create PR once.
8. **CI/merge:** monitor real required checks; obtain approved merge permission; verify GitHub state and main SHA; delete merged branch.
9. **Evaluate:** write outcome metrics, measured token/cost/steps, failure reasons, full redacted audit and trace export.

**Important:** Step 5 is the reasoning engine; the application implements file access, code execution, GitHub access and policy checks. Model function-call arguments are untrusted proposals, never authorization tokens.

## 8. Deployment and permissions assumptions

To fully run live integrations, the environment requires: an accessible PostgreSQL instance; a StepFun account with an API key and funds/limits; a GitHub App or authorized GitHub credentials with correct repository access; a disposable test GitHub repository for safe final E2E; a supported container runtime and Git; and a configured CI environment. Do not commit these secrets. The user does not need to paste them into an AI prompt—runtime secret stores and local environment are appropriate.

The final system must allow **read-only exploration** without automatically enabling write/merge permissions. Never enable automatic merge into third-party/production repositories by default; explicit policy+review is required.

## 9. Release acceptance contract

**Correctness:** every endpoint and UI action works against live backend/database; whole workflow passes in an authorized disposable repo; tests demonstrably fail when bug injected; migration upgrades validated; cache/input accounting reliable.

**Security:** organization isolation, role checks, secret non-disclosure, webhook HMAC, safe path resolution, injected instructions cannot grant authority, runner cannot escape/read host secrets, no direct main writes.

**Engineering:** atomic Git history with `yasserrmd` local source author/committer, no AI co-author credits, PR and green CI per milestone, no squash, verified merges and deletion, no TODO/stub production behavior.

**Observability:** accurate event timeline and trace, exact status, actionable blocked reasons, model/tokens/cost, test evidence, true GitHub links.

**Experimentation:** at least 12 pinned tasks, Step 5 vs Step 3.7 with equal tool/budget conditions, no invented benchmark outcomes or synthetic 'live' scores.

## 10. Definition of verified done

For each milestone, preserve actual command outputs, database migration checks, Playwright screens, PR URL/check status/merge SHA, new source commit IDs and checklist evidence in `docs/milestone-reports/`. Unavailable external verification is recorded explicitly as `BLOCKED`, not silently skipped.

## 11. Source of authority

The verified StepFun model API details are in `SOURCE_REFERENCES.md`. See each milestone document for its precise implementation tasks, API surface, storage, UI, negative cases, tests and branch procedure. The rules in `CLAUDE.md` apply to **every** milestone, even when not repeated in a task bullet.
