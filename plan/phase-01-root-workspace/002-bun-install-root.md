---
task: root-workspace/bun-install-root
phase: 1
number: "002"
title: Run bun install at workspace root
status: todo
tier: sonnet-low
depends_on: [root-workspace/remove-pnpm-artifacts]
---

# Run bun install at workspace root

## Purpose and scope

Run `bun install` at the workspace root to generate `bun.lock` and populate `node_modules/` for all workspace packages (`gui/`). This creates the bun lockfile that all sub-projects will use.

## Requirements

1. **Run `bun install`** from the repo root (`/Users/zane/playground/moduleforge/users-module`):
   ```bash
   bun install
   ```
   This installs dependencies for the root and all workspace packages listed in `"workspaces"`.

2. **`example/` is NOT in the bun workspace** (it was not in pnpm-workspace.yaml either). Its deps are installed separately via `bun install` within `example/` (handled in Phase 3).

3. **Add `bun.lock` to git and commit** with message: `chore(bun-migration): generate bun.lock`

4. **Add `node_modules/` to `.gitignore`** if not already present (confirm it is already ignored).

## Validation

- `bun.lock` exists at the repo root
- `node_modules/` exists and is populated
- `gui/node_modules/` is symlinked or populated by the workspace install
- `bun install` exits with code 0
- `bun.lock` is tracked in git (not gitignored)
- `pnpm-lock.yaml` is absent (confirmed from prior task)
