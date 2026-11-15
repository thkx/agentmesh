# P4 Part 3 — Cross-runtime Integration and Release Acceptance

> M47–M48 implementation record. Run `bash scripts/verify-p4-part3.sh` on Go 1.27.3 before accepting this part.

## M47 — Unified Execution detail and history

The common HTTP read model is additive and reads the canonical P2/P4 tables directly. It does not introduce another execution manager or duplicate P3 Artifact/Evidence storage.

### Endpoints

- `GET /api/v1/managed-executions/:id/detail`
  - Returns the canonical Execution snapshot.
  - Returns Attempts ordered by attempt number.
  - Returns Artifact Versions linked by source Execution or one of its Attempts.
  - Returns Evidence linked by Execution or one of its Attempts.
  - Preserves JSON input/result as JSON values, not quoted JSON strings.
  - Returns 404 for an unknown Execution.

- `GET /api/v1/managed-executions/:id/timeline?limit=50&after_time_ns=0`
  - Merges the M43 append-only `execution_events` stream and the M46 append-only `execution_control_events` stream.
  - Each item identifies its source stream, stream-local sequence, actor, source, event key, Attempt (when applicable), structured payload and UTC occurrence time.
  - Orders by `occurred_at`, then stream name, then stream-local sequence.
  - Uses a tuple cursor (`after_time_ns`, `after_stream`, `after_sequence`) because the two append-only tables intentionally retain independent sequence spaces.
  - Caps page size at 200. The response states the ordering contract and returns the next cursor.

The two event tables are not falsely presented as having a shared global sequence. Their persisted UTC nanosecond timestamps provide the cross-stream chronology; stream and sequence provide deterministic tie-breaking and resumable pagination. This is an ordered history view, not a claim of a globally serialized transaction order across two independent writers.

Both endpoints are registered only when the database dependency is available and inherit the existing optional Bearer API-token middleware.

## M48 — Acceptance coverage

New HTTP handler tests cover:
- merging lifecycle and cancellation-control events;
- chronological ordering across the two streams;
- tuple-cursor pagination across streams;
- Execution detail with Attempt, Artifact Version and Evidence provenance;
- raw JSON input/result response shape;
- 404 behavior for unknown Execution IDs.

The existing full test suite continues to exercise runtime-specific Agent, Coding, Workflow and CLI adapters and their execution lifecycle behavior. The unified read model is runtime-neutral: it keys only on the common Execution/Attempt schema and therefore does not branch by runtime.

## Changed files

- `internal/transport/http/handlers/unified_execution.go`
- `internal/transport/http/handlers/unified_execution_test.go`
- `internal/transport/http/router.go`
- `scripts/verify-p4-part3.sh`

No database migration or sqlc query change is required: M43, M44 and M46 already provide the durable event, Attempt and provenance records consumed by these read APIs.

## Verification

The acceptance script runs:
1. repository-wide gofmt check;
2. `go build ./...`;
3. `go vet ./...`;
4. `go test -race ./...`.

No result is implied by this document. Part 3 is accepted only after the script passes on the exact synchronized branch commit using the target Go 1.27.3 environment.
