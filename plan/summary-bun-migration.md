---
plan: bun-migration
created: 2026-06-21
status: complete
---

# Bun Migration — Session Summary

## What was planned and why

**Goal:** Migrate the users-module monorepo from pnpm+npm to bun as the unified package manager and JavaScript runtime.

**Motivation:** Consolidate on a single, faster toolchain. pnpm was used at the workspace root and npm was used within sub-projects, resulting in mixed lockfiles and inconsistent `package-lock.json` / `pnpm-lock.yaml` files across the repository.

**Scope (in):**
- Root workspace: remove pnpm lockfile and workspace config; configure bun workspace; run `bun install`
- `gui/`: update `package.json` scripts (`npm run` / `npx` → `bun run` / `bunx`); update Makefile PM detection and workspace-root resolution; replace `package-lock.json` with `bun.lock`
- `example/`: update Makefile PM detection; replace `package-lock.json` with standalone `bun.lock`
- Verification: full build pipeline (preflight + build.gui + build.example) passes end-to-end

**Scope (out):** `model/` and `api/` (Go sub-projects); Docker Compose / deployment config; CI/CD pipeline changes (flagged as follow-up).

**Execution order:** Phase 1 (root workspace) was the critical prerequisite. Phases 2 and 3 were parallel-eligible once Phase 1 completed. Phase 4 validated the combined result.

---

## What shipped

### Phase 1 — Root workspace migration

**Task 1.1 — Remove pnpm artifacts and configure bun workspace** (merge `244ecdc`)
- Deleted `pnpm-lock.yaml` and `pnpm-workspace.yaml` from repo root.
- Updated `package.json`: removed `"packageManager": "pnpm@10.33.0"`, changed `"engines"` to `{ "bun": ">=1.0" }`, added `"workspaces": ["gui"]`.
- All 6 validation checks passed.

**Task 1.2 — Run bun install at workspace root** (merge `0e53da3`)
- Ran `bun install` at repo root; generated `bun.lock` (234 KB), populated `node_modules/` (1464 packages) including `gui/node_modules/` via workspace resolution.
- All 6 validation checks passed.

### Phase 2 — Migrate gui/

**Task 2.1 — Update gui/package.json scripts and gui/Makefile for bun** (merge `996136e`)
- `gui/package.json` `scripts.build` changed to use `bun run build:css`; `scripts.build:css` changed to use `bunx @tailwindcss/cli`.
- `gui/Makefile`: `PM := bun`, `NPX := bunx`, workspace-root walk now looks for `bun.lock` instead of `pnpm-workspace.yaml`, preflight checks for `bun`.
- All 6 validation checks passed.

**Task 2.2 — Verify gui/ installs cleanly with bun** (merge `04617ce`)
- Deleted `gui/package-lock.json`.
- `make build.gui` exited 0; `gui/dist/` populated with `index.js`, `index.mjs`, `index.d.ts`, `index.d.mts`, `index.css` (+ sourcemaps).
- Added `@types/node@^22` to `gui/package.json` devDependencies (required to fix a DTS build failure; see Key Decisions).

### Phase 3 — Migrate example/

**Task 3.1 — Update example/Makefile for bun** (merge `c320193`)
- `example/Makefile`: `PM := bun`, `NPX := bunx`, `WORKSPACE_ROOT := .` (simplified; no workspace-root walk needed since example/ is standalone).
- All 3 validation checks passed.

**Task 3.2 — Verify example/ installs cleanly with bun** (merge `53a6056`)
- Deleted `example/package-lock.json`; ran `bun install` inside `example/` (641 packages); generated `example/bun.lock` (tracked in git).
- `make build.example` exited 0; Next.js 15.5.0 compiled 16 static pages; `example/.next/` populated.
- All 6 validation checks passed.

### Phase 4 — Validate full build

