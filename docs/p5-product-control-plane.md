# P5 Part 1 — Product Control Plane (M49–M52)

**Status:** Implementation in progress; see [Part 1 implementation record](p5-part1-implementation.md) and [delivery checklist](p5-part1-checklist.md). Acceptance is not declared until the exact final commit passes verification.  
**Base:** `p4/unified-execution-core` at `46986d56828214e078515ab4dba664ba3ad49273`  
**Implementation branch:** `p5/control-plane`  
**Product decision:** Control Center first; Go `html/template` + HTMX + small amounts of vanilla JavaScript.

## 1. Goal

Turn the existing AgentMesh runtime and execution capabilities into an operable local-first product. A user should be able to configure model resources, define agents and workflows, launch work, inspect execution history, and perform supported control actions without relying on curl.

P5 Part 1 is a presentation and application-integration layer. It is not a rewrite of the runtime.

## 2. Non-goals and invariants

- Do not replace or fork the existing Agent, Coding, Workflow, CLI, Collaboration, or Execution Manager implementations.
- Do not introduce another scheduler, queue, task store, execution state machine, or event ledger.
- Do not add a database migration unless a concrete UI requirement cannot be satisfied by existing application/domain APIs. M49–M52 are expected to be schema-neutral.
- Do not introduce Vue, React, Node build tooling, Redis, or a frontend SPA.
- Preserve Local-first operation: no mandatory cloud account, telemetry service, or external frontend CDN.
- Preserve the existing API and CLI contracts; Dashboard routes are additive.
- Never expose provider secrets, API keys, credential material, raw upstream errors, or sensitive execution inputs in rendered HTML.
- Do not make destructive actions one-click. Require explicit confirmation and use POST for mutations.
- Dashboard actions must call existing application services/handlers, not write directly to runtime tables.

## 3. Architecture

```text
Browser
  |
  | HTML navigation / HTMX partial requests / form POST
  v
Dashboard HTTP surface (Gin)
  |
  +-- html/template page + partial renderer
  +-- view/query adapters (read-only projections)
  +-- command adapters (existing service methods)
  |
  v
Existing Application Services / Registries
  |
  +-- Provider + Model Registry / Credential Manager
  +-- Agent Definition + Agent Runtime / Collaboration
  +-- Workflow Authoring + Workflow Engine
  +-- Execution Manager + existing detail/timeline read model
  |
  v
Existing repositories, SQLite, Runtime adapters and workers
```

### Presentation technology

- Go `html/template` with contextual HTML escaping.
- HTMX pinned and vendored locally under the Dashboard static asset tree; no CDN dependency.
- Vanilla JavaScript only for progressive enhancements that HTMX/HTML cannot express cleanly (for example, confirmation UX and copy-to-clipboard).
- CSS is local and versioned with the application.
- Server-rendered full pages and HTML fragments share the same view models and template components.
- The Dashboard is usable without JavaScript for navigation and basic forms where practical; HTMX enhances partial updates.

### Package and asset layout

Proposed additive layout (final names may follow existing repository conventions, but do not move existing packages):

```text
internal/transport/http/dashboard/
  handler.go
  routes.go
  auth.go
  viewmodels.go
  queries.go
  commands.go
  templates/
    layout.html
    components/
    pages/
    partials/
  static/
    css/
    js/
    vendor/htmx.min.js
web/dashboard/                 # optional design/source assets only if needed
```

Prefer `go:embed` for templates and static assets so a normal Go binary contains the Dashboard. Avoid a separate frontend build step.

### Route namespace

- `GET /dashboard` — overview.
- `GET /dashboard/providers` and `GET /dashboard/models` — resource catalog pages.
- `GET /dashboard/agents` and `GET /dashboard/workflows` — orchestration configuration pages.
- `GET /dashboard/executions` — unified execution list.
- `GET /dashboard/executions/:id` — execution detail and timeline.
- `GET /dashboard/partials/...` — HTML fragments used by HTMX.
- `POST /dashboard/... ` — supported mutations, with validation and confirmation.
- `GET /dashboard/assets/*` — embedded static assets.
- `GET /dashboard/login`, `POST /dashboard/login`, `POST /dashboard/logout` — browser session bootstrap when API Token protection is enabled.

Existing `/api/v1/*`, `/healthz`, `/readyz`, Workflow Editor, CLI, and API clients remain compatible.

## 4. Authentication and security boundary

The existing API Token middleware protects the API. A browser cannot safely attach a Bearer token to a top-level HTML navigation, so the Dashboard needs a browser-specific session mechanism without weakening API authentication.

- When API Token is empty, preserve the current local-first deployment behavior and existing network binding guidance.
- When API Token is configured, Dashboard login accepts the token over HTTPS or loopback only, compares it in constant time, and issues a short-lived, signed, HttpOnly, SameSite=Strict cookie.
- Derive the cookie signing key from the configured token using a domain-separated HMAC; never store the raw token in the cookie.
- Restrict the cookie to Dashboard routes. It must not silently become a general API Bearer credential.
- Mark Secure when TLS is used; reject login over non-loopback plaintext HTTP when token protection is enabled.
- POST routes validate Origin/Host and use SameSite protection; no state-changing GET routes.
- Login/logout are rate-limited and responses do not reveal whether a token prefix or other partial credential was correct.
- Escape all user-controlled text; do not render arbitrary Markdown/HTML as trusted content.
- Do not include secrets in view models, logs, errors, URLs, or HTML attributes.

