#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
echo "== AgentMesh P3 verification =="
echo "Go: $(go version)"
gofmt -w $(find . -type f -name '*.go' -not -path './.git/*')
unformatted="$(gofmt -l $(find . -type f -name '*.go' -not -path './.git/*'))"
if [[ -n "$unformatted" ]]; then printf '%s\n' "$unformatted"; exit 1; fi
go build ./...
go vet ./...
go test -race ./...
echo "P3 verification passed."
