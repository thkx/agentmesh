# M2-F — Provider Connection Testing

`POST /api/v1/providers/{id}/test` now performs a bounded upstream Generate request using the first enabled model associated with the provider. It uses a 15-second deadline and a one-token output ceiling. This is a real upstream connectivity test and may incur provider usage or charges.

The endpoint returns latency and a generic failure message. It intentionally does not echo raw SDK/upstream errors because they may contain request details. Missing adapter, disabled provider, missing enabled model, timeout, and upstream failure have distinct HTTP outcomes.
