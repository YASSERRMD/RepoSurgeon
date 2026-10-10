# e2e

Playwright browser tests. They run against the **composed stack** (web served
by nginx, API and PostgreSQL in containers), never against mocks:

```bash
make dev          # start the stack (Docker required)
make e2e          # run the suite against http://127.0.0.1:8080
```

Configuration:

- `E2E_BASE_URL` — base URL of the web app (default `http://127.0.0.1:8080`)
- `E2E_WEB_SERVER` — optional command Playwright starts before the tests

Suites:

- `readiness.spec.ts` — brand, measured readiness checks, build facts, and
  agreement between what the browser shows and what `/health/ready` reports
- `responsive.spec.ts` — mobile viewport layout and absence of horizontal
  overflow

CI runs the same suite in `.github/workflows/compose-smoke.yml` after
`scripts/smoke-compose.sh` proves the stack is genuinely reachable.
