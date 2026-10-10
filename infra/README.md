# Deployment reference

`docker-compose.yml` is the local reference deployment. Production uses the
same images with different configuration; this document records the exact
requirements rather than pretending a production cluster exists.

## Services and boundaries

| Service | Image | Exposes | Holds secrets |
|---|---|---|---|
| `postgres` | `postgres:16-alpine` | 5432 (internal) | DB credentials |
| `migrate` | `Dockerfile.api` (one-shot) | — | DB credentials |
| `api` | `Dockerfile.api` | 8081 → 8080 | DB DSN, (later) StepFun key, GitHub App key |
| `worker` | `Dockerfile.worker` | 8082 → 8080 | DB DSN only |
| `runner` | `Dockerfile.runner` | 8091 | **only** `RS_RUNNER_TOKEN` |
| `web` | `web/Dockerfile` | 8080 → 80 | none |

`runner` is attached solely to `runner-net`, an `internal: true` network with
no route to the internet, and runs with `cap_drop: [ALL]`,
`security_opt: [no-new-privileges:true]`, `read_only: true`, `pids_limit`,
`mem_limit` and `cpus` quotas, as a fixed non-root UID/GID. It never receives
the database DSN or provider credentials.

## Required configuration

See `.env.example`. Non-test environments refuse to boot when a required
secret is missing; the process exits with the exact variable name.

## Environment checklist

1. `RS_ENV=production` (or `development` locally)
2. `RS_DATABASE_URL` pointing at the managed PostgreSQL instance
3. `RS_RUNNER_TOKEN` (256-bit random) shared with callers
4. `RS_RUNNER_ALLOWED_PROGRAMS` restricted to the commands your policy permits
5. From M03: `STEPFUN_API_KEY`; from M04: GitHub App credentials and webhook
   secret

## Health and recovery

- `api/health/live` — process liveness; always 200 while serving
- `api/health/ready` — 200 only with a reachable database and no pending
  critical migrations; 503 includes per-check remediation text
- `worker/healthz` — worker dependency report; the worker exits after repeated
  failures so the runtime restarts it
- `runner/v1/healthz` — runner liveness; execution requests require the bearer
  token

## Known limitations (M00)

- The runner enforces process-level confinement: non-root, allowlisted
  programs, confined workspace, bounded output/runtime, no credentials.
  Namespace-based default-deny egress (network namespace + seccomp profile)
  is added with M07 on the Linux runner image. The runner already has no
  network path to PostgreSQL or the API/worker.
- Compose pins resource quotas but not cgroup v2 delegation; production
  deployments set the equivalent cgroup limits explicitly.
- Docker networks marked `internal: true` also block host access to published
  ports, so the runner network is not marked internal; the isolation
  properties above are enforced per-service instead.
