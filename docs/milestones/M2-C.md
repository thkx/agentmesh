# M2-C — Provider Runtime Credential Integration

## Goal

Connect the M2-A credential resolver and M2-B encrypted SQLite store to the existing Provider Runtime. Cloud provider instances may exist in an enabled state before credentials are configured; this must not prevent AgentMesh from starting.

## Runtime behavior

- Credential resolution precedence is `Environment > Stored`.
- `Provider.CredentialRef` remains the environment-variable name for compatibility with YAML `api_key_env`.
- Empty or missing environment values fall back to the encrypted stored credential.
- If neither source has a credential, resolution returns `credential.ErrCredentialUnavailable`.
- During bootstrap, an enabled cloud provider with no credential remains in `ProviderRegistry`, but no adapter is published for it. Local Ollama providers do not require credentials.
- `ProviderRuntime.ApplyProvider` treats a missing credential as an unconfigured state, not a fatal provider-catalog error. It removes any stale adapter for that provider instance.
- Other adapter construction failures (bad URL, SDK initialization errors, storage/decryption errors) still fail the update and do not silently become “unconfigured”.
- Secrets are only passed to the adapter constructor. They are not copied into Provider, Model, HTTP responses, or logs.

## Encryption-key location

The portable M2-B file key provider is initialized during bootstrap. The key is stored separately from SQLite at `<database-directory>/keys/credential.key`, with a private directory and file permissions enforced by `FileKeyProvider`. The key file must be backed up securely alongside the database; losing it makes stored credentials unrecoverable. For production deployments, an OS keychain or external secret manager can replace this provider in a later milestone without changing the `KeyProvider` contract.

## Container dependencies

`Container` exposes the credential `Store` and `Resolver` for subsequent M2-D management APIs and M2-E runtime refresh integration. No credential-management HTTP endpoints or automatic adapter hot reload are introduced in M2-C.

## Acceptance

- Environment value overrides stored value for the same provider instance.
- Missing/blank environment value falls back to stored credential.
- Missing both sources is reported as unconfigured and does not abort bootstrap.
- Same-type provider instances resolve credentials independently by provider ID / environment variable reference.
- An enabled provider without credentials remains catalogued but has no adapter.
- Runtime update with missing credentials removes stale adapter; unrelated adapter build/storage errors remain visible.
- Local Ollama adapter startup remains independent of credential storage.

## Validation note

The current development environment has Go 1.23.2 while this module declares Go 1.27.3. Credential resolver packages were tested in an isolated Go 1.23-compatible module. Full-project tests must be run with Go 1.27.3 or newer.
