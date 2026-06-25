# =============================================================================
# Makefile — Harness Engineering Orchestration
# =============================================================================
# This Makefile is the single entry point for all harness checks. Each target
# maps to a specific regulation category:
#
#   Guides (Feedforward):  fmt, imports, lint
#   Sensors (Feedback):    test, vet, arch, race, cover
#
# The aggregate targets run everything in the correct order:
#
#   make harness     → fast checks (fmt-check, vet, lint, test, arch)
#   make harness-full → everything including race detector and coverage
#
# Usage:
#   make help        → list all targets
#   make test        → run unit tests
#   make lint        → run golangci-lint
#   make arch        → run architecture fitness sensor
# =============================================================================

GO          ?= go
GOLANGCI    ?= golangci-lint
MODULE      := bedrock-ai
GOFLAGS     := -count=1
TEST_PKGS   := ./internal/...
BUILD_PKGS  := ./cmd/... ./internal/...

# Colors for output (disabled on Windows CI).
ifeq ($(OS),Windows_NT)
	CLR_PASS :=
	CLR_FAIL :=
	CLR_INFO :=
	CLR_RESET :=
else
	CLR_PASS := \033[32m
	CLR_FAIL := \033[31m
	CLR_INFO := \033[36m
	CLR_RESET := \033[0m
endif

.PHONY: help
help: ## Show all available targets
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  $(CLR_INFO)%-18s$(CLR_RESET) %s\n", $$1, $$2}'

# =============================================================================
# Guides (Feedforward Controls) — prevent issues before they happen
# =============================================================================

.PHONY: fmt
fmt: ## Format all Go source files
	@echo "$(CLR_INFO)→ gofmt$(CLR_RESET)"
	@$(GO) fmt ./...

.PHONY: imports
imports: ## Organize imports with goimports (if available)
	@echo "$(CLR_INFO)→ goimports$(CLR_RESET)"
	@command -v goimports >/dev/null 2>&1 && goimports -w . || echo "  goimports not installed, skipping"

.PHONY: fmt-check
fmt-check: ## Check formatting without modifying files
	@echo "$(CLR_INFO)→ gofmt check$(CLR_RESET)"
	@unformatted=$$(gofmt -l . 2>/dev/null | grep -v vendor | grep -v node_modules); \
	if [ -n "$$unformatted" ]; then \
		echo "$(CLR_FAIL)✗ Files need formatting:$(CLR_RESET)"; \
		echo "$$unformatted"; \
		exit 1; \
	fi
	@echo "$(CLR_PASS)✓ All files formatted$(CLR_RESET)"

.PHONY: lint
lint: ## Run golangci-lint (maintainability guide)
	@echo "$(CLR_INFO)→ golangci-lint$(CLR_RESET)"
	@command -v $(GOLANGCI) >/dev/null 2>&1 || { \
		echo "$(CLR_FAIL)✗ golangci-lint not installed. Run: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest$(CLR_RESET)"; \
		exit 1; \
	}
	@$(GOLANGCI) run ./...

.PHONY: lint-fix
lint-fix: ## Run golangci-lint with auto-fix
	@echo "$(CLR_INFO)→ golangci-lint --fix$(CLR_RESET)"
	@$(GOLANGCI) run --fix ./...

# =============================================================================
# Sensors (Feedback Controls) — detect issues after changes
# =============================================================================

.PHONY: build
build: ## Build all packages
	@echo "$(CLR_INFO)→ go build$(CLR_RESET)"
	@$(GO) build $(BUILD_PKGS)

.PHONY: vet
vet: ## Run go vet (static analysis)
	@echo "$(CLR_INFO)→ go vet$(CLR_RESET)"
	@$(GO) vet $(BUILD_PKGS)

.PHONY: test
test: ## Run unit tests
	@echo "$(CLR_INFO)→ go test$(CLR_RESET)"
	@$(GO) test $(GOFLAGS) $(TEST_PKGS)

.PHONY: test-verbose
test-verbose: ## Run unit tests with verbose output
	@echo "$(CLR_INFO)→ go test -v$(CLR_RESET)"
	@$(GO) test $(GOFLAGS) -v $(TEST_PKGS)

.PHONY: race
race: ## Run tests with race detector
	@echo "$(CLR_INFO)→ go test -race$(CLR_RESET)"
	@$(GO) test $(GOFLAGS) -race $(TEST_PKGS)

.PHONY: cover
cover: ## Run tests with coverage report
	@echo "$(CLR_INFO)→ go test -coverprofile$(CLR_RESET)"
	@$(GO) test $(GOFLAGS) -coverprofile=coverage.out $(TEST_PKGS)
	@$(GO) tool cover -func=coverage.out | tail -1
	@echo "  Full report: $(GO) tool cover -html=coverage.out"

.PHONY: cover-html
cover-html: ## Generate HTML coverage report
	@$(GO) test $(GOFLAGS) -coverprofile=coverage.out $(TEST_PKGS)
	@$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

.PHONY: arch
arch: ## Run architecture fitness sensor (module boundary checks)
	@echo "$(CLR_INFO)→ architecture fitness$(CLR_RESET)"
	@$(GO) test $(GOFLAGS) -run TestArchitecture ./internal/harness/architecture/... -v 2>/dev/null || \
		$(GO) test $(GOFLAGS) ./internal/harness/architecture/...

# =============================================================================
# Aggregate targets — run the full harness
# =============================================================================

.PHONY: harness
harness: fmt-check vet lint build test arch ## Run fast harness checks (guides + sensors)
	@echo ""
	@echo "$(CLR_PASS)✓ Harness PASSED — all fast checks green$(CLR_RESET)"

.PHONY: harness-full
harness-full: fmt-check vet lint build test race cover arch ## Run full harness including race detector and coverage
	@echo ""
	@echo "$(CLR_PASS)✓ Full harness PASSED — all checks green$(CLR_RESET)"

.PHONY: ci
ci: harness-full ## CI entry point (same as harness-full)
	@echo "$(CLR_PASS)✓ CI harness complete$(CLR_RESET)"

# =============================================================================
# Utilities
# =============================================================================

.PHONY: clean
clean: ## Remove build artifacts and coverage files
	@rm -f coverage.out coverage.html
	@rm -f proxy.exe
	@echo "Cleaned artifacts"

.PHONY: deps
deps: ## Download and tidy dependencies
	@$(GO) mod download
	@$(GO) mod tidy

.PHONY: install-hooks
install-hooks: ## Install git pre-commit hook
	@echo "$(CLR_INFO)→ Installing pre-commit hook$(CLR_RESET)"
	@if [ -d .git ]; then \
		cp scripts/pre-commit .git/hooks/pre-commit; \
		chmod +x .git/hooks/pre-commit; \
		echo "  Installed .git/hooks/pre-commit"; \
	else \
		echo "$(CLR_FAIL)✗ No .git directory found$(CLR_RESET)"; \
	fi

.PHONY: tidy
tidy: ## Run go mod tidy
	@$(GO) mod tidy

# =============================================================================
# Default target
# =============================================================================

.DEFAULT_GOAL := help