The implementation must be reviewed against the actual server TLS/proxy configuration before claiming remote access is production-safe. P5 Part 1 does not advertise public-internet exposure as supported.

## 5. M49–M52 scope and acceptance

### M49 — Dashboard foundation

Deliver:
- Embedded templates/static assets and a shared responsive layout.
- Navigation for Overview, Providers & Models, Agents, Workflows, Executions.
- Overview with readiness/dependency status using existing health/readiness sources and a small set of truthful runtime counts; omit metrics that are not currently available.
- Dashboard route registration through existing `NewRouter(Dependencies)`, with optional dependencies handled safely.
- Browser auth behavior described above, integrated without bypassing existing API authentication.
- Handler, template, route, auth, escaping, and responsive smoke tests.

Acceptance:
- `GET /dashboard` renders a complete page.
- Dashboard static assets load from the binary without network access.
- API token protection remains effective for `/api/v1/*`.
- Dashboard cannot mutate state through GET.
- `go test -race ./...`, `go vet ./...`, and `go build ./...` pass.

### M50 — Provider, Model and Credential Console

Deliver:
- Provider and model list/detail views using existing registries and dynamic model catalog.
- Create/update/enable/disable/delete operations only where the existing service/API supports them.
- Provider connectivity test using the existing provider test operation.
- Credential status and safe metadata only; never read back or display secret material.
- Clear distinction between static provider models and dynamic AI Resource models.
- Empty, loading, validation, conflict, and upstream failure states.

Acceptance:
- UI mutations have equivalent validation and side effects to existing API behavior.
- Credential values are write-only and are never included in HTML responses.
- Dynamic and static models are not duplicated.
- Tests cover permissions/auth, invalid input, conflicts, and secret redaction.

### M51 — Agent and Workflow Console

Deliver:
- Agent definition list/detail and supported lifecycle/configuration operations.
- Workflow list/detail and supported authoring/version operations.
- Display runtime binding/capability information that is already exposed by existing services.
- Launch/execute actions only through existing runtime/workflow service entry points.
- Human-readable validation errors and links to resulting executions where available.

Acceptance:
- No new Agent or Workflow persistence model is introduced.
- UI actions call existing services and preserve existing approval/policy boundaries.
- Tests cover listing, detail, validation, and supported mutation/launch paths.

### M52 — Unified Execution Center

Deliver:
- One execution list across Agent, Coding, Workflow, and CLI kinds, based on canonical `executions`.
- Status/runtime/task/time filters and pagination.
- Execution detail reusing P4 detail read model.
- Timeline reusing P4 lifecycle and control event read model.
- Attempt, Artifact Version, and Evidence provenance display.
- Cancel and reconciliation/recovery actions only when supported by the existing Execution Manager and valid for the current state.
- Clear differentiation between cancellation requested, cancellation confirmed, unknown outcome, and terminal result.

Acceptance:
- No duplicate execution state or event store.
- All four runtime kinds appear in the same list/detail contract.
- UI status is derived from canonical execution state.
- Unsupported or unsafe replay is never offered as a generic retry button.
- Tests cover pagination, filters, timeline, provenance, cancellation and invalid-state controls.

## 6. Cross-cutting implementation rules

1. Query/view models are explicit and minimal. Never pass database rows or broad domain objects directly to templates.
2. Command handlers validate input, call existing services, and redirect or return an HTMX fragment with a user-visible result.
3. Use Post/Redirect/Get for full-page forms; HTMX forms return a fragment and appropriate status.
4. Preserve API response contracts; do not convert API endpoints into HTML.
5. Every form has server-side validation. Client validation is only an enhancement.
6. Pagination and filtering must be bounded; do not load unbounded event histories into a page.
7. Time values are rendered in UTC with an explicit label initially; local-time preference is a later enhancement.
8. Every destructive or externally visible operation has a confirmation step and audit/event behavior inherited from the underlying service.
9. Tests use isolated SQLite fixtures and do not depend on external model credentials, Docker, or coding-agent CLIs.
10. The acceptance script must not silently format source before checking it; gofmt validation should fail on unformatted files.

## 7. Delivery sequence

Implementation is split internally into four vertical milestones, but Part 1 is accepted only when all four are integrated:

1. M49 shared Dashboard foundation and secure route integration.
2. M50 Provider/Model/Credential views and commands.
3. M51 Agent/Workflow views and commands.
4. M52 Execution Center, followed by end-to-end acceptance.

Each milestone must include complete source, comments for non-obvious business/security logic, unit/integration tests, and updated documentation. Do not stop after scaffolding or screenshots.

## 8. Part 1 release acceptance

Required command:

```bash
bash scripts/verify-p5-part1.sh
```

The script must check:
- Go formatting (check only; no auto-format masking).
- `go build ./...`
- `go vet ./...`
- `go test -race ./...`
- Dashboard-specific integration tests.
- Existing P2/P3/P4 verification scripts remain runnable and pass where their environment prerequisites are met.

The final report must identify the exact branch and commit SHA, exact commands run, pass/fail output, any skipped checks and why, and whether GitHub Actions completed for that exact commit. A local code write or GitHub commit is not evidence of a successful build/test.

## 9. Deferred beyond P5 Part 1

- System-wide metrics, token/cost accounting, tracing and alerting (P5 Part 2).
- Backup/restore, upgrade workflows, diagnostics and service lifecycle UX (P5 Part 2).
- Fine-grained API tokens, scoped RBAC, token rotation, remote access hardening and public deployment claims (P5 Part 3).
- Multi-machine Runner, multi-user tenancy, cloud control plane and external telemetry.
