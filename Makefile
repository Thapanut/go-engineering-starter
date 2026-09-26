.PHONY: help verify test test-integration lint sec contract-check tools run run-memory token db-up db-down db-reset docker-build

# Local dev defaults; override via environment or .env (never commit .env).
-include .env
export
TEST_DB_DSN ?= postgres://app_user:dev_only_password@localhost:55432/app_test?sslmode=disable

help: ## Show targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-16s\033[0m %s\n",$$1,$$2}'

verify: ## Run every gate (tests, integration, lint, security, contract) — required before "done"
	@if docker compose up -d --wait db >/dev/null 2>&1; then export TEST_DB_DSN="$(TEST_DB_DSN)"; else unset TEST_DB_DSN; fi; \
		bash .agents/skills/verify/scripts/verify.sh

test: ## Unit tests with race detector and coverage (no DB needed)
	go test -race -cover ./...

test-integration: db-up ## Integration tests against PostgreSQL in Docker
	go test -race -count=1 -tags=integration ./internal/adapter/outbound/postgres/...

lint: ## Static analysis (includes hexagonal import rules, see .golangci.yml)
	golangci-lint run

sec: ## Security scans
	gosec -quiet ./... && govulncheck ./... && gitleaks git --no-banner --redact

contract-check: ## Lint OpenAPI contract
	npx --no-install @redocly/cli lint contracts/openapi.yaml

run: db-up ## Run the API against PostgreSQL (needs .env: cp .env.example .env)
	go run ./cmd/api

run-memory: ## Run the API with the in-memory store (no Docker needed)
	STORE=memory go run ./cmd/api

token: ## Mint a dev JWT: make token SUB=demo-customer
	@go run ./cmd/devtoken -sub $(or $(SUB),demo-customer)

db-up: ## Start PostgreSQL (init SQL applied on first start)
	docker compose up -d --wait db

db-down: ## Stop PostgreSQL
	docker compose down

db-reset: ## Destroy and recreate the database
	docker compose down -v && docker compose up -d --wait db

docker-build: ## Build the production image
	docker build -t go-engineering-starter:local .

tools: ## Install verification tools
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
	go install github.com/securego/gosec/v2/cmd/gosec@latest
	go install golang.org/x/vuln/cmd/govulncheck@latest
	@echo "Install gitleaks: brew install gitleaks"
	npm ci
