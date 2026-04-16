# model/ sub-project Makefile — SQL migrations (Atlas) + Go query generation (sqlc).

DATABASE_URL ?= postgresql://users:users@localhost:5432/users?sslmode=disable

.DEFAULT_GOAL := build

.PHONY: build
build: gen ## Generate Go code from queries (the only build artifact)

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
	rm -rf internal/db

.PHONY: help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*?## "} \
		/^[a-zA-Z0-9_.\-]+:.*?## / { printf "  %-20s %s\n", $$1, $$2 }' \
		$(MAKEFILE_LIST) | sort
