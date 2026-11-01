# M2-E — Adapter Hot Reload

Credential Set/Delete operations call `ProviderRuntime.ApplyProvider` after persistence. The runtime builds a replacement adapter before publishing it. A failed build leaves the current provider and adapter unchanged; credential manager rollback restores the previous stored value.

`Registry.Replace` swaps one provider-instance adapter under its write lock. `Registry.Get` returns an interface reference to the adapter selected for that request. Replacing the map entry affects subsequent requests, while requests that already obtained the old adapter continue using it. Adapters are not closed during replacement because the current adapter contract has no lifecycle/retirement protocol. This avoids invalidating in-flight requests without adding unsafe eager cleanup.

Each Provider Instance owns its own adapter/client and credential resolution. Environment > Stored precedence remains in force. No process restart is required after credential updates.
