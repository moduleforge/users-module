---
task: validate/full-build-validation
phase: 4
number: "001"
title: Run make preflight + make build.gui + make build.example end-to-end
status: todo
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
