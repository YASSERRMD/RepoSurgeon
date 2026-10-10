# RepoSurgeon — M19: Release, Deployment, Full Journey Verification, and Handover

> Execution order: M18 → M19. Required branch: `milestone/m19-release-deployment-full-journey-verification-and-handover`. Execute one phase fully; do not claim completion until gates are actually green.
>
> Mandatory before starting: read `CLAUDE.md`, `RUN_FIRST.md`, `MASTER_IMPLEMENTATION_PLAN.md`, `ARCHITECTURE.md`, `GIT_WORKFLOW.md`, `SECURITY_GATES.md`, `API_CONTRACTS.md`, and all previous milestone completion reports. Follow the existing repository architecture where compatible; do not overwrite existing work.

## Objective and tangible outcome

Prove the complete issue-to-merge experience and ship reproducible deployable RepoSurgeon artifacts.

**Definition of done:** A verified user-visible vertical slice running against actual Go backend + PostgreSQL + Vue frontend, with real tests and artifacts. No TODO implementations, mock production APIs, static demo data, unexplained skipped tests, or artificial success labels.

## Detailed implementation tasks — complete every item

1. Create hardened multi-stage containers for API, worker, web and isolated executor service; non-root, minimal image, pinned dependencies and proper healthchecks.
2. Finalize Docker Compose development and documented production reference deployment with reverse proxy TLS, secret injection, controlled DB access and isolated runner pool.
3. Provide clean install, configuration validation, database migration, backup/restore and upgrade/rollback playbooks.
4. Execute golden journeys on disposable GitHub repositories: issue import → plan → branch → model tools → patch → tests → PR → CI → approval → merge → verify main → delete branch.
5. Execute failure journeys: missing key, broken CI, unauthorized merge, model timeout, worker restart, malicious README, test regression and GitHub 403; all must stop safely and truthfully.
6. Complete documentation: architecture and threat model, API reference, operations, data model, feature user guide, evaluation report and troubleshooting.
7. Run final independent reviewer audit comparing every milestone acceptance line to code/tests and logged outcomes; explicitly record unresolved external prerequisites.
8. Tag a release only after all mandatory gates pass and merged main is green; record image digests, SBOM, release SHA and rollback path.

## Required end-to-end interfaces

**Backend and OpenAPI:** Finalize and publish versioned OpenAPI contract; include version, readiness and safe diagnostics.

**Persistence and migration:** Schema compatibility matrix, release migration checksum and retention configuration verified.

**Frontend and interaction:** Final consistent app shell, onboard/setup wizard and real complete issue-to-PR demo path.

For every new endpoint define request/response/error examples and access controls in OpenAPI; regenerate typed Vue clients and include drift checks. Use real PostgreSQL transactions and never invent successful adapter responses.

## Negative cases and security constraints

- Never claim release or merged PR if external APIs/keys/permissions prevented validation.
- A live model account and authorized disposable GitHub repository are required for the final real external-path smoke.

## Mandatory verification scenarios

- [ ] Fresh-machine Compose installation and first-user setup works from documented commands.
- [ ] Full browser/API/DB/worker/runner/GitHub disposable integration journey succeeds.
- [ ] Negative scenario matrix returns blocked/failure with proper audit—not fake success.
- [ ] All CI, security, contract, generated client, Playwright, image and smoke checks green on main.

Also run the repository-wide regression suite, Go formatting/vet/lint/race tests, frontend formatting/lint/type-check/build/tests, generated contract drift, integration tests, secret scan, dependency/security audits, Docker image/Compose smoke, and existing E2E tests. A command that cannot run because of missing prerequisites is **BLOCKED**, not passed. Fix any introduced regression before creating a PR.

## Atomic commit map (suggested minimum)

1. `m19: release container hardening`
2. `m19: production operations/playbooks`
3. `m19: golden journeys and fault scenarios`
4. `m19: docs, artifacts and SBOM`
5. `m19: final gates and release record`

Write each commit only after its contained work and relevant tests pass. Check `git log` for correct `yasserrmd` author and committer, permitted email, and zero AI/co-author trailers. No squash. Never commit directly to main.

## PR, merge, and completion evidence

1. Push `milestone/m19-release-deployment-full-journey-verification-and-handover`; create a PR into `main` with scope, code evidence, linked acceptance items and test results.
2. Wait for **actual** required CI checks and any branch-protection reviews. Fix on the same milestone branch using additional atomic commits; do not fake or bypass required checks.
3. Once green and authorized, merge through GitHub; verify merged SHA on main, pull updated main, and delete local and remote milestone branches.
4. Create `docs/milestone-reports/M19.md` from the supplied template with PR URL, merge SHA, actual commits, checks, evidence and risk summary. Ensure the report itself is versioned in the PR **before merging**, not written only after the branch is deleted.
5. Update `docs/PHASE_STATUS.md` and continue automatically to the next file. If external authorization/credential is missing, record `BLOCKED` and precise remediation, work only on independent safe tasks, and never report unperformed PR/merge as done.

**Explicit handoff to next phase:** This milestone is complete only when the delivered capability can be used in the UI/API and its full positive/negative tests pass with verifiable artifacts.
