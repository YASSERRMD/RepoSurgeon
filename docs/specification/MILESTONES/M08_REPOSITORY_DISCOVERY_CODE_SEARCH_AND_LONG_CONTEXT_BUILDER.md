# RepoSurgeon — M08: Repository Discovery, Code Search, and Long-Context Builder

> Execution order: M07 → M08. Required branch: `milestone/m08-repository-discovery-code-search-and-long-context-builder`. Execute one phase fully; do not claim completion until gates are actually green.
>
> Mandatory before starting: read `CLAUDE.md`, `RUN_FIRST.md`, `MASTER_IMPLEMENTATION_PLAN.md`, `ARCHITECTURE.md`, `GIT_WORKFLOW.md`, `SECURITY_GATES.md`, `API_CONTRACTS.md`, and all previous milestone completion reports. Follow the existing repository architecture where compatible; do not overwrite existing work.

## Objective and tangible outcome

Build precise evidence-backed code context rather than blindly sending an entire repository to the model.

**Definition of done:** A verified user-visible vertical slice running against actual Go backend + PostgreSQL + Vue frontend, with real tests and artifacts. No TODO implementations, mock production APIs, static demo data, unexplained skipped tests, or artificial success labels.

## Detailed implementation tasks — complete every item

1. Index path, language, symbols, imports, module/package boundaries, manifest dependencies and content hashes with incremental updates on git changes.
2. Implement deterministic lexical search, filename/path search, AST or language-server symbol lookups where practical, and bounded snippet reading with line numbers.
3. Construct task context from issue requirements, repository instructions (untrusted), relevant files, call sites, tests, module graph and recent error output.
4. Prioritize precision and coverage by retrieval strategy; implement configurable max-context budget below the Step 5 1M token limit and explicit truncation notices.
5. Keep stable system/instruction prefix separated from untrusted code/data for prompt caching; avoid injecting arbitrary repo README instructions into authority layer.
6. Record context provenance: file path, commit SHA, byte hash, line range, retrieval reason and token estimate; expose a traceable evidence view.
7. Support progressive context expansion triggered by observed failures or model requests, including file changes after context was built.
8. Benchmark needle-in-codebase tasks and near-duplicate files to quantify context accuracy at 32k/128k/256k/selected larger windows.

## Required end-to-end interfaces

**Backend and OpenAPI:** GET /api/v1/workspaces/{id}/search?q=; GET /api/v1/runs/{id}/context; POST /api/v1/runs/{id}/context/refresh.

**Persistence and migration:** File/symbol index, context manifests and retrieval provenance referenced by commit SHA.

**Frontend and interaction:** Code search and context inspector showing sources, lines, selection and truncation flags.

For every new endpoint define request/response/error examples and access controls in OpenAPI; regenerate typed Vue clients and include drift checks. Use real PostgreSQL transactions and never invent successful adapter responses.

## Negative cases and security constraints

- No full 1M context by default; token size and costs must be observable.
- Private source content goes to provider only after explicit project credential and data-transfer authorization.

## Mandatory verification scenarios

- [ ] Search returns correct files and line ranges in Go/Vue/Python fixtures.
- [ ] Incremental index updates after changes and deletes.
- [ ] Repo README with hostile instruction cannot supersede project policies.
- [ ] Budget tests show deterministic omission/truncation and context trace.

Also run the repository-wide regression suite, Go formatting/vet/lint/race tests, frontend formatting/lint/type-check/build/tests, generated contract drift, integration tests, secret scan, dependency/security audits, Docker image/Compose smoke, and existing E2E tests. A command that cannot run because of missing prerequisites is **BLOCKED**, not passed. Fix any introduced regression before creating a PR.

## Atomic commit map (suggested minimum)

1. `m08: indexer and hash updates`
2. `m08: search/symbol adapters`
3. `m08: context builder + provenance`
4. `m08: context UI`
5. `m08: retrieval benchmarks / injection tests`

Write each commit only after its contained work and relevant tests pass. Check `git log` for correct `yasserrmd` author and committer, permitted email, and zero AI/co-author trailers. No squash. Never commit directly to main.

## PR, merge, and completion evidence

1. Push `milestone/m08-repository-discovery-code-search-and-long-context-builder`; create a PR into `main` with scope, code evidence, linked acceptance items and test results.
2. Wait for **actual** required CI checks and any branch-protection reviews. Fix on the same milestone branch using additional atomic commits; do not fake or bypass required checks.
3. Once green and authorized, merge through GitHub; verify merged SHA on main, pull updated main, and delete local and remote milestone branches.
4. Create `docs/milestone-reports/M08.md` from the supplied template with PR URL, merge SHA, actual commits, checks, evidence and risk summary. Ensure the report itself is versioned in the PR **before merging**, not written only after the branch is deleted.
5. Update `docs/PHASE_STATUS.md` and continue automatically to the next file. If external authorization/credential is missing, record `BLOCKED` and precise remediation, work only on independent safe tasks, and never report unperformed PR/merge as done.

**Explicit handoff to next phase:** This milestone is complete only when the delivered capability can be used in the UI/API and its full positive/negative tests pass with verifiable artifacts.
