# SQLC database layer

Generated query code lives in this package. `sqlc.yaml` uses the same embedded runtime migration files under `internal/platform/database/migrations` that `database.RunMigrations` applies at startup.

Regenerate after changing `queries/*.sql` or schema migrations:

```bash
sqlc generate
```

Do not edit generated `*.sql.go`, `models.go`, or `querier.go` manually.
