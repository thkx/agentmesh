#!/usr/bin/env bash
set -euo pipefail

echo "== AgentMesh P5 Part 2 verification =="
echo "Go: $(go version)"

echo "== Shell syntax =="
bash -n scripts/verify-p5-part2.sh
bash -n scripts/verify-p5-m53.sh

echo "== Formatting =="
unformatted="$(gofmt -l $(find . -type f -name '*.go' -not -path './.git/*'))"
if [[ -n "$unformatted" ]]; then echo "$unformatted"; exit 1; fi

echo "== Build =="
go build ./...
echo "== Vet =="
go vet ./...
echo "== Race tests =="
go test -race ./...
echo "P5 Part 2 verification passed."
