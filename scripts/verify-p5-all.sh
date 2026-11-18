#!/usr/bin/env bash
# Full P5 verification. Safe by default: unit/integration tests use isolated fixtures.
# Set AGENTMESH_RUN_PRODUCT_SMOKE=1 to additionally test a running local instance.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

echo "== AgentMesh P5 full verification =="
go version

echo "== Shell syntax =="
bash -n scripts/verify-p5-part1.sh
bash -n scripts/verify-p5-m53.sh
bash -n scripts/verify-p5-part2.sh
bash -n scripts/accept-p5-e2e.sh
bash -n scripts/accept-p5-live.sh
bash -n scripts/verify-p5-all.sh

echo "== Frontend source boundary =="
if find internal -type f \( -name '*.html' -o -name '*.css' -o -name '*.js' \) -print -quit | grep -q .; then
  echo "Browser-facing source must live under repository-root web/." >&2
  find internal -type f \( -name '*.html' -o -name '*.css' -o -name '*.js' \) >&2
  exit 1
fi
test -f web/embed.go
test -d web/dashboard
test -d web/workflow-editor

echo "== Go formatting =="
unformatted="$(gofmt -l $(find . -type f -name '*.go' -not -path './.git/*'))"
if [[ -n "$unformatted" ]]; then
  echo "Go files require gofmt:" >&2
  echo "$unformatted" >&2
  exit 1
fi

echo "== SQLC generated-code consistency =="
if command -v sqlc >/dev/null 2>&1; then
  sqlc generate
  git diff --exit-code -- internal/db
else
  echo "SKIP: sqlc is not installed; install sqlc to verify generated-code consistency."
fi

echo "== Complete Go test suite with race detector =="
go test -race ./...

echo "== Build =="
go build ./...

echo "== Vet =="
go vet ./...

if [[ "${AGENTMESH_RUN_PRODUCT_SMOKE:-0}" == "1" ]]; then
  echo "== Running product smoke test against configured instance =="
  bash scripts/accept-p5-e2e.sh
else
  echo "SKIP: running-instance product smoke test (set AGENTMESH_RUN_PRODUCT_SMOKE=1)."
fi

echo
echo "P5 full verification passed."
