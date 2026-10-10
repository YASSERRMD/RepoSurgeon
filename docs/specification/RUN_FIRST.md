# Paste the following into Claude Code from the repository root

You are implementing the entire RepoSurgeon product, not drafting a roadmap. Treat the attached milestone documents and `CLAUDE.md` as the definitive engineering specification.

**Execute M00 through M19 consecutively, with actual functional implementation and testing. Do not pause for routine confirmation and do not stop after generating scaffolding or a proof of concept.**

Before coding:
1. Read `CLAUDE.md`, `MASTER_IMPLEMENTATION_PLAN.md`, `ARCHITECTURE.md`, `API_CONTRACTS.md`, `GIT_WORKFLOW.md`, `SECURITY_GATES.md`, `EVALUATION_SCENARIOS.md`, every milestone M00–M19, and the existing repository instructions.
2. Inspect the current Git repository, remote, branch protections, tool availability, CI, test commands and existing code. Preserve existing files and functionality unless a documented migration is required.
3. Check identity `yasserrmd <arafath.yasser@gmail.com>` for all new local Git author/committer fields. No coauthors, no Claude/AI/bot authorship metadata, no squash, no direct commits to main.
4. Create `docs/PHASE_STATUS.md` with a live durable execution ledger and ADRs. Determine exactly what already exists and carry it forward.
5. Do not use mock-only services as production paths. Include deterministic mocks only for protocol contract tests, while real integrations are implemented and smoke-tested when credentials are present.

Then for **each milestone file in order**:
1. Create `milestone/mXX-<slug>` branch from current verified main; no milestone branch reuse.
2. Decompose its tasks into atomic implement/test/refactor/doc commits; implement full Go + PostgreSQL + Vue functionality specified in that file.
3. Wire real integrations end-to-end and add automated positive, negative, concurrency and security tests. Keep every existing passing behavior intact.
4. Run Go fmt/vet/lint/tests with race checks; web lint/typecheck/test/build; migration/integration; OpenAPI drift; security scans; container image and Compose smoke; and Playwright browser E2E where applicable. Fix failures and rerun. Capture true logs/artifacts.
5. Create a completion report and update phase status on the branch **before** the PR. Open the PR, verify CI and reviews, fix until green, merge only when authorized, verify the resulting main SHA, delete the branch, and proceed immediately with next phase.
6. Integrate the previous milestone final merge SHA/report on the next branch since direct main writes are forbidden. On final milestone, store final PR closure evidence in a GitHub release/comment or other non-commit artifact.
7. If credentials, required external approvals or repository protection prevent a check, record `BLOCKED`, give the exact blocker and remediation. Continue independent safe work if available; never label the milestone complete or invent a PR/merge/green CI.

Architectural baseline:
- Backend: Go API + Go durable worker + independent tool-runner boundary.
- Frontend: Vue 3, TypeScript strict, Vite, accessible light theme in navy/gold/off-white.
- Persistence: PostgreSQL, versioned migrations, org-scoped access and append-only execution events.
- Default inference: StepFun `step-5-preview` at `https://api.stepfun.ai/v1` via model-agnostic provider contract, native function calls and SSE; no provider-side built-in web search assumption.
- Repo operations: GitHub App least privilege, pinned baseline SHA, isolated worktree and branch, safe patching and tests, reviewable PR and policy-gated merge.
- Evaluation: 12+ reproducible coded scenarios; Step 5 Preview vs Step 3.7 Flash; objective pass/fail; token/latency/cost recording.
- Security: explicit approval and authorization, no sensitive credentials inside repo workspaces, sandboxed command execution and deny-by-default network.

Your closing response is an evidence-based completion ledger. It must never say all milestones finished if any remained unimplemented, tests failed, an external account was not connected, or the GitHub PR/merge did not occur.

**Start now from M00. Continue through the complete specification as far as real tools and permissions allow.**
