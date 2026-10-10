# RepoSurgeon threat model (starter)

Derived from `docs/specification/SECURITY_GATES.md`. Each control names the
code that enforces it and the test that proves it. Update as capabilities land.

## Assets

| Asset | Where it lives | Exposure |
|---|---|---|
| GitHub App private key, installation tokens | API process only (`internal/github` from M04) | never in runner, DB or logs |
| StepFun API key | API process only (`internal/model` from M03) | never in runner or logs |
| User source code | workspace files + PostgreSQL rows | requires explicit consent to send to a provider |
| Run/audit event stream | PostgreSQL (append-only) | tenant-scoped reads |

## Trust boundaries

1. Browser → API: every request is authenticated and org/project-scoped from
   M02; no endpoint trusts an ID supplied by a client.
2. Model → tools: model JSON is validated against typed schemas, the tool
   allowlist, a path scope and a budget before any execution.
3. Workspace → host: only the isolated runner starts processes; it runs
   non-root with dropped capabilities, a read-only root filesystem, resource
   quotas and no network path to the database or the API. Namespace-based
   default-deny egress is added in M07.
4. Runner → GitHub: only the trusted GitHub adapter holds credentials; run
   results flow back as typed receipts.
5. API → StepFun: only authorized, redacted context crosses the provider
   boundary; request/response metadata is logged instead of payloads.

## Threats and controls (current implementation → M00)

| Threat | Control | Proof |
|---|---|---|
| Host command execution | `cmd/runner` allowlist, confined cwd, non-root, quotas | `internal/runner` positive/negative tests |
| Filesystem escape | canonical path with root enforcement, symlink resolution | `resolveWorkspaceDir` tests, `../` and symlink cases |
| Secret exfiltration via logs | `internal/redact` applied to logs and error details | `internal/redact` tests |
| Runner secret leakage into child env | closed env allowlist | `buildEnv` test |
| Resource exhaustion | bounded timeouts, output caps, arg count/length limits | runner tests (timeout, truncation, arg bounds) |
| Dependency on unauthenticated runner control plane | bearer token, constant-time compare | auth tests |
| Mass-assignment via API | typed decoders per route; unknown fields rejected (explicit decoders added with M02 routes) | route tests |

## Open items (tracked by milestone)

- Prompt-injection resistance and tool authority separation — M07, M08, M12.
- Webhook HMAC, nonce and replay protection — M04.
- Encryption at rest for credentials and per-org key isolation — M02.
- SSRF/egress control for dependency acquisition — M07.
- Branch-protection bypass resistance and human approval gate — M07, M13.
- Red-team suite and scan remediation — M18.
