# M2-H — Integration Tests & Acceptance

Regression coverage includes credential resolver precedence and provider-instance isolation, manager persistence/runtime rollback, credential HTTP secret redaction, adapter replacement semantics, SQLite encrypted storage, and provider connection-test failure sanitization.

Run acceptance checks with the Go version declared in `go.mod`:

```bash
gofmt -w ./cmd ./internal
go test ./...
go test -race ./...
go vet ./...
```

The project currently declares Go 1.27.3. If the local toolchain cannot download that version, full-module and race-test acceptance must be rerun in a Go 1.27.3 environment; isolated older-toolchain checks are not equivalent to full acceptance.
