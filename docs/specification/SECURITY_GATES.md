# RepoSurgeon — Security, Trust, and Release Gates

## Threat model

RepoSurgeon handles untrusted issue text, arbitrary repository contents, potentially malicious build scripts, external model outputs, remote GitHub events and user-provided credentials. The central risk is privilege amplification: allowing an AI-generated action or untrusted repo content to escape the authorized worktree or GitHub review process.

## Mandatory concrete controls

| Threat | Enforced control | Must-fail negative test |
|---|---|---|
| Prompt-injected issue/README | Strict system/tool authority separation, schema/policy checks | Repo file asking for tokens/merge cannot grant permission |
| Host command execution | No arbitrary command directly on host; isolated non-root executor | `cat /etc/shadow`, `docker.sock` and host paths denied |
| Secret theft | StepFun/GitHub keys outside runner; per-org encryption; redaction | Injected code or logs cannot recover keys |
| Filesystem escapes | Canonical path with root enforcement, symlink protection | `../`, symlink/hardlink escape attempts rejected |
| Cross-tenant IDOR | Ownership on every record/query and event stream | User A cannot fetch User B event/diff/token |
| Webhook forgery/replay | HMAC+nonce/replay store and deduped deliveries | Invalid signature/delivery replay never mutates |
| SSRF/network egress | Default no egress from executor; audited allowlist only | Runner cannot call metadata endpoint or arbitrary host |
| Dependency malware | Isolated dependency stage, pinned lockfiles, network restrictions | Malicious postinstall cannot reach host/credentials |
| Branch protection bypass | GitHub required checks/review/mergeability queried | Red CI or missing approval blocks merge |
| Repeated external actions | Effect IDs + verify GitHub state on resume | Retries cannot open duplicate PR / double merge |
| Resource exhaustion | CPU/memory/time/PID/output/token/cost caps | Fork bomb/large output stops and audits |
| Provider data leakage | Consent/authorization for source submission, configurable retention | Unapproved source transmission is blocked |

## Verification policy

- All new code: Go lint/vet/race/unit, Vue lint/types/test/build, integration with actual PostgreSQL, OpenAPI drift, E2E smoke, secret scan and dependency/container vulnerability checks.
- Run SAST, dependency/SBOM audit, GitHub Actions permission review and threat-case suite before M19 release.
- Block on any unfixed critical/high exploitable findings; require explicit review for medium findings and document residual risks.
- Store sanitized evidence with failing action, protection outcome and test ID. Never store actual secrets or model private reasoning.
- A failing security test cannot be skipped, xfailed or marked flaky without verified technical cause and documented corrective action.

## Approval and authority

Approved actions require real, authenticated decision records. A model's `request_approval` tool call is not approval. Separate privileges for read, test, edit, PR, merge and external deletion. Build default policies that disallow automatic merging into arbitrary third-party/production repositories. No silent elevation to `admin` or altered repository branch protections.

## Privacy and external provider data

Code and issues may be confidential. Require explicit authorization to send repository data to StepFun. Make opt-in project scope visible. Limit context to necessary files; enforce redaction of tokens, API keys, personally identifying data where practical, and secure logging. Provide audit/export/retention and deletion controls. Provider hosting jurisdiction and contractual data handling must be reviewed before organizational use; never assume UAE data residency.