**Task 4.1 — End-to-end build validation** (no merge SHA recorded)
- Cleaned all `node_modules/` and build artifacts from a fresh state.
- `make preflight` exited 0; bun installed 1468 packages (workspace) and 641 packages (example).
- `make build.gui` and `make build.example` both exited 0.
- All 9 validation checks passed: no `package-lock.json` or `pnpm-lock.yaml` found outside `node_modules/`; `bun.lock` present at repo root and in `example/`.

---

## Key decisions

1. **Workspace membership: gui/ in, example/ out.** `gui/` is declared in root `"workspaces"` (mirroring the old pnpm-workspace.yaml). `example/` remains standalone and manages its own `bun.lock`. This preserves the prior topological boundary.

2. **Workspace-root marker changed from `pnpm-workspace.yaml` to `bun.lock`.** Both `gui/Makefile` and (initially) `example/Makefile` used a directory walk to find the workspace root. For `example/` the walk was eliminated entirely in favor of `WORKSPACE_ROOT := .` since the sub-project is not workspace-managed.

3. **`@types/node@^22` added to gui/ devDependencies.** The `process` global used in `gui/src/lib/api.ts:366` caused a TypeScript DTS build failure (`TS2580: Cannot find name 'process'`) because `tsconfig.json` only includes `dom` and `esnext` libs. Adding node type definitions is the minimal correct fix; the `typeof process !== 'undefined'` runtime guard was already correct.

4. **`make clean.build` removes generated Go code (`model/db/`).** During Phase 4 validation, `make clean.build` wiped `model/db/` (generated Go, tracked in git). It was restored via `git checkout HEAD -- model/db/` so `model preflight` could pass without sqlc installed. This behavior is consistent with the model Makefile's intent but is a footgun for clean builds.

5. **`.yalc/` directories require manual setup in worktrees.** Both `gui/` and `example/` have `file:.yalc/...` dependencies on local `@moduleforge/` packages. The `.yalc/` directories are gitignored. During all phases these were copied from the main checkout into worktrees before running `bun install`. This is not automated.

6. **Broken nested symlink in example/.yalc/ resolved by content copy.** `example/.yalc/@moduleforge/users-gui/node_modules/@moduleforge/core-gui` was a broken relative symlink. Resolved during Phase 4 by copying real `core-gui` content into the nested location directly rather than chasing the symlink target.

---

## Follow-up items

1. **[BLOCKER] CI workflow still uses pnpm** (next-steps id: `r86L`)
   `.github/workflows/ci.yml` references `pnpm` in three jobs (`lint`, `test`, `build`) — installs pnpm via corepack and runs `pnpm install --frozen-lockfile`. These jobs will fail in CI after the bun migration. A follow-up task is needed to replace pnpm with bun throughout the CI workflow.

2. **yalc dep in gui/package.json** (next-steps id: `3RgF`)
   `gui/package.json` declares `@moduleforge/core-gui` via `"file:.yalc/@moduleforge/core-gui"` — a gitignored local-link. Any fresh worktree or CI environment will fail `bun install` without `.yalc/` present. Consider removing the `file:` dep from `dependencies` (keep as optional peer) or documenting the yalc setup step in the developer setup guide.

3. **Next.js dual-lockfile warning** (next-steps id: `QLVr`)
   Next.js warns about multiple lockfiles (`bun.lock` at repo root and `example/bun.lock`). Build succeeds. To silence: set `outputFileTracingRoot` in `example/next.config.ts`.

4. **example/Makefile: redundant rm path** (next-steps id: `0Qv6`)
   Lines 43-45 expand `rm -rf $(WORKSPACE_ROOT)/node_modules node_modules` to the same path twice (`WORKSPACE_ROOT=.`). Harmless but noisy. Simplify to `rm -rf node_modules`.

5. **example/Makefile: install idiom inconsistency** (next-steps id: `bRL4`)
   `example/Makefile` uses bare `bun install` while `gui/Makefile` uses `$(PM) install`. Functionally identical (`PM=bun`) but inconsistent. Consider aligning to `$(PM) install` and adding the `https://bun.sh` URL to the preflight error message.
