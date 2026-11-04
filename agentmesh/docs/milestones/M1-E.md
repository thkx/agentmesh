# M1-E — HTTP Server and API

## Scope

M1-E exposes the existing Provider Registry, Model Registry, Provider Runtime, Catalog Repository, and Gateway over HTTP. It does not add authentication, billing, user isolation, or a remote management plane.

## Runtime

`cmd/agentmesh` now starts a real `http.Server`, listens on `server.host:server.port`, handles SIGINT/SIGTERM, stops accepting new connections, drains active requests, and closes SQLite afterwards.

The HTTP server intentionally leaves `WriteTimeout` unset because Server.WriteTimeout is an absolute response deadline and would terminate long-lived Server-Sent Events. Request body size is limited to 4 MiB; Generate requests are bounded by the Gateway request context (120 seconds). `server.write_timeout` remains in config for compatibility but is not applied to the shared server while SSE is supported.

## Endpoints

- `GET /healthz` — process liveness.
- `GET /readyz` — dependency wiring readiness.
- `GET|POST /api/v1/providers`
- `GET|PUT|DELETE /api/v1/providers/:id`
- `PATCH /api/v1/providers/:id/enabled`
- `POST /api/v1/providers/:id/test` — runtime readiness only; deliberately does not make an upstream/billable request.
- `GET|POST /api/v1/models`
- `GET|PUT|DELETE /api/v1/models/:id`
- `PATCH /api/v1/models/:id/enabled`
- `POST /api/v1/generate`
- `POST /api/v1/generate/stream` — SSE (`text/event-stream`).

Provider responses never include `credential_ref` / `api_key_env`.

## Catalog consistency

Provider mutations build/publish the adapter and update the in-memory Provider Registry, then persist the full current Provider/Model catalogue in one repository transaction. If persistence fails, the previous Provider Runtime configuration is restored. Model mutations persist the replacement catalogue before publishing the Model Registry update. Provider deletion is delegated to SQLite first so the foreign-key constraint rejects deletion while models still reference it.

## Security boundary

M1-E has no API authentication or authorization. Keep `server.host` bound to `127.0.0.1` or another loopback address. Do not expose this API to a LAN or the public Internet until an authentication/authorization layer is implemented.

## Validation

The HTTP integration tests exercise provider/model CRUD, adapter readiness, FK-protected deletion, and health endpoint behavior. Run with the project's required Go toolchain:

```bash
gofmt -w $(find . -name '*.go' -not -path './.git/*')
go test ./...
go test -race ./internal/transport/http/... ./internal/modelgateway/...
go build ./...
```
