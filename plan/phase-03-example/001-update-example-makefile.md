---
task: example/update-example-makefile
phase: 3
number: "001"
title: Update example/Makefile for bun
status: todo
tier: sonnet-low
depends_on: [root-workspace/bun-install-root]
---

# Update example/Makefile for bun

## Purpose and scope

Update `example/Makefile` to use bun instead of pnpm/npm. The `example/` sub-project is NOT in the bun workspace (same as pnpm: only `gui/` was in pnpm-workspace.yaml). Its dependencies are installed independently via a local `bun install`.

## Requirements

### `example/Makefile` changes

The Makefile uses the same PM-detection and workspace-root-resolution pattern as `gui/Makefile`. Apply the same set of changes:

1. **Replace PM detection** (lines ~12–13):
   ```makefile
   # Before:
   PM := $(shell if command -v pnpm >/dev/null 2>&1; then echo pnpm; else echo npm; fi)
   NPX := npx

   # After:
   PM := bun
   NPX := bunx
   ```

2. **Replace workspace root resolution** (looks for `pnpm-workspace.yaml`):
   ```makefile
   # Before:
   WORKSPACE_ROOT := $(shell d=$$PWD; while [ "$$d" != "/" ]; do \
     if [ -f "$$d/pnpm-workspace.yaml" ]; then echo "$$d"; break; fi; \
     d=$$(dirname "$$d"); done)

   # After:
   WORKSPACE_ROOT := $(shell d=$$PWD; while [ "$$d" != "/" ]; do \
     if [ -f "$$d/bun.lock" ]; then echo "$$d"; break; fi; \
     d=$$(dirname "$$d"); done)
   ```
   Note: since `example/` is not in the workspace, `WORKSPACE_ROOT` will resolve to the repo root (where `bun.lock` lives) — but example/ has its own `package-lock.json`. After removing that file and running `bun install` in example/, bun will install locally. The WORKSPACE_ROOT variable is used only for `PLATFORM_STAMP`; for installs the Makefile falls through to `cd $(WORKSPACE_ROOT) && $(PM) install` — but since example/ is not a workspace member, it should install locally. Simplify: set `WORKSPACE_ROOT := .` for example/ or let it fall through correctly.

   **Preferred simplification for example/**: Since example/ is standalone (not a workspace member), set:
   ```makefile
   WORKSPACE_ROOT := .
   PLATFORM_STAMP := node_modules/.platform
   ```
   This avoids the workspace-root walk entirely for example/.

3. **Update preflight** — replace the `$(PM)` check with a `bun` check (same as gui/).

4. **Install command** — `cd $(WORKSPACE_ROOT) && $(PM) install` → `bun install` (from within example/).

## Validation

- `example/Makefile` `PM` is set to `bun`
- `example/Makefile` `NPX` is set to `bunx`
- `example/Makefile` WORKSPACE_ROOT set to `.` (or bun.lock-based walk resolves correctly)
- Commit with message: `chore(bun-migration): update example/Makefile for bun`
