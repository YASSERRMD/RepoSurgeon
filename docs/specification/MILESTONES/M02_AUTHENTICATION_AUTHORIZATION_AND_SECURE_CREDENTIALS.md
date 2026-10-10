# RepoSurgeon — M02: Authentication, Authorization, and Secure Credentials

> Execution order: M01 → M02. Required branch: `milestone/m02-authentication-authorization-and-secure-credentials`. Execute one phase fully; do not claim completion until gates are actually green.
>
> Mandatory before starting: read `CLAUDE.md`, `RUN_FIRST.md`, `MASTER_IMPLEMENTATION_PLAN.md`, `ARCHITECTURE.md`, `GIT_WORKFLOW.md`, `SECURITY_GATES.md`, `API_CONTRACTS.md`, and all previous milestone completion reports. Follow the existing repository architecture where compatible; do not overwrite existing work.

## Objective and tangible outcome

Secure access to organizations, StepFun keys and GitHub credentials; implement enforceable permission boundaries.

**Definition of done:** A verified user-visible vertical slice running against actual Go backend + PostgreSQL + Vue frontend, with real tests and artifacts. No TODO implementations, mock production APIs, static demo data, unexplained skipped tests, or artificial success labels.

## Detailed implementation tasks — complete every item

1. Implement secure local authentication for development and a documented OIDC integration path for production; never ship test bypass into production.
2. Implement server-side sessions or short-lived signed access tokens with refresh rotation, logout/revocation, secure cookie rules and CSRF defense where cookies are used.
3. Introduce roles `owner`, `maintainer`, `reviewer`, `viewer` and separate explicit permissions for project read, execution, credential administration, approval and merge.
4. Encrypt provider credentials using envelope-style AEAD encryption and a deployment-supplied key; rotate keys with version metadata; mask values on all read APIs.
5. Design GitHub App integration credentials and StepFun API keys as per-organization encrypted records; restrict retrieval to adapter services outside tool sandboxes.
6. Implement frontend login/logout, member management, credential create/rotate/revoke and permission-driven UI; backend remains authoritative.
7. Emit audit records for failed authentication, credential writes, permission denials, approvals and revocations.
8. Add threat and negative tests for IDOR, CSRF, brute-force controls, replay, session fixation and inadvertent secret exposure.

## Required end-to-end interfaces

**Backend and OpenAPI:** POST /api/v1/auth/login, /logout, /refresh; GET /api/v1/me; CRUD /members; POST /credentials; POST /credentials/{id}/rotate; DELETE /credentials/{id}.

**Persistence and migration:** Membership roles, session revocations, encrypted credential blobs, key versions and audit events.

**Frontend and interaction:** Real login, protected routes, permission messaging and masked credential settings with verification status.

For every new endpoint define request/response/error examples and access controls in OpenAPI; regenerate typed Vue clients and include drift checks. Use real PostgreSQL transactions and never invent successful adapter responses.

## Negative cases and security constraints

- Untrusted repository content may not change role, approval or credential permissions.
- No API key is ever passed to generated code or repository test processes.

## Mandatory verification scenarios

- [ ] Integration tests for positive/negative role matrix across all protected routes.
- [ ] Verify secret value never occurs in JSON responses, logs, browser storage or SSE.
- [ ] Test key rotation and invalid key/failure states with no loss of unrelated data.
- [ ] Playwright session expiration/logout and unauthorized page tests.

Also run the repository-wide regression suite, Go formatting/vet/lint/race tests, frontend formatting/lint/type-check/build/tests, generated contract drift, integration tests, secret scan, dependency/security audits, Docker image/Compose smoke, and existing E2E tests. A command that cannot run because of missing prerequisites is **BLOCKED**, not passed. Fix any introduced regression before creating a PR.

## Atomic commit map (suggested minimum)

1. `m02: auth schema and services`
2. `m02: roles/middleware and negative tests`
3. `m02: credential encryption/rotation`
4. `m02: auth and credential frontend`
5. `m02: security tests and CI gates`

Write each commit only after its contained work and relevant tests pass. Check `git log` for correct `yasserrmd` author and committer, permitted email, and zero AI/co-author trailers. No squash. Never commit directly to main.

## PR, merge, and completion evidence

1. Push `milestone/m02-authentication-authorization-and-secure-credentials`; create a PR into `main` with scope, code evidence, linked acceptance items and test results.
2. Wait for **actual** required CI checks and any branch-protection reviews. Fix on the same milestone branch using additional atomic commits; do not fake or bypass required checks.
3. Once green and authorized, merge through GitHub; verify merged SHA on main, pull updated main, and delete local and remote milestone branches.
4. Create `docs/milestone-reports/M02.md` from the supplied template with PR URL, merge SHA, actual commits, checks, evidence and risk summary. Ensure the report itself is versioned in the PR **before merging**, not written only after the branch is deleted.
5. Update `docs/PHASE_STATUS.md` and continue automatically to the next file. If external authorization/credential is missing, record `BLOCKED` and precise remediation, work only on independent safe tasks, and never report unperformed PR/merge as done.

**Explicit handoff to next phase:** This milestone is complete only when the delivered capability can be used in the UI/API and its full positive/negative tests pass with verifiable artifacts.
