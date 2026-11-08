# M17–M22 implementation status

This source tree is based on the previously supplied `agentmesh-m16-final.zip` and adds migration 000018 plus the `internal/airesource` package and HTTP wiring.

## Included

- M17: separate Provider, Endpoint, Credential and Model resource entities; model-to-credential bindings.
- M18: AES-256-GCM encrypted API-key persistence using the existing local credential key file; bulk import is transactional; list responses are masked.
- M19: synchronous protocol adapters for OpenAI-compatible Chat Completions, Anthropic Messages and Gemini generateContent.
- M20: model aliases, per-credential upstream model mapping and remote model discovery endpoints.
- M21: priority, round-robin, weighted round-robin and least-connections selection; per-key concurrency caps; bounded retries; 401 invalidation; 429 cooldown; failure-threshold circuit opening and single in-process half-open probe.
- M22: HTTP management API, `/v1/chat/completions`, simplified `/v1/responses`, request logs, usage aggregation, manual health checks and optional API-token protection.

## Explicit v1 limitations

- The new resource Gateway is synchronous. `stream=true` returns `ErrStreamingUnsupported`; it does not pretend to stream. Existing M1–M16 Gateway streaming is unchanged.
- Tool calling, multimodal payloads, vendor-specific advanced options and seamless mid-stream failover are not implemented in this compatibility surface.
- `custom` auth headers are not a separate protocol. Providers using OpenAI-compatible request/response shapes should use `openai-compatible` and Bearer authentication.
- Health checks are manual/passive; no periodic paid probe loop is started automatically.
- The new resource repository currently uses parameterized `database/sql` statements for the multi-table candidate join and transactionally coupled encryption/health/usage writes. SQLC schema and query definitions are updated, but this package is not yet calling generated sqlc methods.
- The two model aliases are seeded by the operator. Use the exact upstream model identifiers returned by each provider's discovery endpoint; the display names supplied by the operator are not assumed to be upstream API IDs.

## Validation performed in this environment

- All migrations 000001–000018 were executed against Python's SQLite engine; resource tables, foreign keys, credential insert, candidate join and usage SQL smoke tests passed.
- `internal/airesource` core package and unit tests passed under an isolated Go 1.23.2 check module, including `-race`.
- HTTP handler source was type-checked against a minimal Gin API stub and passed.
- `gofmt -l .` produced no output.

## Validation not performed

The full project `go test -race ./...`, `go vet ./...`, `go build ./...` and `sqlc generate` were not run. This environment has Go 1.23.2, while the project baseline requires Go 1.27.3; downloading the required toolchain/dependencies was blocked by unavailable network access. Run the project's standard checks with Go 1.27.3 and sqlc v1.31.1 before treating this archive as production-ready.
