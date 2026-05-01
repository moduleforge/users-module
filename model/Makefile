# model/ sub-project Makefile — SQL migrations (Atlas) + Go query generation (sqlc).

DATABASE_URL ?= postgresql://users:users@localhost:5432/users?sslmode=disable

# Locate core-model migrations via go list, falling back to a relative path
# for fresh clones before go work sync has run.
CORE_DIR := $(shell go list -m -f '{{.Dir}}' github.com/moduleforge/core-model 2>/dev/null || echo ../../core-module/model)

# Set by preflight if sqlc is available.
SQLC ?=

.DEFAULT_GOAL := build

# ---------------------------------------------------------------------------
# Compose — assemble core + users-module migrations into schema/migrations/
# ---------------------------------------------------------------------------

.PHONY: compose
compose: ## Assemble composed migrations dir from core-model + users-module
	@rm -rf schema/migrations
	@mkdir -p schema/migrations
	@cp $(CORE_DIR)/migrations/*.sql schema/migrations/
	@cp migrations/*.sql schema/migrations/
	@atlas migrate hash --dir file://schema/migrations > /dev/null

build: compose
migrate.up: compose
migrate.status: compose
migrate.hash: compose

# ---------------------------------------------------------------------------
# Preflight — verify tools, set vars
# ---------------------------------------------------------------------------

.PHONY: preflight
preflight: ## Check required tools for model
	@printf "  model preflight: "
	@if command -v sqlc >/dev/null 2>&1; then \
		echo "sqlc=$$(sqlc version) ✓"; \
	elif [ -d db ]; then \
		echo "sqlc not found (using committed generated code) ✓"; \
	else \
		echo "FAIL"; \
		echo "ERROR: sqlc is not installed and no generated code exists in db/."; \
		echo "  Install: go install github.com/sqlc-dev/sqlc/cmd/sqlc@v1.28.0"; \
		exit 1; \
	fi

# ---------------------------------------------------------------------------
# Build
# ---------------------------------------------------------------------------

.PHONY: build
build: preflight ## Generate Go code from queries
	@if command -v sqlc >/dev/null 2>&1; then \
		sqlc generate; \
	else \
		echo "model: using existing generated code in db/"; \
	fi

# ---------------------------------------------------------------------------
# Migration targets
# ---------------------------------------------------------------------------

.PHONY: migrate.new
migrate.new: ## Create a new migration file (usage: make migrate.new NAME=foo)
	@test -n "$(NAME)" || (echo "ERROR: NAME is required (e.g. make migrate.new NAME=create_users)" && exit 1)
	atlas migrate new $(NAME) --dir "file://migrations"

.PHONY: migrate.up
migrate.up: ## Apply pending migrations to DATABASE_URL
	atlas migrate apply --dir "file://migrations" --url "$(DATABASE_URL)"

.PHONY: migrate.status
migrate.status: ## Show migration status
	atlas migrate status --dir "file://migrations" --url "$(DATABASE_URL)"

.PHONY: migrate.hash
migrate.hash: ## Recalculate migration directory integrity hash
	atlas migrate hash --dir "file://migrations"

# ---------------------------------------------------------------------------
# Code generation
# ---------------------------------------------------------------------------

.PHONY: gen
gen: ## Generate Go packages from sqlc queries
	sqlc generate

# ---------------------------------------------------------------------------
# Verification
# ---------------------------------------------------------------------------

.PHONY: verify
verify: ## Validate migrations (atlas) + compile-check queries (sqlc)
	atlas migrate validate --dir "file://migrations" --dev-url "$(DATABASE_URL)"
	sqlc compile

# ---------------------------------------------------------------------------
# Canonical targets (delegated from root Makefile)
# ---------------------------------------------------------------------------

.PHONY: test
test: test.unit ## Alias for test.unit

.PHONY: test.unit
test.unit: ## Run unit tests
	@echo "model: no unit tests (generated code)"

.PHONY: test.integration
test.integration: ## Run integration tests (migration apply + verify against live DB)
	atlas migrate apply --dir "file://migrations" --url "$(DATABASE_URL)"

.PHONY: lint
lint: ## Lint migrations
	atlas migrate lint --dir "file://migrations" --dev-url "$(DATABASE_URL)" --latest 1

.PHONY: lint-fix
lint-fix: ## Lint-fix (no-op for model)
	@echo "model: no auto-fix available"

.PHONY: dev.start
dev.start: ## No runtime for model
	@echo "model: no runtime; nothing to start"

.PHONY: dev.stop
dev.stop: ## No runtime for model
	@echo "model: no runtime; nothing to stop"

.PHONY: clean
clean: ## Remove generated Go code (preserves migrations and queries)
	rm -rf db

.PHONY: help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*?## "} \
		/^[a-zA-Z0-9_.\-]+:.*?## / { printf "  %-20s %s\n", $$1, $$2 }' \
		$(MAKEFILE_LIST) | sort
