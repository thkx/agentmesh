#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

echo "== AgentMesh P5 Part 2 verification =="
echo "Go: $(go version)"

echo "== Shell syntax =="
bash -n scripts/verify-p5-part2.sh
bash -n scripts/verify-p5-m53.sh
bash -n scripts/verify-p5-part1.sh

echo "== Go formatting =="
unformatted="$(gofmt -l $(find . -type f -name '*.go' -not -path './.git/*'))"
if [[ -n "$unformatted" ]]; then
  echo "Go files require gofmt:"
  echo "$unformatted"
  exit 1
fi

echo "== Frontend asset boundary =="
if find internal -type f \( -name '*.html' -o -name '*.css' -o -name '*.js' \) -print -quit | grep -q .; then
  echo "Browser-facing source must live under repository-root web/."
  find internal -type f \( -name '*.html' -o -name '*.css' -o -name '*.js' \)
  exit 1
fi
test -f web/embed.go
test -d web/dashboard
test -d web/workflow-editor

echo "== SQLC generated-code consistency =="
if command -v sqlc >/dev/null 2>&1; then
  sqlc generate
  git diff --exit-code -- internal/db
else
  echo "sqlc is not installed; generated-code consistency check skipped."
fi

echo "== Focused operations and migration tests =="
go test ./internal/platform/database ./internal/observability ./internal/usage ./internal/operations/backup ./internal/operations/upgrade ./internal/operations/diagnostics ./internal/transport/http ./internal/transport/http/dashboard ./internal/transport/http/handlers

echo "== Build =="
go build ./...

echo "== Vet =="
go vet ./...

echo "== Race tests =="
go test -race ./...

echo "P5 Part 2 verification passed."
