#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

echo "== AgentMesh P6 verification =="
go version
echo "== Shell syntax =="
bash -n scripts/verify-p6.sh scripts/accept-p6-coding.sh
echo "== Frontend source boundary =="
if find internal -type f \( -name '*.html' -o -name '*.css' -o -name '*.js' \) -print -quit | grep -q .; then
  echo "Browser-facing source must live under repository-root web/." >&2
  exit 1
fi
test -f web/dashboard/templates/coding.html

echo "== Go formatting =="
unformatted="$(gofmt -l $(find . -type f -name '*.go' -not -path './.git/*'))"
if [[ -n "$unformatted" ]]; then
  echo "Go files require gofmt:" >&2
  echo "$unformatted" >&2
  exit 1
fi

echo "== SQLC consistency =="
if command -v sqlc >/dev/null 2>&1; then
  sqlc generate
  git diff --exit-code -- internal/db
else
  echo "SKIP: sqlc is not installed."
fi

echo "== Full race test suite =="
go test -race ./...
echo "== Build =="
go build ./...
echo "== Vet =="
go vet ./...

if [[ "${AGENTMESH_RUN_P6_LIVE:-0}" == "1" ]]; then
  bash scripts/accept-p6-coding.sh
else
  echo "SKIP: real model + Docker live acceptance (set AGENTMESH_RUN_P6_LIVE=1 after configuring the required variables)."
fi

echo "P6 verification passed."
