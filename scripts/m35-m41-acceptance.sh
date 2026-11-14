#!/usr/bin/env bash
set -euo pipefail

echo "== formatting =="
unformatted="$(gofmt -l .)"
if [[ -n "$unformatted" ]]; then
  echo "$unformatted"
  exit 1
fi

echo "== sqlc =="
sqlc generate

echo "== build =="
go build ./...

echo "== tests =="
go test -race ./...

echo "== vet =="
go vet ./...

echo "M35-M41 acceptance checks passed."
