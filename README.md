# AgentMesh hardening patch

Target source baseline: `thkx/agentmesh` main at `01d67046ce6cd9a79314dedf053c8f8c4cbe6443`.

Apply from the repository root on a clean working tree:

```bash
python3 /path/to/apply_fixes.py
gofmt -w internal/config/validate.go internal/config/loader.go internal/config/validate_test.go internal/transport/http/router.go internal/transport/http/router_test.go internal/coding/service.go internal/agentcatalog/service.go
go test -race ./...
go vet ./...
go build ./...
```

The script uses exact source anchors and aborts if the checkout differs. Review `git diff` before committing.

## Included

- Config loading accepts a non-loopback bind only when `AGENTMESH_API_TOKEN` is at least 32 characters; Bootstrap continues to enforce token strength and presence.
- Only exact static `GET /workflow-editor` and `GET /workflow-editor/app.js` bypass Bearer auth.
- Coding task background starts reserve the existing two execution slots before spawning goroutines; overload returns `ErrConflict` rather than accumulating unbounded goroutines.
- Per-task workspace locks serialize run/patch/revert/cleanup; different tasks remain concurrent. Patch and revert reject active task states.
- Agent Definition run completion uses a bounded persistence context, preserves failed status, and reports result-persistence failures.
- Config and HTTP regression tests.

## Not yet closed by this patch

Coding event/outbox atomicity and every ignored event write; durable coding queue/lease and restart recovery; moving Agent Definition runs onto the common scheduler; complete Workflow Worker shutdown accounting; full Tool Engine policy enforcement and Docker sandbox boundary audit. No schema changes are made here.
