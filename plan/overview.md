---
plan: bun-migration
created: 2026.06.21
status: open
---

# Bun Migration — users-module

## Purpose and scope

Migrate the users-module monorepo from pnpm+npm to bun as the unified package manager and JavaScript runtime. This covers all JS/TS sub-projects: the `gui/` component library and the `example/` Next.js app. The `model/` and `api/` sub-projects are Go and are out of scope.

**In scope:**
- Root workspace: remove pnpm lock/workspace files; install bun workspace; run `bun install`
- `gui/`: update `package.json` scripts (`npm run` → `bun run`, `npx` → `bunx`); update Makefile PM detection; replace `package-lock.json` with `bun.lock`
- `example/`: update Makefile PM detection; replace `package-lock.json` with `bun.lock`
- Root Makefile: no changes needed (delegates to sub-project Makefiles)
- Verification: full build pipeline passes after migration

**Out of scope:**
- `model/` and `api/` (Go sub-projects)
- Docker Compose / deployment configuration
- CI/CD pipeline changes (flagged for follow-up)

## Current status

Phase 1 (root workspace migration) is the critical prerequisite — all other phases depend on it. Phases 2 and 3 can proceed in parallel once Phase 1 is complete.

## Overview

| Phase | Title | Tasks | Goal |
|-------|-------|-------|------|
| 1 | Root workspace migration | 2 | Remove pnpm, install bun workspace |
| 2 | Migrate gui/ | 2 | Update scripts + Makefile, install with bun |
| 3 | Migrate example/ | 2 | Update Makefile, install with bun |
| 4 | Validate full build | 1 | Confirm preflight + build + dev pass end-to-end |

**Critical path:** Phase 1 → (Phase 2 ∥ Phase 3) → Phase 4

**Parallel-eligible:** Tasks 2.1 and 3.1 can run concurrently after Phase 1 completes.
