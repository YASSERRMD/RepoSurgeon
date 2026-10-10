## Summary

<!-- What does this milestone change deliver? Reference the milestone file. -->

Milestone: <!-- e.g. M00 — Repository foundation and engineering governance -->

## Acceptance evidence

| Requirement | Evidence (route, test, artifact) | Result |
|---|---|---|
|  |  |  |

## Local verification (attach real logs)

- [ ] `make check-go` (gofmt, vet, golangci-lint, `go test -race ./...`)
- [ ] `make check-web` (lint, format, type-check, unit tests, build, client drift)
- [ ] `bash scripts/smoke-compose.sh` (image build, compose start/stop, readiness break/recover)
- [ ] `make e2e` (Playwright)
- [ ] `bash scripts/ci/verify-commit-metadata.sh origin/main HEAD`

## Security checklist

- [ ] No secrets, tokens or `.env` content in the diff
- [ ] Tenant scoping and authorization verified where applicable
- [ ] Sandbox/tool authority boundaries unchanged or strengthened
- [ ] OpenAPI contract updated and typed client regenerated

## Notes / risks

<!-- Known limitations, follow-ups, BLOCKED items with exact remediation. -->
