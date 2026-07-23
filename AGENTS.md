# Agent Rules

All rules in `.cursor/rules/` are **mandatory** for every implementation. Cursor loads them automatically.

## Always apply

| Rule | Purpose |
|------|---------|
| `planning.mdc` | Inspect repo, plan before coding, feature workflow |
| `architecture.mdc` | Clean Architecture, SOLID, package-first, minimal helpers |
| `review.mdc` | Production-ready quality gates, pre-completion checklist |
| `git.mdc` | Branching, Conventional Commits, push/PR policy |
| `testing.mdc` | Test requirements, validation commands, fix-until-green |
| `graphify.mdc` | Knowledge graph context for architecture questions |

## Stack-specific (activate by file path)

| Rule | Globs | Purpose |
|------|-------|---------|
| `kmp.mdc` | `apps/mobile/**` | KMP, Compose, MVVM, StateFlow |
| `backend.mdc` | `services/backend/**` | Modular Monolith, apigen, Go layers |
| `nextjs.mdc` | `apps/web/**`, `apps/pallet-ross/**` | App Router, TypeScript, Server Components |

## Repository layout

```text
apps/web/          InfluAudit dashboard (Next.js 16)
apps/pallet-ross/  Unrelated art-marketplace site, preserved
apps/mobile/       KMP influencer app (phase 3)
services/backend/  Go modular monolith
services/ml/       Python FastAPI fraud/ML service
packages/          contracts (OpenAPI + generated types), config, scripts
deploy/            docker-compose, migrations, k8s
product/           PRDs
```

Each top-level directory is designed to be extracted into its own repository
with no rewiring.

## Workflow summary

1. Inspect the repository and read existing architecture.
2. Produce a short implementation plan.
3. Extend existing packages — never duplicate.
4. Implement following layer boundaries.
5. Write tests for every layer touched.
6. Run validation until green.
7. Update documentation when APIs or architecture change.
