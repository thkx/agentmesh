# M2-A — Credential Domain & Abstraction

## Scope

M2-A establishes the domain types and application contracts for provider credentials. It deliberately does not implement encryption, SQLite persistence, OS keychain integration, HTTP endpoints, or runtime hot reload; those belong to M2-B through M2-E.

## Domain model

- `domain/credential.Source`: `environment` and `stored`.
- `domain/credential.Status`: `unconfigured`, `configured`, and `invalid`.
- `domain/credential.Secret`: in-memory secret wrapper with a private value and redacted `String` / `GoString` formatting.
- `domain/credential.Credential`: resolved secret for one Provider Instance ID.
- `domain/credential.Record`: safe metadata only; never contains the secret value.

## Application contracts

`internal/credential` declares:

- `Store`: put/get/delete/exists for provider credentials. Durable implementations must encrypt before writing.
- `EnvironmentLookup`: injectable environment-variable lookup.
- `Resolver`: resolves the effective credential and reports safe status.
- `Manager`: use-case boundary for setting, deleting, and querying stored credentials.

## Resolution policy

The agreed policy is `Environment > Stored`:

1. If the provider's configured environment variable exists and is non-empty, it is the effective credential.
2. Otherwise, a stored credential may be used.
3. If neither source is available, status is `unconfigured`; this is not an application startup error.
4. A stored credential update does not override an environment variable. Status must report the effective source so operators can see which credential is active.

## Compatibility

M1's `provider.Provider.CredentialRef` and YAML `api_key_env` remain in place for compatibility. M2-C will connect this legacy field to the new resolver and remove the current startup failure when the environment variable is missing.

## Security constraints

- Secret values are never part of `Record` or API response DTOs.
- Do not log `Secret.Reveal()` or raw credential request bodies.
- `Secret.String()` and `Secret.GoString()` redact values as defense in depth; this is not a substitute for careful logging.
- M2-B must encrypt durable values with a key stored separately from SQLite.
- Environment variables are not copied into persistent storage.

## Acceptance

- Domain types validate Provider Instance ID, source, status, and non-empty secret.
- Secret formatting is redacted.
- Store / Resolver / Manager / EnvironmentLookup contracts compile.
- Existing M1 runtime behavior remains unchanged until M2-C.
