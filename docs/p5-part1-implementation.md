# P5 Part 1 Implementation Record

**Branch:** `p5/control-plane`  
**Base:** `p4/unified-execution-core`  
**Scope:** M49–M52  
**Status:** Implementation in progress; acceptance is not yet declared.

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

- Agent Definition list, create, enable/disable and delete use the existing Agent Catalog service.
- Agent launch calls the existing Agent Catalog run entry point with a supplied workspace and prompt.
- Workflow Template list, create and delete use the existing Workflow Authoring repository.
- Applying a template calls the existing Workflow Engine definition writer.
- Workflow launch calls the existing durable `StartQueued` entry point; no new queue is introduced.

### M52 — Unified Execution Center

- Execution list reads canonical `executions` and supports status, kind and runtime filters with bounded pagination.
- Execution detail reads canonical Execution and Attempt state plus existing Artifact Version and Evidence provenance.
- Timeline merges the existing lifecycle and control event streams for display; timestamps are ordered numerically in UTC and ties use stream/sequence ordering.
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

**Current gate:** Go formatting has been applied by CI in commit `0414cfb6c2ec1d19a5014c70306a1f6b98691435`. Build, vet and race verification for the final Part 1 commit remain pending. Do not treat earlier failed intermediate commits or a formatting-only run as acceptance evidence. The final report must identify the exact commit SHA and the completed CI result; the user should also run the script on the local Go 1.27.3 darwin/arm64 environment.

## Remaining acceptance work

- Finish and review Dashboard-specific integration/security tests for service mutations and execution provenance.
- Verify the exact final commit with Go 1.27.3 CI: gofmt, build, vet and race tests.
- Run the acceptance script locally on the user's macOS environment.
- Update the checklist only when the corresponding behavior and tests are verified.
