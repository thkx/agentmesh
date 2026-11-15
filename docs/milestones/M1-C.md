# M1-C — Gateway execution policy

## Scope

M1-C strengthens the existing Gateway without changing the project layout or
introducing persistence, HTTP endpoints, retries, or a new adapter abstraction.

## Request execution path

1. Validate the caller request (`model_id`, messages, roles, sampling values,
   and non-negative output-token count).
2. Resolve the internal model ID.
3. Validate the resolved model and reject disabled models.
4. Enforce the model's configured `max_output_tokens` ceiling when the caller
   explicitly requests a positive value.
5. Resolve and validate the provider instance when the Gateway was created
   with `NewGatewayWithProviders`.
6. Resolve the adapter by provider-instance ID and populate the private
   `ProviderID` / `ProviderModel` request fields.
7. Execute Generate or Stream with the caller's context.
8. Normalize response/event/error identity to the resolved AgentMesh IDs.

## Token policy

`Model.MaxOutputTokens` is an upper bound, not a default. A request value of
zero preserves provider-default behavior. A positive request value greater
than the model's configured ceiling is rejected before the adapter is called.

The Gateway does not estimate input tokens. Correct token counting requires a
model-specific tokenizer; character-count heuristics would produce misleading
context-window enforcement. The model's configured `ContextWindow` remains
metadata until tokenizer integration is introduced.

## Streaming policy

- Models must explicitly set `SupportsStreaming`.
- A nil provider stream is rejected before a consumer goroutine is started.
- Events receive the resolved provider-instance and internal model IDs.
- Usage values are normalized; completed responses are normalized using the
  same response path as Generate.
- Error events always contain a normalized error.
- Unknown event types become `StreamEventError` events.
- The forwarding goroutine stops after `completed`, `error`, source closure,
  or request-context cancellation.

## Error policy

The Gateway maps context cancellation/deadline, HTTP authentication,
permission, invalid request, not found, rate limit, timeout, server failure,
and network timeout into `provider.Error`. Retryability is metadata only;
M1-C deliberately does not retry requests because retries require an explicit
idempotency and billing policy.

## Tests added

- Internal model/provider identity normalization.
- Output-token ceiling enforcement before adapter invocation.
- Disabled model/provider rejection.
- Context deadline normalization and provider-instance identity rewrite.
- Stream event identity normalization and terminal-event handling.
- Streaming capability enforcement.
- Unknown stream event conversion.
- Nil response handling.
- NaN / infinity rejection for temperature and top-p.
