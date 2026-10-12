# RepoSurgeon — Durable Phase Execution Ledger

> This file is the authoritative project log required by `CLAUDE.md` §1. Update it before
> every PR and after every verified merge. Recover from interruption by reading this file plus
> `git log` on `main`; never restart the plan from scratch.

**Product:** RepoSurgeon — autonomous software engineering workspace
(Go modular monolith `api`/`worker` + isolated `runner`, PostgreSQL durable workflows,
Vue 3 + TypeScript strict web workbench, StepFun `step-5-preview` as default inference engine
behind a model-agnostic provider interface).

**Specification of record:** `docs/specification/` (copied from the planning package):
`CLAUDE.md`, `RUN_FIRST.md`, `MASTER_IMPLEMENTATION_PLAN.md`, `ARCHITECTURE.md`,
`API_CONTRACTS.md`, `GIT_WORKFLOW.md`, `SECURITY_GATES.md`, `EVALUATION_SCENARIOS.md`,
`MILESTONES/M00`–`M19`, `TEMPLATES/`.

**Local Git identity (enforced):** author/committer `yasserrmd <arafath.yasser@gmail.com>`,
no GPG signing, no AI/co-author trailers, no direct commits to `main`, no squash.

## Progress

| Phase | Branch | Status | PR | Merge SHA | Evidence |
|---|---|---|---|---|---|
| M00 — Repository foundation & engineering governance | merged (branch deleted) | PR https://github.com/YASSERRMD/RepoSurgeon/pull/1 | `d69e78b2dea17a9744c842e0419c766e826d7747` | `docs/milestone-reports/M00.md` |
| M01 — Domain model, persistence, project isolation | merged, branch deleted | PR https://github.com/YASSERRMD/RepoSurgeon/pull/2 | recorded in M02 report | `docs/milestone-reports/M01.md` |
| M02 — Authentication, authorization, secure credentials | merged, branch deleted | PR #3 | `7978b22d7bc214f4c6478fb066e9b4a1ad7672b0` | `docs/milestone-reports/M02.md` |
| M03 — Step 5 Preview model gateway and tool calling | merged, branch deleted | PR #4 | `fa289c869c48a56065cdd9a697d6809e71b11519` | `docs/milestone-reports/M03.md` |
| M04 — GitHub integration, repo registration, issue sync | merged, branch deleted | PR #5 | recorded in M05 report | `docs/milestone-reports/M04.md` |
| M05 — Safe checkout, branching, worktree management | merged, branch deleted | PR #6 | `3112919a5dd093b104b26e0096941ec3ab3756b3` | `docs/milestone-reports/M05.md` |
| M06 — Durable run state machine and worker scheduling | merged, branch deleted | PR #7 | `5211c937e2466ed4632ffdb59ad3f6bfa99a6197` | `docs/milestone-reports/M06.md` |
| M07 — Isolated tool execution and human approval gateway | merged, branch deleted | PR #8 | `994a1b91d2f314c7c24e67ab7185ca033f3be3df` | `docs/milestone-reports/M07.md` |
| M08 — Repository discovery, code search, long-context builder | merged, branch deleted | PR #9 | `0d2dca781200309523fde1e81c0db685835f5af4` | `docs/milestone-reports/M08.md` |
| M09 — Issue investigation, plans, acceptance criteria | merged, branch deleted | PR #10 | `76592d4867c299ba842f3780139e03712bce6fe1` | `docs/milestone-reports/M09.md` |
| M10 — Atomic code change and patch engine | merged, branch deleted | PR #11 | recorded in M11 report | `docs/milestone-reports/M10.md` |
| M11 — Test discovery, execution and evidence collection | merged, branch deleted | PR #12 | 9fd9e6b | `docs/milestone-reports/M11.md` |
| M12–M19 | — | not started | — | — | — |

## Log

### 2026-10-10 — M01 pushed; stale M00 merge detected

- PR #1 merged an earlier branch ref, leaving three M00 fixes off `main`
  (golangci-lint pin, runner network, smoke wait). PR #2 re-applies them so
  main returns to green; this is disclosed here rather than silently fixed.

### 2026-10-10 — M01 merged

- PR #2 merged into `main`; all CI green on the final head.
- Local evidence: `RS_TEST_DATABASE_URL` race suite across 9 packages, 92 Vue unit tests,
  8 Playwright browser tests, HTTP smoke against local PostgreSQL 16 covering tenant
  isolation, optimistic concurrency conflicts, idempotent replay and the audit trail.
- Known interim risk carried into M02: the development subject resolver (documented,
  production-disabled).

### 2026-10-10 — M00 merged

- PR #1 merged into `main` with merge commit `d69e78b2dea17a9744c842e0419c766e826d7747`
  (merge mode preserved all 80 atomic source commits; the merge commit itself is
  GitHub-managed metadata, disclosed per `GIT_WORKFLOW.md` §5).
- CI on final head `226aee9`: go (fmt/vet/lint/race) success; web (lint/format/
  types/tests/build/drift) success; security (gitleaks/gosec/govulncheck/npm
  audit/actionlint) success; commit-metadata success; compose-smoke + Playwright
  E2E success.
- Local verification evidence for M00 is recorded in `docs/milestone-reports/M00.md`.
- M01 branch created from merged main.

### 2026-10-10 — M00 started

- Repository inspected: clean `main` at `9a1aecc` (LICENSE only), remote
  `https://github.com/YASSERRMD/RepoSurgeon.git`, `gh` authenticated as `YASSERRMD`,
  `main` currently unprotected (recorded fact; branch protection still respected by policy —
  every change lands via PR).
- Planning package copied to `docs/specification/`; `CLAUDE.md` placed at repository root.
- Local Git identity configured per `GIT_WORKFLOW.md` §1; `core.hooksPath=.githooks`.
- Toolchain verified locally: go1.25.5, node v24.11.1, docker 29.0.1, compose v2.40.3,
  postgres 16.14 (psql client), gh 2.83.2, golangci-lint present.
- M00 branch created from verified `main`.

## Blockers

_None yet. Record exact blocker + remediation here whenever a gate is `BLOCKED`._
