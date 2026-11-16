#!/usr/bin/env bash
set -euo pipefail

echo "== AgentMesh P5 M53 verification =="
echo "Go: $(go version)"

echo
echo "== Shell syntax =="
bash -n scripts/verify-p5-m53.sh

echo
echo "== Go formatting =="
unformatted="$(gofmt -l $(find . -type f -name '*.go' -not -path './.git/*'))"
if [[ -n "${unformatted}" ]]; then
  echo "Go files require gofmt:"
  echo "${unformatted}"
  exit 1
fi

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
echo "P5 M53 verification passed."
