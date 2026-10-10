# RepoSurgeon — Step 5 Preview autonomous coding agent

**Planning package — not yet an implemented application.** This repository-ready package directs Claude Code to build and verify a real, usable autonomous software engineering product.

## Start here

1. Place the entire `RepoSurgeon_Implementation_Package` contents in the target repository (or a reference `implementation/` directory and copy `CLAUDE.md` to repository root).
2. Open Claude Code in the repository root and provide the exact prompt in `RUN_FIRST.md`.
3. Configure a StepFun API key only via runtime secret management; never paste credentials into Claude prompts, repository content or Git commits.
4. Supply authorized GitHub access to the actual target repository and a disposable GitHub repository for final E2E. No PR/merge claims are allowed unless verified externally.
5. Execute milestone files `M00` to `M19` in order. Treat each as a completed vertical slice and require PR, green CI, merge and branch deletion.

## Documents

- `CLAUDE.md` — persistent non-negotiable coding-agent instructions
- `RUN_FIRST.md` — one-shot instruction to Claude Code to implement everything
- `MASTER_IMPLEMENTATION_PLAN.md` — complete product specification and sequencing
- `ARCHITECTURE.md` — trust-boundary design, components, persistent entities, data flow
- `API_CONTRACTS.md` — API/agent/tool contracts and example payloads
- `GIT_WORKFLOW.md` — author identity, branches, commits, PR and merge lifecycle
- `SECURITY_GATES.md` — threat model and measurable security release gates
- `EVALUATION_SCENARIOS.md` — objective evaluation scenarios for Step 5 Preview
- `MILESTONES/M00_*` through `MILESTONES/M19_*` — 20 detailed phase instructions
- `TEMPLATES/PHASE_COMPLETION_REPORT.md` and `TEMPLATES/DECISION_RECORD.md`
- `SOURCE_REFERENCES.md` — primary technical references verified on 2026-10-10

## Product boundary

Claude Code is the **implementation orchestrator** of RepoSurgeon. RepoSurgeon is the **resulting application** and must use Step 5 Preview through the real provider API. Do not accidentally substitute Claude as RepoSurgeon's product inference engine.

The product must only access repositories, tools and GitHub APIs that are explicitly authorized. Software development task automation is not permission to bypass code reviews, branch protection, human approvals or credential safety.
