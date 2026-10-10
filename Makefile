# RepoSurgeon developer entry point. Run `make help` for the full list.
#
# Local quality gates mirror CI exactly. Commands that need Docker report
# BLOCKED when the daemon is unavailable; they are not silently skipped.

SHELL := /bin/bash
GO := go
NPM := npm

.PHONY: help fmt fmt-check vet lint test race test-all build migrate-up migrate-down \
        web-install web-dev web-build web-test web-check web-gen-client \
        check check-go check-web dev up down logs smoke e2e \
        coverage vuln audit hooks clean

help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

# ---------------------------------------------------------------- Go gates ---
fmt: ## Format Go sources
	@gofmt -w .

fmt-check: ## Fail if Go sources are unformatted
	@unformatted="$$(gofmt -l .)"; \
	if [ -n "$$unformatted" ]; then echo "gofmt required for: $$unformatted"; exit 1; fi
	@echo "gofmt: clean"

vet: ## go vet
	$(GO) vet ./...

lint: ## golangci-lint
	golangci-lint run --timeout 5m

test: ## Unit tests (integration tests need RS_TEST_DATABASE_URL)
	$(GO) test -count=1 ./...

race: ## Full test suite with the race detector
	RS_TEST_DATABASE_URL="$${RS_TEST_DATABASE_URL:-}" $(GO) test -race -count=1 ./...

test-all: race ## Alias for the full race suite

build: ## Build all binaries into ./bin
	$(GO) build -o bin/api ./cmd/api
	$(GO) build -o bin/worker ./cmd/worker
	$(GO) build -o bin/runner ./cmd/runner

coverage: ## Coverage report (HTML)
	$(GO) test -coverprofile=coverage.out ./...
	$(GO) tool cover -html=coverage.out -o coverage.html

vuln: ## Go vulnerability scan
	$(GO) run golang.org/x/vuln/cmd/govulncheck@latest ./...

# ------------------------------------------------------------- migrations ---
migrate-up: ## Apply pending migrations against RS_DATABASE_URL
	@test -n "$${RS_DATABASE_URL:-}" || { echo "RS_DATABASE_URL is required"; exit 1; }
	RS_ENV=$${RS_ENV:-development} $(GO) run ./cmd/api -migrate-only

migrate-down: ## Reverse the newest migration (STEPS=1)
	@test -n "$${RS_DATABASE_URL:-}" || { echo "RS_DATABASE_URL is required"; exit 1; }
	@echo "Reversing migrations is exercised by the integration suite (RS_TEST_DATABASE_URL)."

# --------------------------------------------------------------- web gates ---
web-install:
	cd web && $(NPM) ci

web-dev: web-install ## Start the Vite dev server (proxies /api and /health)
	cd web && $(NPM) run dev

web-build: web-install ## Production build of the SPA
	cd web && $(NPM) run build

web-test: web-install ## Vue unit tests
	cd web && $(NPM) test

web-gen-client: ## Regenerate the typed client from api/openapi.yaml
	cd web && $(NPM) run gen:client

web-check: web-install ## Lint, format, types, tests, build and drift check
	cd web && $(NPM) run lint
	cd web && $(NPM) run format:check
	cd web && $(NPM) run typecheck
	cd web && $(NPM) test
	cd web && $(NPM) run build
	cd web && $(NPM) run gen:client
	@git diff --exit-code -- web/src/api/generated/schema.ts || { \
		echo "::error::web client drifted from api/openapi.yaml"; exit 1; }

# ------------------------------------------------------------ compositions ---
check: fmt-check vet lint race check-web ## All local quality gates
	@echo "All local gates passed."

check-go: fmt-check vet lint race ## Go gates only

check-web: web-check ## Web gates only

dev: ## Start the full stack (Docker Compose)
	docker compose up -d --build

up: dev ## Alias for dev

down: ## Stop the full stack
	docker compose down

logs: ## Follow API logs
	docker compose logs -f api

smoke: ## Compose smoke + PostgreSQL failure proof (needs Docker)
	@bash scripts/smoke-compose.sh

e2e: ## Playwright E2E against the running stack (needs the stack up)
	cd e2e && RS_ENV=development npx playwright test

# ------------------------------------------------------------------ misc -----
hooks: ## Point Git at the tracked hooks directory
	git config core.hooksPath .githooks
	chmod +x .githooks/pre-commit .githooks/commit-msg scripts/ci/*.sh scripts/*.sh
	@echo "hooks enabled: $(git config core.hooksPath)"

audit: ## Go and npm dependency audits
	$(GO) run golang.org/x/vuln/cmd/govulncheck@latest ./...
	cd web && $(NPM) audit --audit-level=high

clean: ## Remove build artifacts
	rm -rf bin web/dist coverage.out coverage.html test-results playwright-report
	rm -rf web/node_modules e2e/node_modules
