#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"

required="$(awk '$1 == "go" { print $2; exit }' go.mod)"
current="$(go env GOVERSION | sed 's/^go//')"
if [[ "$(printf '%s\n%s\n' "$required" "$current" | sort -V | head -n1)" != "$required" ]]; then
  echo "Go $required or newer is required by go.mod; found go$current" >&2
  exit 2
fi

echo "[M11] Workflow process, crash, idempotency and long-running tests"
go test -race -count=1 ./internal/workflow/checkpoint ./internal/workflow/runtime ./internal/agent/scheduler

echo "[M11] Full race-enabled test suite"
go test -race -count=1 ./...

echo "[M11] go vet"
go vet ./...

echo "[M11] reliability acceptance passed"
