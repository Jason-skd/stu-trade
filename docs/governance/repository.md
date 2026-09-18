# Repository Governance Supplement

This file contains only StuTrade-specific ownership, integration, validation,
and delivery rules. The common baseline governs general repository work.

## Repository Actions

- **R-A002** Perform cross-repository changes as an ownership handoff. Update
  engine or web in its owning repository first; after that revision is merged,
  update this repository's gitlink, derived artifacts, and integration checks.
- **R-A003** Run `make pr-check` as the complete repository verification with
  `STUTRADE_TEST_DATABASE_URL` pointing to a dedicated PostgreSQL test
  database. If PostgreSQL is unavailable, run `make check workflow-check` and
  report that database integration coverage was omitted.

## Product and Component Ownership

- **R-G001** This repository's `docs/project-description.md` owns current
  product scope, user-visible transaction semantics, deployment topology, and
  cross-component boundaries. Each repository's
  `docs/implementation_plan.md` owns its unfinished targets and stages without
  proving current behavior.
- **R-G002** The engine repository's exported Go code and behavioral tests own
  generic engine models, entry points, logical persistence protocols,
  restoration rules, errors, and state-machine behavior.
- **R-G003** The web repository's `openapi/stu-trade.yaml` owns `/api/v1`
  paths, transport DTOs, statuses, and error structures. Web maintainers
  propose caller-facing HTTP structures; host maintainers review product
  semantics, security, idempotence, and feasibility before implementation;
  `internal/httpapi/contract.gen.go` is generated from that schema.
- **R-G004** This repository's migrations own the physical PostgreSQL schema,
  constraints, and migration order, including engine-protocol tables and
  host-extension foreign keys, without transferring engine state semantics to
  the host.
- **R-G005** This repository's query files own sqlc query text. Generated
  bindings under `internal/db` are derived from those queries and the
  migrations, and stay inside adapters implementing engine protocols or
  host-owned persistence.
- **R-G006** Web implementation and behavioral tests own pages, interaction,
  frontend routing, accessibility, and frontend release behavior.
- **R-G007** The host owns `/health`, `/ready`, process configuration,
  logging, dependency assembly, and deployment lifecycle; these are not web
  business API operations.

## Integration Revisions

- **R-G008** The parent repository records exact engine and web commits. A
  child merge does not affect product integration until the corresponding
  parent gitlink is updated and verified.

## Product and Delivery Identity

- **R-G009** The Main Gate builds backend and web images from one exact parent
  commit and records their digests in `deployment-manifest`. A production tag
  promotes and deploys those certified digests without rebuilding them.
- **R-G010** Product-facing text uses `StuTrade`; lowercase `stu-trade` is
  reserved for repository names, paths, module identifiers, commands, and
  other technical identifiers.
- **R-G011** The supported assembly profiles are exactly `develop`, `native`,
  and `production`; documentation and automation do not substitute
  `development` for `develop`.
