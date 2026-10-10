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
| M00 — Repository foundation & engineering governance | `milestone/m00-repository-foundation-and-engineering-governance` | in progress | — | — | `docs/milestone-reports/M00.md` |
| M01 — Domain model, persistence, project isolation | — | not started | — | — | — |
| M02 — Authentication, authorization, secure credentials | — | not started | — | — | — |
| M03 | — | not started | — | — | — |
| … M19 | — | not started | — | — | — |

## Log

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
