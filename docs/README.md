# AgentMesh Documentation

This page is the documentation index. Use the root README for the project overview and setup; use this directory for architecture and runtime contracts.

## Start here

| Document | Purpose |
|---|---|
| [Project overview (English)](../README.md) | Product summary, requirements and quick start |
| [项目说明（简体中文）](../README.zh-CN.md) | 中文项目概览、环境要求与快速开始 |
| [P2 Unified Execution](p2-execution.md) | Execution/Attempt model, Runtime adapters, HTTP API, cancellation and recovery |
| [P3 Agent Collaboration](p3-collaboration.md) | Workspace, Agent Identity, Channels, Tasks, Inbox, Handoff, Decision Gate and approvals |\n| [P4 Unified Execution Core](p4-unified-execution-core.md) | Cross-runtime Task, Execution, Attempt, Event, Evidence, Artifact, lifecycle and recovery contract |

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
- Verification entry point: bash scripts/verify-p3.sh
- Database migrations: 000024_collaboration_core and 000025_collaboration_governance
