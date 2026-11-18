.DEFAULT_GOAL := help

BINARY        := multena-proxy
COVERAGE_OUT  := coverage.out
COVERAGE_THRESHOLD ?= 75
GOLANGCI_LINT_VERSION := v2.15.0
IMAGE_NAME    ?= multena-proxy:local
CONTAINER_ENGINE ?= podman

.PHONY: help build test coverage lint vet vulncheck image helm-lint clean all ci

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

build: ## Build the multena-proxy binary (mirrors the CI "build" job)
	go build -o $(BINARY) .

test: ## Run tests with race detector + coverage profile (mirrors CI "test" job)
	go test -race -v -coverprofile=$(COVERAGE_OUT) ./...

coverage: test ## Run tests and enforce the coverage threshold (mirrors CI "Enforce coverage threshold")
	@total=$$(go tool cover -func=$(COVERAGE_OUT) | tail -1 | awk '{print $$3}' | tr -d '%'); \
	echo "Total statement coverage: $${total}% (threshold: $(COVERAGE_THRESHOLD)%)"; \
	awk -v total="$$total" -v threshold="$(COVERAGE_THRESHOLD)" 'BEGIN { exit !(total >= threshold) }' \
		|| { echo "Coverage $${total}% is below the required $(COVERAGE_THRESHOLD)%" >&2; exit 1; }

lint: ## Run golangci-lint (requires golangci-lint installed locally, see GOLANGCI_LINT_VERSION)
	@command -v golangci-lint >/dev/null 2>&1 || { \
		echo "golangci-lint not found. Install the version pinned in CI ($(GOLANGCI_LINT_VERSION)):"; \
		echo "  go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)"; \
		exit 1; \
	}
	golangci-lint run --issues-exit-code=1 --timeout=5m

vet: ## Run go vet (mirrors CI "lint" job's go vet step)
	go vet ./...

vulncheck: ## Run govulncheck (installs it if missing, mirrors CI "govulncheck" job)
	@command -v govulncheck >/dev/null 2>&1 || { \
		echo "govulncheck not found, installing..."; \
		go install golang.org/x/vuln/cmd/govulncheck@latest; \
	}
	govulncheck ./...

image: build ## Build the container image from build/Containerfile.Build
	$(CONTAINER_ENGINE) build -f build/Containerfile.Build -t $(IMAGE_NAME) .

helm-lint: ## Lint the Helm chart under charts/multena-proxy (optional, mirrors chart-ci.yml)
	helm lint charts/multena-proxy

clean: ## Remove build artifacts
	rm -f $(BINARY) $(COVERAGE_OUT)

ci: build vet lint test ## Minimal local equivalent of the CI pipeline (build + vet + lint + test)

all: ci ## Alias for ci
