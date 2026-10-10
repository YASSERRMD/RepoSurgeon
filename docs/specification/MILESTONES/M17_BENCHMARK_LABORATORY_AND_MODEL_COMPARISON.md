# RepoSurgeon — M17: Benchmark Laboratory and Model Comparison

> Execution order: M16 → M17. Required branch: `milestone/m17-benchmark-laboratory-and-model-comparison`. Execute one phase fully; do not claim completion until gates are actually green.
>
> Mandatory before starting: read `CLAUDE.md`, `RUN_FIRST.md`, `MASTER_IMPLEMENTATION_PLAN.md`, `ARCHITECTURE.md`, `GIT_WORKFLOW.md`, `SECURITY_GATES.md`, `API_CONTRACTS.md`, and all previous milestone completion reports. Follow the existing repository architecture where compatible; do not overwrite existing work.

## Objective and tangible outcome

Measure Step 5 Preview objectively against baselines through repeatable software engineering tasks.

**Definition of done:** A verified user-visible vertical slice running against actual Go backend + PostgreSQL + Vue frontend, with real tests and artifacts. No TODO implementations, mock production APIs, static demo data, unexplained skipped tests, or artificial success labels.

## Detailed implementation tasks — complete every item

1. Build evaluation harness where each task runs in a fresh pinned repo SHA, same environment, same tools, same maximum budgets and defined hidden tests.
2. Provide provider profiles for Step 5 Preview and Step 3.7 Flash using the same normalized tool contract; deterministic fake provider for CI.
3. Create at least 12 seeded evaluations spanning Go bugfix, Vue component bug, cross-layer migration, regression repair, flaky test, monorepo change, API contract drift, security mistake, conflicting issue, long-context search, UI screenshot fix and malicious README injection.
4. Grade pass/fail using code/test outcomes, not LLM judgment; record per-task partial credit only with explicit rubric.
5. Record success@1, repeat-run success rate, total steps, valid tool call %, regression count, time, estimated USD, input/output/cached tokens and recovery rate.
6. Support one-click rerun, side-by-side traces and exporting CSV/JSON/Markdown report with environment/model configuration.
7. Add confidence intervals and repeated trials where budget permits; keep test corpora versioned and independent of agent prompt data.
8. Enable comparative configurations for context budget/reasoning effort without silently changing other variables.

## Required end-to-end interfaces

**Backend and OpenAPI:** POST /api/v1/evaluations; GET /api/v1/evaluations; GET /api/v1/evaluations/{id}/results; POST /api/v1/evaluations/{id}/rerun.

**Persistence and migration:** Dataset/version, run config, baselines, individual scored checks, aggregated stats and report artifacts.

**Frontend and interaction:** Real comparison table/charts with sortable scenario outcomes and links to execution evidence.

For every new endpoint define request/response/error examples and access controls in OpenAPI; regenerate typed Vue clients and include drift checks. Use real PostgreSQL transactions and never invent successful adapter responses.

## Negative cases and security constraints

- Do not claim universal model superiority from one small experiment.
- No benchmark answer leakage to model; hidden tests remain outside task workspace.

## Mandatory verification scenarios

- [ ] All 12 seeded scenarios run in CI with fake provider protocol and expected scoring.
- [ ] Live baseline experiment requires user keys; mark result absent rather than pretend.
- [ ] Reproducible scenario reset prevents evaluation pollution between runs.
- [ ] Change effort/context only and verify experiment config captures differences.

Also run the repository-wide regression suite, Go formatting/vet/lint/race tests, frontend formatting/lint/type-check/build/tests, generated contract drift, integration tests, secret scan, dependency/security audits, Docker image/Compose smoke, and existing E2E tests. A command that cannot run because of missing prerequisites is **BLOCKED**, not passed. Fix any introduced regression before creating a PR.

## Atomic commit map (suggested minimum)

1. `m17: dataset and reproducible runner`
2. `m17: scoring and metrics`
3. `m17: baseline provider profiles`
4. `m17: comparison UI/export`
5. `m17: replay and integrity tests`

Write each commit only after its contained work and relevant tests pass. Check `git log` for correct `yasserrmd` author and committer, permitted email, and zero AI/co-author trailers. No squash. Never commit directly to main.

## PR, merge, and completion evidence

1. Push `milestone/m17-benchmark-laboratory-and-model-comparison`; create a PR into `main` with scope, code evidence, linked acceptance items and test results.
2. Wait for **actual** required CI checks and any branch-protection reviews. Fix on the same milestone branch using additional atomic commits; do not fake or bypass required checks.
3. Once green and authorized, merge through GitHub; verify merged SHA on main, pull updated main, and delete local and remote milestone branches.
4. Create `docs/milestone-reports/M17.md` from the supplied template with PR URL, merge SHA, actual commits, checks, evidence and risk summary. Ensure the report itself is versioned in the PR **before merging**, not written only after the branch is deleted.
5. Update `docs/PHASE_STATUS.md` and continue automatically to the next file. If external authorization/credential is missing, record `BLOCKED` and precise remediation, work only on independent safe tasks, and never report unperformed PR/merge as done.

**Explicit handoff to next phase:** This milestone is complete only when the delivered capability can be used in the UI/API and its full positive/negative tests pass with verifiable artifacts.
