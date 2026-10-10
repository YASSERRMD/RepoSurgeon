# RepoSurgeon

Autonomous software engineering workspace: import an authorized GitHub issue,
let the agent investigate real source code with grounded, tool-scoped
capabilities, produce a controlled patch, run reproducible tests, and open a
reviewable pull request. CI, branch protection and human approval remain
authoritative — RepoSurgeon never merges on its own.

The default inference engine is **StepFun Step 5 Preview** (`step-5-preview`)
behind a model-agnostic provider interface; Step 3.7 Flash is the evaluation
baseline. Tool execution is implemented and authorized by RepoSurgeon's Go
services — the model is never assumed to browse, execute code, read files or
talk to GitHub by itself.

## Status

Milestone **M00 — repository foundation and engineering governance** is in
review. See `docs/milestone-tracker.md` and `docs/PHASE_STATUS.md` for the
authoritative progress ledger, and `docs/specification/` for the full
specification of the M00–M19 plan.

## Repository layout

```
cmd/api        HTTP API (health, system info; more surfaces per milestone)
cmd/worker     background supervisor (durable queue arrives in M06)
cmd/runner     isolated execution trust boundary (ADR-001)
internal/      domain packages: api, config, health, ids, migrations, redact,
               runner, store, worker
migrations/    numbered, reversible SQL migrations
api/           OpenAPI 3.1 contract of record
web/           Vue 3 + TypeScript strict SPA (typed client generated from api/)
e2e/           Playwright browser tests against the composed stack
fixtures/      deterministic test repositories
scripts/       local gates and CI helpers
docs/          phase status, milestone reports, ADRs, threat model, standards
infra/         deployment reference
```

## Quick start

Prerequisites: Go 1.25, Node 24, Docker with the Compose plugin, and a
PostgreSQL instance (local or containerised).

```bash
cp .env.example .env        # then set RS_RUNNER_TOKEN and DB credentials
make hooks                  # install the tracked Git hooks
make dev                    # build images and start postgres/api/worker/runner/web
make smoke                  # prove readiness, break+recover PostgreSQL, exec a runner command
make e2e                    # Playwright against http://127.0.0.1:8080
```

Without Docker, run the services directly against a local PostgreSQL:

```bash
export RS_DATABASE_URL=postgres://reposurgeon@127.0.0.1:5432/reposurgeon?sslmode=disable
export RS_ENV=development
make migrate-up             # apply migrations
make build && ./bin/api     # API on :8080 (override with RS_HTTP_ADDR)
```

## Quality gates

```bash
make check      # gofmt, vet, golangci-lint, go test -race, full web gate
make check-go   # Go gates only
make check-web  # web lint/format/types/tests/build/client-drift
make vuln       # govulncheck
make audit      # Go + npm dependency audits
```

The same commands run in CI (`.github/workflows/`) together with gitleaks secret
scanning, gosec, actionlint, image builds, the Compose smoke and the Playwright
suite.

## Security model in one paragraph

The browser is untrusted; model output is untrusted data; cloned code is
hostile by default. Only the separate `runner` process starts operating-system
processes, as a non-root user, with an explicit program allowlist, a confined
workspace directory, capped output and runtime, an internal no-egress network
and no access to the database or provider credentials. Authorization decisions
are made by deterministic Go policy code, never by text a model produced.
Full model: `docs/security/threat-model.md`, boundary decision:
`docs/adr/ADR-001-modular-monolith-and-isolated-runner.md`.

## License

MIT — see `LICENSE`.
