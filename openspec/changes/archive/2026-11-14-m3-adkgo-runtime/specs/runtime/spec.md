## MODIFIED Requirements

### Requirement: Runtime conformance

`conformance.Runtime(t, newRuntime)` SHALL pass for `native`, `eino`, `adkgo`. The `adkgo` leg MUST run against the `google.golang.org/adk/v2` tag pinned in `adapter/adkgo/go.mod`. A backend MUST propagate `ctx` unchanged into every component call, translate suspend into its own mechanism and back into `Suspended`, honour cancellation, and MUST NOT emit component events or count turns.

#### Scenario: conformance suite passes for the native runtime
ID: `runtime.conformance-native`
- WHEN `conformance.Runtime` runs against the native runtime
- THEN the runtime passes the shared conformance suite

#### Scenario: conformance suite passes for the eino runtime
ID: `runtime.conformance-eino`
- WHEN `conformance.Runtime` runs against the eino runtime
- THEN the runtime passes the shared conformance suite

#### Scenario: conformance suite passes for the adk-go runtime
ID: `runtime.conformance-adkgo`
- WHEN `conformance.Runtime` runs against the adk-go runtime
- THEN the runtime passes the shared conformance suite against the `google.golang.org/adk/v2` tag pinned in `adapter/adkgo/go.mod`, with no scenario skipped and no per-backend branch inside the shared suite

#### Scenario: plain answer
ID: `runtime.plain-answer`
- WHEN the scripted model returns "hi"
- THEN events are `TextDelta*`, `AssistantMessage("hi")`, `Done(StopCompleted)`; `SessionLog` holds user + assistant

#### Scenario: tool round trip
ID: `runtime.tool-round-trip`
- WHEN the model calls `echo` then answers
- THEN `ToolStarted`, `ToolFinished`, second `AssistantMessage`, `Done`

#### Scenario: parallel calls ordering
ID: `runtime.parallel-calls-ordering`
- WHEN one message contains two tool calls
- THEN results reach the next model call in call order

#### Scenario: max turns
ID: `runtime.max-turns`
- WHEN the model calls a tool every turn and `MaxTurns=3`
- THEN `Done(StopLimit)` after 3 model calls and `gohan.max_turns.reached` increments

#### Scenario: cancellation
ID: `runtime.cancellation`
- WHEN ctx is cancelled mid-stream
- THEN the iterator yields `context.Canceled` and no further components run
