# ADR-001: Modular monolith with an isolated runner boundary

- Status: accepted
- Date: 2026-10-10
- Deciders: RepoSurgeon engineering (yasserrmd)
- Applies to: M00 onward

## Context

RepoSurgeon runs an autonomous agent that reads untrusted repository content
and executes generated commands on behalf of authorized users. The dominant
risk is privilege amplification: a prompt-injected issue, a malicious test
fixture or a model-proposed command escaping into the host, the database or
the GitHub account.

A microservice split would multiply deployment, authorization and data
consistency surface before the product has users; a single process with an
in-process executor would give untrusted workload access to every secret the
API holds.

## Decision

Build one Go **modular monolith** shipped as two binaries (`cmd/api`,
`cmd/worker`) with strict internal package boundaries (`internal/domain`,
`internal/store`, `internal/policy`, `internal/model`, `internal/github`,
`internal/gitworkspace`, `internal/agent`, `internal/tools`), plus a
**separate runner process** (`cmd/runner`) that is the only component allowed
to start operating-system processes.

Hard rules that follow from this decision:

1. The runner never receives the database DSN, StepFun key, GitHub App private
   key or installation tokens.
2. The API and worker never execute model-supplied commands themselves; every
   execution request goes through `POST /v1/exec` on the runner with a typed,
   allowlisted program and a confined workspace directory.
3. The runner refuses to run as root, caps runtime, output and argument sizes,
   and deduplicates repeated operation IDs.
4. Model output is untrusted data. Authorization decisions are made by
   deterministic Go policy code, never by text a model produced.
5. Persistence is PostgreSQL only, with versioned reversible migrations.

## Alternatives considered

- **Full microservices from day one**: rejected for M00; premature operational
  surface, distributed transactions for run state, slower iteration.
- **Single binary with in-process sandbox**: rejected; a compromised task
  process would hold every API secret and the DB connection.
- **Kubernetes Job per command**: deferred; heavy for local development, but
  the runner's HTTP contract allows replacing it later without changing
  callers.

## Consequences

Positive:

- Clear trust boundary that can be reasoned about and penetration-tested.
- Runner can be scaled, network-isolated and resource-limited independently.
- Local development runs the whole stack with one `docker compose up`.

Negative / costs:

- An extra network hop for tool execution, requiring leases, timeouts and
  idempotency keys on the runner protocol.
- The runner needs its own image, healthcheck and failure mode.

## Verification hooks

- `internal/runner` tests prove allowlist enforcement, workspace confinement,
  path and symlink escape rejection, root refusal, bounded output/timeouts and
  per-operation idempotency.
- `scripts/smoke-compose.sh` starts the runner with dropped capabilities, a
  read-only root filesystem, resource quotas, a dedicated network with no
  access to PostgreSQL or the API, and then executes an allowlisted command
  through ``POST /v1/exec``.
