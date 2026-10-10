# RepoSurgeon — M11: Test Discovery, Execution, and Evidence Collection

> Execution order: M10 → M11. Required branch: `milestone/m11-test-discovery-execution-and-evidence-collection`. Execute one phase fully; do not claim completion until gates are actually green.
>
> Mandatory before starting: read `CLAUDE.md`, `RUN_FIRST.md`, `MASTER_IMPLEMENTATION_PLAN.md`, `ARCHITECTURE.md`, `GIT_WORKFLOW.md`, `SECURITY_GATES.md`, `API_CONTRACTS.md`, and all previous milestone completion reports. Follow the existing repository architecture where compatible; do not overwrite existing work.

## Objective and tangible outcome

Verify actual code changes using build, unit, integration and project-specific checks, with readable evidence.

**Definition of done:** A verified user-visible vertical slice running against actual Go backend + PostgreSQL + Vue frontend, with real tests and artifacts. No TODO implementations, mock production APIs, static demo data, unexplained skipped tests, or artificial success labels.

## Detailed implementation tasks — complete every item

1. Discover checked-in test configuration and lockfiles; define checked command profiles for Go, Node/Vue, Python and mixed repos.
2. Execute commands only through restricted runner with known workdir, timeout, CPU/memory limits and separated environment.
3. Capture machine-readable results for test cases, build failures, lint/typecheck warnings, coverage where available and process exit codes.
4. Parse stdout/stderr into failure groups retaining raw bounded logs, failing path/line and artifact checksums.
5. Compare before/after baseline outcomes where possible; prevent the model from claiming regression is pre-existing without evidence.
6. Support multi-stage test execution (targeted → module → full suite → browser smoke when applicable), with always-on security checks where required.
7. Implement test matrix viewer, success/failure logs, downloadable artifacts and failing test filtering.
8. Define deterministic verdict rules; green means tool exit codes and required checks pass, never merely model saying green.

## Required end-to-end interfaces

**Backend and OpenAPI:** POST /api/v1/runs/{id}/checks; GET /api/v1/runs/{id}/checks; GET /api/v1/runs/{id}/checks/{check_id}/logs.

**Persistence and migration:** Check plans, attempts, process exits, parsed diagnostics and artifact pointers.

**Frontend and interaction:** Real test matrix with status, logs, duration and targeted/final run distinction.

For every new endpoint define request/response/error examples and access controls in OpenAPI; regenerate typed Vue clients and include drift checks. Use real PostgreSQL transactions and never invent successful adapter responses.

## Negative cases and security constraints

- Do not install dependencies through unrestricted network as a side effect of running tests.
- CI success is separate from local success and will be enforced at PR lifecycle.

## Mandatory verification scenarios

- [ ] Run actual Go and Vue fixture test suites, including a seeded failing test.
- [ ] Kill timed-out build; assert sandbox process cannot outlive lease.
- [ ] Fail verification if a required test did not run or the command was absent.
- [ ] Compare parsed failure counts with original process output; no fabricated passes.

Also run the repository-wide regression suite, Go formatting/vet/lint/race tests, frontend formatting/lint/type-check/build/tests, generated contract drift, integration tests, secret scan, dependency/security audits, Docker image/Compose smoke, and existing E2E tests. A command that cannot run because of missing prerequisites is **BLOCKED**, not passed. Fix any introduced regression before creating a PR.

## Atomic commit map (suggested minimum)

1. `m11: test profile discovery`
2. `m11: sandbox execution and structured results`
3. `m11: test evidence parser`
4. `m11: browser test matrix`
5. `m11: failure and timeout tests`

Write each commit only after its contained work and relevant tests pass. Check `git log` for correct `yasserrmd` author and committer, permitted email, and zero AI/co-author trailers. No squash. Never commit directly to main.

## PR, merge, and completion evidence

1. Push `milestone/m11-test-discovery-execution-and-evidence-collection`; create a PR into `main` with scope, code evidence, linked acceptance items and test results.
2. Wait for **actual** required CI checks and any branch-protection reviews. Fix on the same milestone branch using additional atomic commits; do not fake or bypass required checks.
3. Once green and authorized, merge through GitHub; verify merged SHA on main, pull updated main, and delete local and remote milestone branches.
4. Create `docs/milestone-reports/M11.md` from the supplied template with PR URL, merge SHA, actual commits, checks, evidence and risk summary. Ensure the report itself is versioned in the PR **before merging**, not written only after the branch is deleted.
5. Update `docs/PHASE_STATUS.md` and continue automatically to the next file. If external authorization/credential is missing, record `BLOCKED` and precise remediation, work only on independent safe tasks, and never report unperformed PR/merge as done.

**Explicit handoff to next phase:** This milestone is complete only when the delivered capability can be used in the UI/API and its full positive/negative tests pass with verifiable artifacts.
