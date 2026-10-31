# AgentMesh M1.0 SQLite PoC

Isolated validation project for macOS Apple Silicon, Go 1.27.3, and CGO-disabled
SQLite. It does not modify the frozen AgentMesh project.

## Prerequisites

- Go 1.27.3 (`darwin/arm64`)
- sqlc v1.31.1

Install sqlc if needed:

```bash
go install github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1
sqlc version
```

## Run

Run these commands in order from this directory:

```bash
sqlc generate
go mod tidy
CGO_ENABLED=0 go test ./...
CGO_ENABLED=0 go run ./cmd/poc
CGO_ENABLED=0 go build ./...
```

The executable creates `data/agentmesh-poc.db`, applies the migration, inserts
a sample user through generated sqlc code, and lists users.

## Design notes

- The golang-migrate `database/sqlite` driver uses `modernc.org/sqlite` (pure Go).
- Its SQLite driver wraps each migration in an implicit transaction by default.
  Do not put explicit `BEGIN` / `COMMIT` statements in migration files.
- The one-connection pool is intentional for deterministic local validation.
- This is a PoC, not production database infrastructure. Pool tuning, concurrent
  write behavior, backup/restore, and crash recovery require separate tests.

agentmesh/
├── cmd/
│   ├── agentmesh/
│   │   └── main.go
│   └── agentmesh-cli/
│       └── main.go
│
├── configs/
│   └── config.example.yaml
│
├── internal/
│   ├── app/
│   │   ├── bootstrap.go
│   │   ├── container.go
│   │   └── shutdown.go
│   │
│   ├── config/
│   │   ├── config.go
│   │   ├── loader.go
│   │   ├── validate.go
│   │   └── defaults.go
│   │
│   ├── domain/
│   │   └── provider/
│   │       ├── provider.go
│   │       ├── model.go
│   │       ├── credential.go
│   │       └── errors.go
│   │
│   ├── modelgateway/
│   │   ├── gateway.go
│   │   ├── registry.go
│   │   ├── resolver.go
│   │   └── adapters/
│   │       ├── openai/
│   │       │   ├── provider.go
│   │       │   ├── model.go
│   │       │   └── mapper.go
│   │       ├── anthropic/
│   │       │   ├── provider.go
│   │       │   ├── model.go
│   │       │   └── mapper.go
│   │       ├── gemini/
│   │       │   ├── provider.go
│   │       │   ├── model.go
│   │       │   └── mapper.go
│   │       └── ollama/
│   │           ├── provider.go
│   │           ├── model.go
│   │           └── mapper.go
│   │
│   ├── platform/
│   │   ├── database/
│   │   │   └── postgres/
│   │   │       ├── pool.go
│   │   │       └── health.go
│   │   ├── secrets/
│   │   │   ├── cipher.go
│   │   │   └── manager.go
│   │   └── logger/
│   │       └── logger.go
│   │
│   └── transport/
│       ├── http/
│       │   ├── router.go
│       │   ├── middleware/
│       │   └── handlers/
│       │       ├── health.go
│       │       ├── providers.go
│       │       └── models.go
│       └── cli/
│           ├── root.go
│           ├── provider.go
│           └── model.go
│
├── migrations/
│   └── 000001_init_providers.up.sql
│   └── 000001_init_providers.down.sql
│
├── queries/
│   └── providers.sql
│
├── schemas/
│   └── config.schema.json
│
├── test/
│   ├── integration/
│   └── fixtures/
│
├── docs/
│   ├── architecture/
│   └── milestones/
│       └── M1.md
│
├── sqlc.yaml
├── Makefile
├── go.mod
├── go.sum
├── .env.example
├── .gitignore
└── README.md