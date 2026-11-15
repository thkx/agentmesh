# M2-B — Encryption & Storage

## Scope

- AES-256-GCM authenticated encryption (`nonce || ciphertext || tag`).
- A `KeyProvider` abstraction separates encryption key management from SQLite.
- Portable `FileKeyProvider` fallback: 32 random bytes, private directory (`0700`), private key file (`0600`), exclusive creation, and strict permission/length validation on read.
- SQLite `provider_credentials` table stores ciphertext and an encryption format version; provider deletion cascades to its credential row.
- `SQLiteStore` implements the M2-A `credential.Store` interface and uses generated SQLC query methods.

## Security properties

- API keys are encrypted before they reach SQLite.
- AES-GCM authenticates ciphertext and rejects wrong keys or modified data.
- A fresh cryptographically random nonce is generated for each encryption.
- The encryption key is not stored in SQLite. Keep the key file separate from the database and outside source control.
- Store methods never log or return plaintext except `Get`, which returns the M2-A in-memory `Secret` type.
- The file-key fallback is not equivalent to an OS keychain or KMS. Production deployments should inject a keychain/KMS implementation of `cryptography.KeyProvider`.

## Persistence schema

`provider_credentials`:

| Column | Meaning |
|---|---|
| `provider_id` | Provider instance ID; primary key and FK to `providers(id)` |
| `encrypted_value` | AES-GCM nonce + ciphertext + authentication tag |
| `encryption_version` | Ciphertext format version (currently `1`) |
| `created_at`, `updated_at` | UTC Unix milliseconds |

## Not included

- Credential HTTP APIs (M2-D).
- Environment/stored credential resolution and startup behavior changes (M2-C).
- Adapter hot reload / in-flight lease handling (M2-E).
- Provider connection testing (M2-F).

## Validation coverage

Unit tests cover round-trip encryption, unique nonces, wrong-key rejection, tamper detection, key persistence, and key-file permissions. SQLite store tests cover persistence across database reopen, ciphertext-only storage, wrong-key rejection, CRUD, missing providers, and provider-delete cascade.
