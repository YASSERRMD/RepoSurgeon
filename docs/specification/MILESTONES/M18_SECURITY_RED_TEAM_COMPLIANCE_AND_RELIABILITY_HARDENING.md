# RepoSurgeon — M18: Security Red Team, Compliance, and Reliability Hardening

> Execution order: M17 → M18. Required branch: `milestone/m18-security-red-team-compliance-and-reliability-hardening`. Execute one phase fully; do not claim completion until gates are actually green.
>
> Mandatory before starting: read `CLAUDE.md`, `RUN_FIRST.md`, `MASTER_IMPLEMENTATION_PLAN.md`, `ARCHITECTURE.md`, `GIT_WORKFLOW.md`, `SECURITY_GATES.md`, `API_CONTRACTS.md`, and all previous milestone completion reports. Follow the existing repository architecture where compatible; do not overwrite existing work.

## Objective and tangible outcome

Systematically attack repo-to-agent trust boundaries and resolve critical weaknesses before release.

**Definition of done:** A verified user-visible vertical slice running against actual Go backend + PostgreSQL + Vue frontend, with real tests and artifacts. No TODO implementations, mock production APIs, static demo data, unexplained skipped tests, or artificial success labels.

## Detailed implementation tasks — complete every item

1. Develop and execute security threat matrix for prompt-injected issues/README, forged tool results, cross-tenant IDOR, poisoned tests, malicious build scripts, symlink traversal, credential theft, SSRF and dependency supply-chain compromise.
2. Implement policy-as-code guards for action authority, network egress, dangerous files, shell execution, approvals, cost/rate limits and emergency shutdown.
3. Run SAST, SCA/dependency audits, secret scan, container vulnerability scan and GitHub Actions workflow permissions audit; severity policy must block release when violated.
4. Test PostgreSQL backup/restore, point-in-time strategy documentation, migration rollback and restoration of selected run evidence.
5. Load-test task queue, worker lease storms, SSE fanout and search/large diff endpoints; remove unbounded memory usage.
6. Instrument rate limiting, quota enforcement, per-organization job isolation and log retention.
7. Validate signed webhook replay safety, credential rotation, webhook disable/uninstall propagation and branch-protection adherence.
8. Create threat evidence report with exploit reproduction, patches, fixed tests and residual risk.

## Required end-to-end interfaces

**Backend and OpenAPI:** Admin-only health/security/usage endpoints; prohibit a public emergency-override endpoint.

**Persistence and migration:** Security finding records, retention jobs, quota/restriction settings and immutable audit proof.

**Frontend and interaction:** Safe security settings, quotas, credential health and administrator audit exports.

For every new endpoint define request/response/error examples and access controls in OpenAPI; regenerate typed Vue clients and include drift checks. Use real PostgreSQL transactions and never invent successful adapter responses.

## Negative cases and security constraints

- Do not implement unreviewed arbitrary host command execution.
- No bypass of sandbox, security, GitHub permissions or approval rules to keep automation moving.

## Mandatory verification scenarios

- [ ] All critical/high exploitable findings remediated; rerun attacks and scans.
- [ ] Cross-tenant, sandbox breakout and prompt-injection regression suite passes.
- [ ] Back up and restore a fixture DB and replay job state correctly.
- [ ] Load-test thresholds documented and met in target environment.

Also run the repository-wide regression suite, Go formatting/vet/lint/race tests, frontend formatting/lint/type-check/build/tests, generated contract drift, integration tests, secret scan, dependency/security audits, Docker image/Compose smoke, and existing E2E tests. A command that cannot run because of missing prerequisites is **BLOCKED**, not passed. Fix any introduced regression before creating a PR.

## Atomic commit map (suggested minimum)

1. `m18: threat cases and adversarial suite`
2. `m18: security fixes and policy tests`
3. `m18: dependency/container audits`
4. `m18: resilience/backup load tests`
5. `m18: security evidence and CI gates`

Write each commit only after its contained work and relevant tests pass. Check `git log` for correct `yasserrmd` author and committer, permitted email, and zero AI/co-author trailers. No squash. Never commit directly to main.

## PR, merge, and completion evidence

1. Push `milestone/m18-security-red-team-compliance-and-reliability-hardening`; create a PR into `main` with scope, code evidence, linked acceptance items and test results.
2. Wait for **actual** required CI checks and any branch-protection reviews. Fix on the same milestone branch using additional atomic commits; do not fake or bypass required checks.
3. Once green and authorized, merge through GitHub; verify merged SHA on main, pull updated main, and delete local and remote milestone branches.
4. Create `docs/milestone-reports/M18.md` from the supplied template with PR URL, merge SHA, actual commits, checks, evidence and risk summary. Ensure the report itself is versioned in the PR **before merging**, not written only after the branch is deleted.
5. Update `docs/PHASE_STATUS.md` and continue automatically to the next file. If external authorization/credential is missing, record `BLOCKED` and precise remediation, work only on independent safe tasks, and never report unperformed PR/merge as done.

**Explicit handoff to next phase:** This milestone is complete only when the delivered capability can be used in the UI/API and its full positive/negative tests pass with verifiable artifacts.
