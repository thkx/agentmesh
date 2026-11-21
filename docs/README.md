# AgentMesh Documentation

This page is the documentation index. Use the root README for the project overview and setup; use this directory for architecture and runtime contracts.

## Start here

| Document | Purpose |
|---|---|
| [Project overview (English)](../README.md) | Product summary, requirements and quick start |
| [项目说明（简体中文）](../README.zh-CN.md) | 中文项目概览、环境要求与快速开始 |
| [P2 Unified Execution](p2-execution.md) | Execution/Attempt model, Runtime adapters, HTTP API, cancellation and recovery |
| [P3 Agent Collaboration](p3-collaboration.md) | Workspace, Agent Identity, Channels, Tasks, Inbox, Handoff, Decision Gate and approvals |
| [P4 Unified Execution Core](p4-unified-execution-core.md) | Cross-runtime Task, Execution, Attempt, Event, Evidence, Artifact, lifecycle and recovery contract |
| [P4 M42 Audit](p4-m42-audit.md) | Source-level mapping of existing P2/P3 contracts and M43/M44 implementation gaps |
| [P4 Part 2 Implementation](p4-part2-implementation.md) | M45/M46 lifecycle enforcement, cancellation intent and safe recovery |
| [P4 Part 3 Implementation](p4-part3-implementation.md) | M47/M48 unified execution detail, merged history timeline and release acceptance |
| [P5 Product Control Plane](p5-product-control-plane.md) | M49–M52 Dashboard architecture, security boundary and acceptance contract |
| [P5 Part 1 Checklist](p5-part1-checklist.md) | M49–M52 implementation tasks and final acceptance gates |
| [P5 Part 1 Implementation](p5-part1-implementation.md) | Dashboard implementation record and exact verification gates |
| [P5 Part 2 Operations & Observability](p5-part2-operations.md) | M53–M56 web asset boundary, telemetry, usage/cost, backup/restore, upgrades and diagnostics |
| [P5 M53 Implementation Status](p5-m53-implementation.md) | Current M53 implementation record, limitations and verification command |
| [P6 Coding Runtime Implementation](p6-coding-runtime-audit.md) | M57–M60 source audit and implementation record, migrations, provenance and verification state |
| [P6 Coding Runtime Architecture](p6-coding-runtime.md) | Product outcome, architecture, milestone scope, security requirements and live acceptance instructions |

## Architecture and implementation history

### M1–M16: foundational runtime

The milestone design and implementation documents live in [milestones/](milestones/). They describe the original provider/model gateway, Agent and Tool runtimes, scheduler, Workflow runtime, Goal planning, reflection and memory work.

### M17–M41: subsequent implementation records

The following files remain at the repository root to preserve existing links. They are historical implementation notes, not the current project landing page:

- [M17–M22: AI Resource Pool and Gateway](../M17-M22-STATUS.md)
- [M23–M28: Agent Runtime and Tool System](../M23-M28-STATUS.md)
- [M29–M34: Context and Workflow Authoring](../M29-M34-STATUS.md)
- [M35–M41: Coding Agent execution](../M35-M41-STATUS.md)
- [M35–M41 file manifest](../M35-M41-FILES.md)

### Historical patch/package notes

- [P0/P1 implementation notes](../README-P0-P1.md): retained for historical context; not a current installation guide or a replacement for the repository.

| [P7 First-Run Product Experience](p7-product-onboarding.md) | M61–M65 guided onboarding, model setup, Agent quickstart and Workflow templates |

## P2: current execution contract

P2 is the common execution management layer above the existing Agent Scheduler, Coding Service, Workflow queue and CLI executor.

- P2 design, lifecycle and API: [p2-execution.md](p2-execution.md)
- Verification entry point: `bash scripts/verify-p2.sh`
- Database migration: `internal/platform/database/migrations/000023_unified_executions.*.sql`

## Which source should be trusted?

When documents and implementation appear inconsistent, use this order:

1. Current Go code, HTTP route registration and tests.
2. Current SQL migrations and configuration schema/examples.
3. Current verification scripts and completed CI runs for the exact commit.
4. Milestone notes, which explain historical intent and implementation decisions.

Historical validation sections are snapshots. They must not be interpreted as evidence that the current branch has or has not passed its checks. For current P2 CI status, see the [Go CI workflow runs for the P2 implementation branch](https://github.com/thkx/agentmesh/actions/workflows/go.yml?query=branch%3Ap2%2Fimplementation) and verify the run's commit SHA.

## P3: Agent Collaboration Plane

- P3 collaboration contract: [p3-collaboration.md](p3-collaboration.md)
- Verification entry point: `bash scripts/verify-p3.sh`
- Database migrations: 000024_collaboration_core and 000025_collaboration_governance

## P4 Part 2

- Implementation record: [p4-part2-implementation.md](p4-part2-implementation.md)
- Verification entry point: `bash scripts/verify-p4-part2.sh`
- Migration: `internal/platform/database/migrations/000043_execution_lifecycle_enforcement.*.sql`

## P4 Part 3

- Implementation record: [p4-part3-implementation.md](p4-part3-implementation.md)
- Verification entry point: `bash scripts/verify-p4-part3.sh`
- Additive HTTP read APIs: `/api/v1/managed-executions/:id/detail` and `/api/v1/managed-executions/:id/timeline`

## P5

- Part 1 architecture: [p5-product-control-plane.md](p5-product-control-plane.md)
- Part 1 implementation record: [p5-part1-implementation.md](p5-part1-implementation.md)
- Part 1 delivery checklist: [p5-part1-checklist.md](p5-part1-checklist.md)
- Part 1 verification entry point: `bash scripts/verify-p5-part1.sh`
- Part 2 architecture and delivery plan: [p5-part2-operations.md](p5-part2-operations.md)
- M53 implementation status: [p5-m53-implementation.md](p5-m53-implementation.md)
