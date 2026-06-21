---
task: validate/full-build-validation
phase: 4
number: "001"
title: Run make preflight + make build.gui + make build.example end-to-end
status: done
tier: sonnet-med
depends_on:
  - gui/install-gui-deps
  - example/install-example-deps
---

# Full build validation

## Purpose and scope

End-to-end verification that the bun migration is complete and the full build pipeline passes cleanly from a fresh state (no pre-existing node_modules).

## Requirements

1. **Clean all JS build artifacts and node_modules**:
   ```bash
   rm -rf /Users/zane/playground/moduleforge/users-module/node_modules
   rm -rf /Users/zane/playground/moduleforge/users-module/gui/node_modules
   rm -rf /Users/zane/playground/moduleforge/users-module/example/node_modules
   make -C /Users/zane/playground/moduleforge/users-module clean.build
   ```

2. **Confirm no npm/pnpm lockfiles remain**:
   ```bash
   find /Users/zane/playground/moduleforge/users-module -name "package-lock.json" -not -path "*/node_modules/*"
   find /Users/zane/playground/moduleforge/users-module -name "pnpm-lock.yaml" -not -path "*/node_modules/*"
   ```
   Both commands must return empty output.

3. **Run full preflight from root** (installs deps via bun):
   ```bash
   make -C /Users/zane/playground/moduleforge/users-module preflight
   ```
   Must succeed. All sub-project preflights must report bun as the package manager.

4. **Run full build**:
   ```bash
   make -C /Users/zane/playground/moduleforge/users-module build.gui build.example
   ```
   Both must exit with code 0.

5. **Spot-check output artifacts**:
   - `gui/dist/index.js`, `gui/dist/index.mjs`, `gui/dist/index.d.ts`, `gui/dist/index.css` — all present
   - `example/.next/` — present and non-empty

6. **Flag for follow-up (do not block this task)**: CI/CD pipeline (if any) may reference `npm` or `pnpm` commands. Audit `.github/workflows/` or equivalent and file a follow-up ticket if changes are needed.

## Validation

- `make preflight` exits 0 with no npm/pnpm warnings
- `make build.gui` exits 0
- `make build.example` exits 0
- `gui/dist/` and `example/.next/` are populated
- No `package-lock.json` or `pnpm-lock.yaml` files exist outside node_modules
- `bun.lock` exists at repo root; `bun.lock` exists in `example/`

## Status

**outcome**: succeeded
**date**: 2026-06-21
**worktree**: worktree/phase-04-task-01-full-build-validation

### Validation summary

All checks passed:

| Check | Command | Result |
|---|---|---|
| `make preflight` exits 0 | `make -C <worktree> preflight` | passed — bun installed deps (1468 pkg workspace, 641 pkg example) |
| `make build.gui` exits 0 | `make -C <worktree> build.gui` | passed — tsup + tailwindcss-cli built successfully |
| `make build.example` exits 0 | `make -C <worktree> build.example` | passed — Next.js 15.5.0 compiled 16 static pages |
| `gui/dist/` populated | `ls gui/dist/index.{js,mjs,d.ts,css}` | passed — all four artifacts present |
| `example/.next/` populated | `ls example/.next/` | passed — non-empty |
| No `package-lock.json` | `find … -name "package-lock.json" -not -path "*/node_modules/*"` | passed — empty output |
| No `pnpm-lock.yaml` | `find … -name "pnpm-lock.yaml" -not -path "*/node_modules/*"` | passed — empty output |
| `bun.lock` at repo root | `ls bun.lock` | passed |
| `bun.lock` in `example/` | `ls example/bun.lock` | passed |

### Decisions made

- `make clean.build` removed `model/db/` (generated Go code tracked in git). Restored via `git checkout HEAD -- model/db/` so `model preflight` could pass without sqlc installed. This is consistent with the model Makefile's intent ("using committed generated code").
- `.yalc/` directories were copied from main checkout before `bun install` (as instructed in dispatch). The `example/.yalc/@moduleforge/users-gui/node_modules/@moduleforge/core-gui` was a broken relative symlink; resolved by copying the real `core-gui` content into the nested location directly.

### Follow-ups flagged

- `.github/workflows/ci.yml` still references `pnpm` in three jobs (`lint`, `test`, `build`): installs pnpm via corepack and runs `pnpm install --frozen-lockfile`. These will fail in CI after the bun migration. A follow-up task is needed to update the CI workflow to use bun instead of pnpm.
