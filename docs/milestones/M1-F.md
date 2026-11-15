# M1-F — Integration Tests and Acceptance

## Scope

M1-F verifies the M1-A through M1-E implementation without adding new product features or changing the package layout. It focuses on provider-instance isolation, runtime update safety, Gateway stream lifecycle, SQLite persistence constraints, and the HTTP-to-Gateway execution path.

## Added acceptance coverage

### Provider runtime isolation

- Two `openai` provider instances are built independently.
- Each adapter retains its own Provider Instance ID, Base URL, and credential environment-variable reference.
- A failed adapter build does not replace the currently working adapter or provider registry entry.

### Gateway lifecycle

- Stream events stop after `Completed` / `Error` (existing M1-C tests).
- Stream output closes after request-context cancellation.
- Provider/model identity and provider errors are normalized (existing M1-C tests).

### HTTP integration

- Provider and Model CRUD lifecycle against a real temporary SQLite database.
- Concurrent Model creates preserve both catalog entries; the handler now serializes catalog writes to prevent lost updates from concurrent read-modify-write snapshots.
- Provider deletion is rejected while Models reference it.
- Generate and SSE endpoints reach the Gateway and return normalized internal Provider/Model identifiers.
- Health endpoint is covered (readiness requires a live DB and complete runtime dependencies).

### Persistence

- Migration idempotence, SQLC CRUD, foreign-key restrictions, and atomic catalog sync are covered by existing integration tests.

## Local acceptance commands

Use the Go version declared by `go.mod` (Go 1.27.3):

```bash
gofmt -w $(find . -name '*.go' -not -path './.git/*')
go test ./...
go test -race ./internal/modelgateway ./internal/platform/database ./internal/integration ./internal/transport/http/...
go vet ./...
go build ./...
```

Recommended manual smoke test after starting the server:

```bash
curl -i http://127.0.0.1:8080/healthz
curl -i http://127.0.0.1:8080/readyz
curl -s http://127.0.0.1:8080/api/v1/providers
curl -s http://127.0.0.1:8080/api/v1/models
```

For an actual upstream Generate/SSE smoke test, configure a provider and model with valid credentials. The automated HTTP tests use a deterministic fake adapter and do not incur provider charges.

## Acceptance status in the build environment

- `gofmt`: executed successfully.
- Go tests / race detector / vet / build: not executed successfully in this environment because the installed Go toolchain is 1.23.2 while `go.mod` requires 1.27.3; automatic toolchain download is blocked by unavailable DNS/network access. This is an environment limitation, not a passing test result.
- No live provider API calls are made by the automated tests.

## M1 exit criteria

M1-F is code-complete when the test suite is present. M1 is considered fully accepted only after the commands above pass in a Go 1.27.3 environment, and the operator confirms the manual health/readiness and at least one real provider request.
