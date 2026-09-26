.PHONY: help verify test lint sec contract-check tools

help: ## Show targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-16s\033[0m %s\n",$$1,$$2}'

verify: ## Run every gate (tests, lint, security, contract) — required before "done"
	@bash .agents/skills/verify/scripts/verify.sh

test: ## Unit tests with race detector and coverage
	go test -race -cover ./...

lint: ## Static analysis
	golangci-lint run

sec: ## Security scans
	gosec ./... && govulncheck ./... && gitleaks detect --no-banner --redact

contract-check: ## Lint OpenAPI contract
	npx --no-install @redocly/cli lint contracts/openapi.yaml

tools: ## Install verification tools
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
	go install github.com/securego/gosec/v2/cmd/gosec@latest
	go install golang.org/x/vuln/cmd/govulncheck@latest
	@echo "Install gitleaks: brew install gitleaks"
	npm i -D @redocly/cli
