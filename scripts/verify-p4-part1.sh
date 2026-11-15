#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

echo "== AgentMesh P4 Part 1 verification =="
echo "Go: $(go version)"

unformatted="$(gofmt -l $(find . -type f -name '*.go' -not -path './.git/*'))"
if [[ -n "$unformatted" ]]; then
  echo "The following Go files are not gofmt-formatted:"
  printf '%s\n' "$unformatted"
  exit 1
fi

echo "== go build ./... =="
go build ./...

echo "== go vet ./... =="
go vet ./...

echo "== go test -race ./... =="
go test -race ./...

echo "P4 Part 1 verification passed."
