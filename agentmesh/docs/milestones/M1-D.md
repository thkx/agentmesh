# M1-D — SQLite persistence for Provider / Model catalog

## Scope

M1-D replaces the split SQLite PoC migration path with one runtime migration source and persists Provider instances and Model definitions. It does not add HTTP CRUD endpoints or move SQLite dependencies into the Gateway.

## Storage layout

All runtime migrations live in `internal/platform/database/migrations` and are embedded by `internal/platform/database/migrate.go`:

| Version | Migration | Purpose |
|---|---|---|
| 000001 | init | application metadata |
| 000002 | create users | existing SQLC sample user table, now part of the runtime schema |
| 000003 | create providers | provider instances and credential references |
| 000004 | create models | model catalog with provider foreign key |

The former root `migrations/` directory was removed. `sqlc.yaml` reads the same `*.up.sql` files used by the runtime migration set, so generated query schema and application schema no longer diverge.

## Data model

- `providers.id` is the provider instance ID, not the vendor type.
- `providers.type` stores `openai`, `anthropic`, `gemini`, or `ollama`.
- `credential_ref` stores an environment variable name only; raw API keys are never persisted.
- `models.id` is the stable AgentMesh model ID.
- `models.provider_id` references `providers.id` with `ON DELETE RESTRICT`.
- Boolean values are stored as constrained SQLite integers.
- Timestamps are UTC Unix milliseconds.

## Runtime flow

1. Bootstrap loads and validates YAML.
2. `RuntimeRepository.Sync` writes configured providers and models in one SQL transaction.
3. Bootstrap loads the persisted catalog.
4. Existing in-memory ProviderRegistry / ModelRegistry and AdapterFactory are initialized from persisted records.
5. Gateway continues to depend only on registries and adapter interfaces; it does not know about SQLite or sqlc.

The YAML configuration currently acts as the startup synchronization source. HTTP-managed catalog CRUD and reconciliation policies are intentionally outside M1-D.

## Validation

Run with the Go version declared by `go.mod`:

```bash
gofmt -w $(find . -name '*.go' -not -path './.git/*')
sqlc generate
go test ./...
go test -race ./internal/platform/database ./internal/integration ./internal/modelgateway
go build ./...
```

Integration tests cover migration idempotency, generated user queries, provider/model round-trips, foreign-key delete restriction, and transaction atomicity.
