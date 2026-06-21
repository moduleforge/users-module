---
created: 2026.06.21
creators:
  - project-flow-check skill
notes: Session-binding manifest produced by `project-flow-check`. Regenerated on each run.
---

# Flow Skill Binding

## Project type

- language: typescript / javascript (bun workspace monorepo)
- framework: react (gui/), next.js (example/) — detected in sub-packages only
- runtime: bun >=1.0 (engines field); bun workspace declared in package.json
- additional markers: Makefile present, bun.lock present, sub-projects: model (Go), api (Go), gui (TypeScript/React), example (Next.js)

## Build / test / run commands

| purpose | command | source |
|---------|---------|--------|
| build | `make build` | Makefile (delegates to sub-projects) |
| build.gui | `make build.gui` | Makefile |
| build.example | `make build.example` | Makefile |
| test | `make test` | Makefile |
| dev | `make dev.start` | Makefile |
| preflight | `make preflight` | Makefile |

## Layout conformance

| dimension | score | notes |
|-----------|-------|-------|
| standard doc set | absent | no README.md, no AGENTS.md at root |
| docs/ discoverability | n/a | docs/ has only oidc-troubleshooting.md (no spec file) |
| plan/ shape | n/a | no plan/ directory |
| make-layout | present | build, test, run (dev.start) targets present |

## Bound skill chain

- role docs: `references/role/developer-node.md`, `references/role/developer-go.md`
- doc-author skills: `write-readme`, `write-agents-md`, `write-project-spec`, `write-architecture`
- implementation skills: `implement-task` (via `dispatch-implementation-task`)
- review skills: `review-changes-correctness`, `review-changes-style`, `review-changes-security`, `review-changes-efficiency`
- release skills: `package-release`, `publish-release`, `coordinate-release`
- deploy / sunset / archive skills: none detected

## Link-chain status

- root: none (README.md absent)
- first-layer docs: n/a
- depth: 0
- orphans: docs/oidc-troubleshooting.md, next-steps.md (unreachable — no README root)

## Open binding gaps

- README.md absent — the link-chain root is missing; all docs are orphans.
- AGENTS.md absent — task agents lack documented build/test/run commands.
- docs/oidc-troubleshooting.md and next-steps.md are link-chain orphans.

## Active plans

| Slug | Branch | Worktree | Status |
|------|--------|----------|--------|
| bun-migration | plan/bun-migration | /Users/zane/playground/moduleforge/users-module/worktree/plan/bun-migration | healthy |
