# gui/ sub-project Makefile (@moduleforge/users-gui pure component library).
# Self-contained: implements the canonical target contract used by the root
# Makefile so it can be invoked standalone or via `make -C gui <target>`.
# Build: tsup (JS/TS) + @tailwindcss/cli (CSS). Preview: ladle on port 61002.

.DEFAULT_GOAL := build

# Detect current platform (e.g. "Darwin" on macOS, "Linux" in containers).
CURRENT_OS := $(shell uname -s)

# bun is the package manager for this workspace.
PM := bun
NPX := bunx

# bun hoists deps to the workspace root. We need to track platform there too.
# Resolve root: walk up looking for bun.lock (workspace root marker).
WORKSPACE_ROOT := $(shell d=$$PWD; while [ "$$d" != "/" ]; do \
	if [ -f "$$d/bun.lock" ]; then echo "$$d"; break; fi; \
	d=$$(dirname "$$d"); done)
# Fall back to just this directory if not in a workspace.
ifeq ($(WORKSPACE_ROOT),)
WORKSPACE_ROOT := .
endif

PLATFORM_STAMP := $(WORKSPACE_ROOT)/node_modules/.platform

# ---------------------------------------------------------------------------
# Preflight — verify tools, fix cross-platform node_modules
# ---------------------------------------------------------------------------

.PHONY: preflight
preflight: ## Check tools + fix stale node_modules
	@printf "  gui preflight: "
	@if ! command -v node >/dev/null 2>&1; then \
		echo "FAIL"; \
		echo "ERROR: node is not installed."; \
		exit 1; \
	fi
	@if ! command -v bun >/dev/null 2>&1; then \
		echo "FAIL"; \
		echo "ERROR: bun is not installed. See https://bun.sh"; \
		exit 1; \
	fi
	@NEEDS_INSTALL=false; \
	if [ -f $(PLATFORM_STAMP) ] && [ "$$(cat $(PLATFORM_STAMP))" != "$(CURRENT_OS)" ]; then \
		echo "node_modules installed on $$(cat $(PLATFORM_STAMP)), current platform is $(CURRENT_OS) — reinstalling..."; \
		rm -rf $(WORKSPACE_ROOT)/node_modules node_modules; \
		NEEDS_INSTALL=true; \
	elif [ -d $(WORKSPACE_ROOT)/node_modules ] && ! [ -f $(PLATFORM_STAMP) ]; then \
		echo "node_modules missing platform stamp — reinstalling to be safe..."; \
		rm -rf $(WORKSPACE_ROOT)/node_modules node_modules; \
		NEEDS_INSTALL=true; \
	elif ! [ -d node_modules ] && ! [ -d $(WORKSPACE_ROOT)/node_modules ]; then \
		echo "node_modules missing — installing..."; \
		NEEDS_INSTALL=true; \
	fi; \
	if [ "$$NEEDS_INSTALL" = "true" ]; then \
		cd $(WORKSPACE_ROOT) && $(PM) install; \
		echo "$(CURRENT_OS)" > $(PLATFORM_STAMP); \
	else \
		echo "node=$$(node --version) pm=$(PM) platform=$(CURRENT_OS) ✓"; \
	fi

# ---------------------------------------------------------------------------
# Build
# ---------------------------------------------------------------------------

.PHONY: build
build: preflight ## Build the component library (tsup JS/TS + CSS via tailwindcss-cli)
	$(PM) run build

.PHONY: test
test: test.unit ## Alias for test.unit

.PHONY: test.unit
test.unit: preflight ## Run unit tests (none yet — placeholder passes)
	@echo "gui: no unit tests configured yet"

.PHONY: test.integration
test.integration: ## Run integration tests (none yet)
	@echo "gui: no integration tests yet"

.PHONY: lint
lint: preflight ## Run tsc typecheck (library has no eslint config)
	$(NPX) tsc --noEmit

.PHONY: lint-fix
lint-fix: lint ## No autofix for tsc; delegate to lint
	@echo "gui: lint-fix same as lint for this project"

.PHONY: dev.start
dev.start: preflight ## Run ladle component preview on port 61002
	$(PM) run dev

.PHONY: dev.stop
dev.stop: ## No-op (dev.start runs in foreground)
	@echo "gui: dev.start runs in foreground; nothing to stop"

.PHONY: clean
clean: ## Remove build artifacts (dist/)
	rm -rf dist .next build

.PHONY: help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*?## "} \
		/^[a-zA-Z0-9_.\-]+:.*?## / { printf "  %-20s %s\n", $$1, $$2 }' \
		$(MAKEFILE_LIST) | sort
