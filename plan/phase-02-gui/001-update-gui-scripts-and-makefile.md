---
task: gui/update-gui-scripts-and-makefile
phase: 2
number: "001"
title: Update gui/package.json scripts and gui/Makefile for bun
status: todo
tier: sonnet-low
depends_on: [root-workspace/bun-install-root]
---

# Update gui/package.json scripts and gui/Makefile for bun

## Purpose and scope

Update `gui/package.json` to replace `npm run` and `npx` invocations with their bun equivalents, and update `gui/Makefile` to detect and use bun instead of pnpm/npm.

## Requirements

### `gui/package.json` changes

1. **`scripts.build`**: Change `"tsup && npm run build:css"` → `"tsup && bun run build:css"`
2. **`scripts.build:css`**: Change `"npx @tailwindcss/cli ..."` → `"bunx @tailwindcss/cli ..."` (or keep `npx` — bun supports `bunx` as a drop-in for `npx`, prefer `bunx`)

Final scripts block:
```json
"scripts": {
  "build": "tsup && bun run build:css",
  "build:css": "bunx @tailwindcss/cli -i src/styles.css -o dist/index.css --minify",
  "typecheck": "tsc --noEmit",
  "clean": "rm -rf dist build .yalc yalc.lock",
  "dev": "ladle serve --port 61002",
  "preview:build": "ladle build"
}
```

### `gui/Makefile` changes

The Makefile currently detects pnpm then npm. Replace the package-manager detection and workspace-root logic with bun-native equivalents:

1. **Replace PM detection** (lines ~12–13):
   ```makefile
   # Before:
   PM := $(shell if command -v pnpm >/dev/null 2>&1; then echo pnpm; else echo npm; fi)
   NPX := npx

   # After:
   PM := bun
   NPX := bunx
   ```

2. **Replace workspace root resolution** (currently looks for `pnpm-workspace.yaml`):
   ```makefile
   # Before: looks for pnpm-workspace.yaml
   WORKSPACE_ROOT := $(shell d=$$PWD; while [ "$$d" != "/" ]; do \
     if [ -f "$$d/pnpm-workspace.yaml" ]; then echo "$$d"; break; fi; \
     d=$$(dirname "$$d"); done)

   # After: looks for bun.lock (workspace root marker)
   WORKSPACE_ROOT := $(shell d=$$PWD; while [ "$$d" != "/" ]; do \
     if [ -f "$$d/bun.lock" ]; then echo "$$d"; break; fi; \
     d=$$(dirname "$$d"); done)
   ```

3. **Update preflight node check** — add a bun check alongside the node check:
   ```makefile
   @if ! command -v bun >/dev/null 2>&1; then \
     echo "FAIL"; \
     echo "ERROR: bun is not installed. See https://bun.sh"; \
     exit 1; \
   fi
   ```
   Remove the `$(PM)` (pnpm/npm) check since PM is now hardcoded to `bun`.

4. **PLATFORM_STAMP path** — already derives from `$(WORKSPACE_ROOT)/node_modules/.platform`; no change needed.

5. **Install command in preflight** — `cd $(WORKSPACE_ROOT) && $(PM) install` becomes `cd $(WORKSPACE_ROOT) && bun install` (automatically correct since PM=bun).

6. **`$(PM) run build`** in the `build` target — becomes `bun run build` (correct via PM variable).

## Validation

- `gui/package.json` `scripts.build` contains `bun run build:css` (not `npm run`)
- `gui/package.json` `scripts.build:css` uses `bunx` (not `npx`)
- `gui/Makefile` `PM` is set to `bun`
- `gui/Makefile` `NPX` is set to `bunx`
- `gui/Makefile` WORKSPACE_ROOT resolution looks for `bun.lock`
- `gui/Makefile` preflight checks for `bun`, not `pnpm`/`npm`
- Commit all changes with message: `chore(bun-migration): update gui/ scripts and Makefile for bun`
