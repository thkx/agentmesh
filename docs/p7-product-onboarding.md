# P7 — First-Run Product Experience

**Implementation branch:** `p7/product-onboarding`  
**Base:** `p6/coding-runtime`  
**Product outcome:** a first-time user can understand what AgentMesh does, configure a model, create useful work and inspect the result without reading implementation documentation.

P7 is a product usability phase, not a new runtime phase. It must reuse the existing Provider/Model, Agent, Workflow, Coding and Unified Execution services. The Dashboard remains Go `html/template` + HTMX + local CSS/JavaScript under `web/`.

## Product principles

- The Overview page explains the next useful action instead of presenting only database counters.
- Guide users in a sensible sequence, but never block experienced users from navigating directly.
- Prefer safe defaults and short task descriptions over exposing every runtime tuning parameter at first contact.
- Explain technical terms at the point of use; keep advanced configuration available as an expandable or secondary path.
- Show honest readiness: a configured provider is not necessarily a working model; a created Agent is not proof that a real task succeeded.
- Every successful action should lead to its persisted run/execution result.
- Local-first operation, existing authentication boundaries and all P5/P6 runtime ownership rules remain unchanged.

## Milestones

### M61 — Guided first-run Overview

- Replace the empty/counter-only landing experience with a persisted five-step checklist: connect a provider, enable a model, create an Agent, run an Agent task, and create a reusable Workflow.
- Derive completion from existing persisted records; do not add onboarding tables or migrations.
- Link each step to the existing Dashboard page.
- Add responsive styling and regression tests for the empty/first-run state.

**Acceptance:** a clean instance explains where to begin; existing instances show truthful progress; all links navigate to existing routes; no new runtime or schema is introduced.

### M62 — Model setup and connection verification

- Present provider, credential and model setup as one understandable sequence.
- Explain provider type, Base URL, API key storage and model identifier with examples.
- Add an explicit connection/model test result with safe, actionable error categories.
- Never expose raw credentials or upstream secrets in HTML or logs.

### M63 — Agent quickstart

- Add a beginner path to create an Agent using a small number of fields: name, model and task instructions.
- Keep advanced budgets, tools and system prompt controls available without making them the first interaction.
- Provide a simple first-task composer and link the result to Execution detail.
- Distinguish queued, running, completed and failed states with plain-language explanations.

### M64 — Workflow templates and guided run

- Provide a small set of useful, safe starter Workflow templates.
- Preview the steps and required configuration before applying a template.
- Let the user run a saved Workflow and navigate directly to its durable Run detail.
- Keep YAML and graph editing available as advanced authoring paths.

**Implementation in progress:** the Dashboard now has a guided single-Agent starter that requires an enabled model and an existing workspace, previews the Start → Agent → End sequence, and saves a validated durable template. Queueing a run now redirects to its durable Run detail. Automated and local verification are still required before M64 is accepted.

### M65 — Product-wide usability and acceptance

- Normalize empty states, validation messages, destructive-action confirmation and navigation.
- Review mobile layout, keyboard navigation, labels and accessible status text.
- Add an end-to-end first-run acceptance path using deterministic fixtures.
- Verify P5/P6 invariants and the existing full Go/SQLC/race/build/vet checks.

## Non-goals

- No Vue, React, SPA rewrite, Node build pipeline or external CDN.
- No new queue, scheduler, runtime, event ledger or database migration unless a proven UI requirement cannot be met with current services.
- No cloud account, telemetry dependency, multi-user tenancy or public deployment claim.
- No automatic commit, push, PR creation or merge by Coding Runtime.

## Verification policy

A milestone is accepted only when its exact branch commit passes the relevant tests and GitHub Actions. Live model/Docker behavior must be identified separately from deterministic tests. A draft PR or a successful code write is not evidence of acceptance.
