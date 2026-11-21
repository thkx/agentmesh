# P5 Part 1 Implementation Record

**Branch:** `p5/control-plane`  
**Base:** `p4/unified-execution-core`  
**Scope:** M49–M52  
**Status:** Accepted. The user's local Go 1.27.3 darwin/arm64 verification and GitHub Actions verification both passed.

## Implemented surfaces

### M49 — Dashboard foundation

- Server-rendered Gin + `html/template` Dashboard at `/dashboard`.
- Responsive navigation for Overview, Providers, Models, Agents, Workflows and Executions.
- Vendored HTMX 2.0.8 served from `/dashboard/assets/htmx.min.js`; no CDN dependency.
- API-token browser login uses a short-lived HMAC-signed HttpOnly, SameSite=Strict cookie scoped to `/dashboard`.
- Login is restricted to loopback HTTP or TLS, compares the token in constant time, and rate-limits repeated attempts per remote host.
- State-changing forms use POST and same-origin Origin/Referer validation.
- Dashboard pages set no-store and browser hardening headers.
- Existing API Bearer middleware remains in place; browser sessions are not accepted as API credentials.

### M50 — Provider, Model and Credential Console

- Provider and model catalog data is read from the existing registries and Catalog Handler.
- Model list includes the existing dynamic AI Resource catalog and respects its de-duplication behavior.
- Provider create/update/enable/disable/delete and connectivity test are routed through the existing Catalog Handler.
- Model create/update/enable/disable/delete are routed through the existing Catalog Handler.
- Credential status is metadata-only; set/delete use the existing Credential Manager. Secret values are never read back into a view model or HTML.

### M51 — Agent and Workflow Console

- Agent Definition list, create, edit, enable/disable and delete use the existing Agent Catalog service.
- Collaboration Agent Identity list, create, enable/disable and delete use the existing Collaboration service.
- Agent launch calls the existing Agent Catalog run entry point with a supplied workspace and prompt.
- Canonical Collaboration Tasks are listed across workspaces; create and assignment use the existing Collaboration service, including its capability/workspace validation.
- Workflow Template list, create and delete use the existing Workflow Authoring repository.
- Applying a template calls the existing Workflow Engine definition writer.
- Workflow launch calls the existing durable `StartQueued` entry point; no new queue is introduced.

### M52 — Unified Execution Center

- Execution list reads canonical `executions` and supports status, kind and runtime filters with bounded pagination.
- Execution detail reads canonical Execution and Attempt state plus existing Artifact Version and Evidence provenance.
- Timeline uses the P4 merged lifecycle/control API and its documented compound cursor; the Dashboard renders cursor-based “Load more” navigation.
- Artifact Versions and Evidence display their source Execution/Attempt provenance identifiers.
- Cancellation calls the existing Execution Manager. The UI says cancellation was requested, not that the runtime has confirmed cancellation.
- No generic replay/retry action is exposed for ambiguous outcomes.

## Architecture and security invariants

- No new database migration, queue, scheduler, Execution Manager, event ledger or duplicate artifact store is introduced.
- Dashboard commands call existing application services/handlers; read-only Execution views query existing canonical tables.
- Provider secrets, API tokens, raw upstream errors and execution input/result payloads are not rendered.
- The dashboard does not advertise public-internet deployment as supported. Keep the service bound to loopback unless a separately reviewed network/authentication boundary is configured.

## Verification

Run:

```bash
bash scripts/verify-p5-part1.sh
```

The script checks gofmt without modifying files, shell syntax, build, vet and race tests.

**Accepted commit:** `733f1224664bbb3b2e39f76ced4b310a74da65f0` on `p5/control-plane`.

**Verification:** GitHub Actions and the user's local macOS run both passed. Local command: `bash scripts/verify-p5-part1.sh`, Go `go1.27.3 darwin/arm64`. The script completed formatting, shell syntax, build, vet and full race-test checks and printed `P5 Part 1 verification passed.`

Part 1 is accepted. P5 Part 2 is specified in [p5-part2-operations.md](p5-part2-operations.md).
