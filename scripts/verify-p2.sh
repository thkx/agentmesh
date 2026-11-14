#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

echo "== AgentMesh P2 verification =="
echo "Go: $(go version)"

echo
echo "== gofmt check =="
unformatted="$(gofmt -l $(find . -type f -name '*.go' -not -path './.git/*'))"
if [[ -n "$unformatted" ]]; then
  printf '%s\n' "$unformatted"
  echo "Go files are not formatted."
  exit 1
fi

echo
echo "== go build ./... =="
go build ./...

echo
echo "== go vet ./... =="
go vet ./...

echo
echo "== go test -race ./... =="
go test -race ./...

echo
echo "P2 verification passed."
