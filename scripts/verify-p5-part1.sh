#!/usr/bin/env bash
set -euo pipefail

echo "== AgentMesh P5 Part 1 verification =="
go version

echo
echo "== Go formatting check =="
files="$(gofmt -l $(find . -type f -name '*.go' -not -path './.git/*'))"
if [[ -n "$files" ]]; then
  echo "The following Go files require gofmt:"
  echo "$files"
  gofmt -d $files
  exit 1
fi

echo
echo "== Shell syntax =="
bash -n scripts/verify-p5-part1.sh

echo
echo "== Build =="
go build ./...

echo
echo "== Vet =="
go vet ./...

echo
echo "== Race tests =="
go test -race ./...

echo
echo "P5 Part 1 verification passed."
