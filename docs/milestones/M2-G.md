# M2-G — Runtime Reliability

Reliability guarantees implemented across M2:

- Missing credentials are an explicit unconfigured state, not a process-startup failure.
- Environment variables override stored credentials.
- Provider instance IDs isolate credentials, Base URLs, and adapter instances.
- Credential mutation is serialized, persisted before runtime publication, and rolled back when adapter rebuild fails. Credential refresh requires an existing provider and cannot resurrect a provider deleted concurrently.
- Adapter construction happens before publication; a failed rebuild does not replace a working adapter.
- Adapter replacement is atomic for new lookups; in-flight requests retain their already acquired adapter reference.
- Stored credentials are encrypted with AES-256-GCM and authenticated on read.
- HTTP credential responses and errors never contain API key values.
- Connection tests have a fixed deadline and sanitized error output.

The server remains a single-process runtime. Multi-process catalog writers, adapter reference-counted shutdown, automatic retries, and key rotation are outside M2 scope.
