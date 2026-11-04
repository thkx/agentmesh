
.PHONY: run test build tidy migrate-check

run:
	go run ./cmd/agentmesh --config configs/config.yaml

test:
	CGO_ENABLED=0 go test ./...

build:
	CGO_ENABLED=0 go build -o bin/agentmesh ./cmd/agentmesh

tidy:
	go mod tidy

migrate-check:
	go test ./internal/platform/database -v
