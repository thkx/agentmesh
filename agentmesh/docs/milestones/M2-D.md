# M2-D — Credential HTTP API

Adds provider-instance credential management endpoints:

- `GET /api/v1/providers/{id}/credential`: returns safe metadata (`provider_id`, `source`, `status`, optional `updated_at`).
- `PUT /api/v1/providers/{id}/credential`: accepts `{"api_key":"..."}` and stores the encrypted value.
- `DELETE /api/v1/providers/{id}/credential`: deletes the stored value; an environment variable remains effective due to Environment > Stored precedence.

The API never returns the submitted secret. Request bodies are bounded by the router's 4 MiB limit. Error responses are generic and do not echo submitted values or raw storage/decryption errors. Manager operations persist first, then apply the provider runtime; on runtime failure the previous stored value is restored.

The HTTP server still has no authentication. Keep it bound to loopback and do not expose it to a network.
