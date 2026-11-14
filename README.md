# AgentMesh Coding Event Order Fix

This is a minimal follow-up patch for the P0/P1 package. It changes only the event query ordering in `internal/coding/repository.go`.

## Apply

From the AgentMesh repository root:

```bash
git apply /path/to/fix.patch
```

If `git apply` reports that the patch does not apply, stop and send me the output; do not replace the whole repository file manually.

## Root cause

`coding_task_events.created_at` is stored with second precision (`Unix()`), while event IDs are random UUIDs. The previous query sorted by `created_at, id`, so events created within the same second could be returned in arbitrary UUID order, making a valid sequence such as `task.created -> task.started -> task.succeeded` appear out of order.

The patch orders the current rowid-backed SQLite event table by `rowid`, which reflects insertion order. The current schema uses a TEXT primary key and does not declare `WITHOUT ROWID`.

## Verify

```bash
gofmt -w internal/coding/repository.go
go test -race ./internal/coding -run 'TestRepositoryCreateClaimAndUpdateEventsAreAtomic' -count=50
go test -race ./...
go vet ./...
go build ./...
```
