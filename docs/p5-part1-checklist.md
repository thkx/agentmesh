# P5 Part 1 Delivery Checklist

Branch: `p5/control-plane`  
Base: `p4/unified-execution-core`  
Architecture: [P5 Product Control Plane](p5-product-control-plane.md)

This checklist is the implementation gate for M49–M52. A milestone is not complete merely because its page or route exists; its service integration, security behavior, tests, and acceptance checks must also be complete.

## M49 — Dashboard foundation

- [ ] Add embedded Dashboard templates and static assets.
- [ ] Add shared layout, responsive navigation, overview page and empty/error states.
- [ ] Add Dashboard handler and register it through existing router dependencies.
- [ ] Preserve existing API and Workflow Editor authentication behavior.
- [ ] Implement browser login/logout for configured API Token without exposing token in URLs or cookies.
- [ ] Keep Dashboard browser session scoped to Dashboard routes.
- [ ] Validate Origin/Host on state-changing requests; use POST for mutations.
- [ ] Add tests for routing, missing optional dependencies, auth, cookie expiry/signature, CSRF boundary, escaping and asset serving.
- [ ] Add a smoke test that renders the Dashboard without external network access.

## M50 — Provider, Model and Credential Console

- [ ] Render providers and static/dynamic models from existing registries/catalog services.
- [ ] Render credential presence/status only; never render or return secret values.
- [ ] Implement supported provider/model create, update, enable/disable and delete forms.
- [ ] Implement provider connectivity test using existing behavior.
- [ ] Match existing API validation, conflict, persistence and runtime publication semantics.
- [ ] Cover static/dynamic model de-duplication and safe error presentation.
- [ ] Test secret redaction in every page, fragment, validation error and log path.

## M51 — Agent and Workflow Console

- [ ] Render Agent Definitions and their supported runtime binding/capability metadata.
- [ ] Implement only operations supported by existing Agent Definition/Runtime services.
- [ ] Render Workflow definitions, versions and authoring state.
- [ ] Implement supported create/apply/delete/version operations through existing authoring service.
- [ ] Launch Agent/Workflow work through existing runtime entry points.
- [ ] Link launched work to canonical Execution detail when an Execution ID is returned.
- [ ] Test validation, policy/approval boundaries, and error-to-HTML behavior.

## M52 — Unified Execution Center

- [ ] List canonical Executions across Agent, Coding, Workflow and CLI.
- [ ] Add bounded pagination and status/runtime/task filters.
- [ ] Render P4 detail read model, attempts, artifacts and evidence.
- [ ] Render P4 merged lifecycle/control timeline with the documented cursor semantics.
- [ ] Add cancel action through Execution Manager with explicit confirmation.
- [ ] Add reconciliation/recovery action only if the existing manager exposes a safe operation for the selected state.
- [ ] Do not offer blind replay for unknown external outcomes.
- [ ] Test four runtime kinds, pagination, filters, timeline cursor, provenance and invalid state controls.

## Final Part 1 gate

- [ ] No new queue, scheduler, execution state machine, event ledger or duplicate persistence model.
- [ ] No new migration unless explicitly justified and reviewed.
- [ ] `gofmt` check passes without auto-formatting during the verification script.
- [ ] `go build ./...` passes.
- [ ] `go vet ./...` passes.
- [ ] `go test -race ./...` passes.
- [ ] `bash scripts/verify-p5-part1.sh` passes.
- [ ] Existing P2/P3/P4 verification scripts remain compatible.
- [ ] Documentation and root README point to the Dashboard usage and security guidance.
- [ ] Final report records exact commit SHA and exact local/CI verification evidence.
