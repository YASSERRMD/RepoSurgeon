# RepoSurgeon engineering standards

These rules apply to every milestone and are enforced by hooks, CI and review.

## Code

- Go: gofmt-clean, `go vet`-clean, golangci-lint clean, race-tested. Explicit
  error returns; no panics in request paths (recovered and logged if they
  happen); no `TODO` handlers in production paths.
- TypeScript: `strict`, `exactOptionalPropertyTypes`, `noUncheckedIndexedAccess`
  style discipline, no `any` without an eslint disable and a reason.
- Vue: one component per file, `<script setup lang="ts">`, scoped styles using
  the brand tokens in `web/src/styles/theme.css`.
- Every state-changing operation carries a unique operation ID, an
  authorization check, an audit record and idempotent behaviour.
- Business logic lives in `internal/*` packages; HTTP handlers stay thin.

## Contracts

- `api/openapi.yaml` is the source of truth. Server changes and the generated
  Vue client (`web/src/api/generated/schema.ts`) change in the same commit;
  CI fails when the generated client drifts.
- Errors always use `{"error":{code,message,request_id,details}}`.
- Mutations require `Idempotency-Key`; `X-Request-ID` is accepted and echoed.

## Database

- PostgreSQL only. Numbered, reversible migrations (`NNNN_name.up.sql` /
  `NNNN_name.down.sql`) applied one transaction per migration and recorded in
  `schema_migrations` with a checksum; editing an applied migration is an
  error (drift), not a warning.
- Test-only fixtures live in test databases; production never seeds accounts.

## Git

- Local author/committer identity is exactly `yasserrmd <arafath.yasser@gmail.com>`.
- No co-author, generated-by, AI or bot trailers. No squash merges. No direct
  commits to `main`.
- Commit subjects use the milestone prefix: `mNN: imperative summary`.
- Branch per milestone: `milestone/mNN-<slug>`; land through a PR with green CI.

## Tests

- New behaviour needs positive and negative cases; concurrency behaviour needs
  race-enabled tests.
- Mocks are allowed only for protocol/contract tests; adapters are exercised
  against real systems in integration tests (PostgreSQL is always available to
  tests via `RS_TEST_DATABASE_URL`).
- No skipped tests without a recorded technical reason in the phase report.

## Review

- One approving review from the code owner of each touched boundary before
  merge (`internal/runner`, `internal/policy`, `internal/auth`, `migrations/`).
