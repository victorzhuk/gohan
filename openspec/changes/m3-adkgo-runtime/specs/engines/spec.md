## ADDED Requirements

### Requirement: The adk-go backend is the third conformance backend
The `adapter/adkgo` module MUST implement the frozen runtime surface for `google.golang.org/adk/v2` and MUST pass the shared conformance suite beside `native` and `eino`. The backend MUST declare `StepGranularity: Turn` and MUST source component events and the turn counter from the governed decorators only.

#### Scenario: Conformance green on the third backend
ID: `engines.adkgo-runtime-conformance`
- WHEN `conformance.Runtime(t, newRuntime)` runs with the adk-go backend installed
- THEN every shared subtest passes on its public path with no scenario skipped and no adk-go-specific test outside the adapter module

#### Scenario: Message fidelity round trip
ID: `engines.adkgo-message-fidelity`
- WHEN the shared fidelity contract runs the adk-go codec functions as values
- THEN block order, every declared kind's origin and sequence, and every carried blob equal the input after a full round trip

#### Scenario: Foreign tool under the adk-go backend
ID: `engines.adkgo-foreign-tool`
- WHEN a foreign tool runs under the adk-go runtime
- THEN its identity, trust marking, spec declaration and result mapping match the assertions the eino leg already makes

### Requirement: Suspension crosses into adk-go's native mechanism
The backend MUST translate a component's suspend signal into adk-go v2 native pause/resume and back into `Suspended`, MUST propagate `ctx` unchanged into every component call, and MUST honour cancellation.

#### Scenario: Suspend and resume through the backend
ID: `engines.adkgo-suspend-bridge`
- WHEN a run suspends inside an adk-go graph step and resumes from the stored resume input
- THEN the backend re-drives the run from rebuilt state and the suspension and turn boundaries match the eino leg

### Requirement: Backend gaps fall back to Replay
For any capability the backend carries no mechanism for, the adapter MUST record the step result and resume by `Replay` re-executing `Step` over recorded results, and MUST declare the fallback per capability rather than falling back silently.

#### Scenario: Replay fallback for a missing backend capability
ID: `engines.adkgo-replay-fallback`
- WHEN a run resumes a capability the backend does not natively carry
- THEN `Step` is re-executed over recorded results producing the same `State` sequence up to the suspension point, and the adapter's declared fallback table names the capability

### Requirement: Workflow flows pass the foreign-backend suites
The adk-go workflow flows MUST pass the backend-swap and limits suites with unchanged test code, and the shared suites MUST keep consuming the backend only through the public path.

#### Scenario: Flow backend swap on adkgo
ID: `engines.adkgo-workflow-swap`
- WHEN the one acceptance suite runs an adk-go graph flow beside the other backends
- THEN it passes with the same test code and no per-backend branch

#### Scenario: Limits under the adkgo backend
ID: `engines.adkgo-limits-foreign`
- WHEN the limits scenarios run on an adk-go graph flow
- THEN the same limits hold as under the native and eino backends
