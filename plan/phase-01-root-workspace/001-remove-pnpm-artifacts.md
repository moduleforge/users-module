---
task: root-workspace/remove-pnpm-artifacts
phase: 1
number: "001"
title: Remove pnpm artifacts and configure bun workspace
status: todo
tier: sonnet-low
depends_on: []
---

# Remove pnpm artifacts and configure bun workspace

## Purpose and scope

Remove all pnpm-specific files from the repository root and replace them with bun workspace configuration. This is the prerequisite for all subsequent phases.

## Requirements

1. **Delete pnpm artifacts:**
   - `pnpm-lock.yaml` — pnpm lockfile; bun will generate `bun.lock` on first install
   - `pnpm-workspace.yaml` — pnpm workspace config; replaced by bun workspace

2. **Update `package.json` at the repo root:**
   - Remove `"packageManager": "pnpm@10.33.0"` field
   - Change `"engines": { "node": ">=20" }` to `"engines": { "bun": ">=1.0" }`
   - Add `"workspaces": ["gui"]` to declare the bun workspace (mirrors pnpm-workspace.yaml's `packages: [gui]`)

3. **No changes needed to `node_modules/`** — that directory will be regenerated in task 002 after this task completes.

4. **Commit all changes** with message: `chore(bun-migration): remove pnpm artifacts, configure bun workspace`

## Validation

- `pnpm-lock.yaml` does not exist at the repo root
- `pnpm-workspace.yaml` does not exist at the repo root
- `package.json` has no `packageManager` field containing "pnpm"
- `package.json` has `"workspaces": ["gui"]`
- `package.json` has `"engines": { "bun": ">=1.0" }` (or similar bun engine specifier)
- `git status` shows the deleted files and the modified `package.json`
