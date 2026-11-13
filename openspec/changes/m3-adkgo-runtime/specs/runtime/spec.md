## MODIFIED Requirements

### Requirement: Runtime conformance

`conformance.Runtime(t, newRuntime)` SHALL pass for `native`, `eino`, `adkgo`. The `adkgo` leg MUST run against the `google.golang.org/adk/v2` tag pinned in `adapter/adkgo/go.mod`. A backend MUST propagate `ctx` unchanged into every component call, translate suspend into its own mechanism and back into `Suspended`, honour cancellation, and MUST NOT emit component events or count turns.

#### Scenario: Third backend joins the conformance suite
ID: `runtime.adkgo-runtime-conformance`
- WHEN `conformance.Runtime(t, newRuntime)` runs for each of `native`, `eino` and `adkgo`
- THEN all three legs pass with no scenario skipped and no per-backend branch inside the shared suite
