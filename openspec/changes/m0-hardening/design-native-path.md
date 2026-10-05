# Design: the governed native construction path

Chunk 5 of `tasks.md`. Authored from a read-only design pass over `Build`, the driver, the runtime contract and the `std` components; no code was changed. Proposed identifiers do not exist yet.

# Design
**Title:** Governed native construction from one resolved Stack configuration

**Tier:** Standard-depth proposal with one unavoidable new pattern

**Status:** Read-only design. No files changed. No builds, tests, or formatters executed. Proposed identifiers below do not describe existing APIs.

**Testing mode:** existing-service-strict

**Summary:** Register native definitions before Build. Build resolves an immutable execution configuration. Public constructors obtain that configuration and create a fresh runtime for each invocation. Extract the existing governed turn implementation into model and batch effects. DriveLifecycle remains the only execution loop.

## Public seam
**Constructor:** func NewNativeConversation(stack *Stack, name string, opts ...ConversationOption) (Conversation, error)

**Location:** core/native_conversation.go, package gohan

**Registration:** func WithNativeAgent(spec NativeSpec) Option

**Registration location:** core/native_options.go, package gohan

**Definition:** NativeSpec is driver construction data. It contains Request FlowRequest, Profile string, Instruction []types.Block, Tools []types.Tool, Assemble func(context.Context, types.AssembleInput) (types.ModelRequest, error), ModelChain chains.ModelChain, and ToolChain chains.ToolChain. Request.Tools is derived from Tools, not independently supplied.

### Service sequence
- The service supplies models, stores, prompts, chains, and native definitions to Build.
- Build indexes models by Profile().Name and resolves each registered definition.
- The service calls NewNativeConversation(stack, name).
- The service calls Send, Continue, or Resume without supplying a Stepper.

### Stack resolution
- The constructor looks up stack.resolved[name]. It does not accept another model or another strategy map.
- The resolved definition holds the selected model, primary and fallback profiles, StrategyPlan, fidelity decisions, tool registry, named chains, assembler, prompts, and limits.
- The invocation factory obtains store ports, credentials, approval policy, and telemetry from the same Stack.
- The invocation factory supplies governed Model and Tools, loaded History, Assemble, Input, Mode, and Save through runtime.AgentRun.
- Each invocation creates its own runtime. A Conversation must not share a runtime that retains AgentRun across concurrent sessions.

**Compatibility:** Keep NewConversation(stack, spec, rt, opts...) as the foreign-runtime and conformance seam. It is not an obsolete alias. Native examples stop using it with custom steppers.

**New pattern:** This is a new native construction pattern, not a wiring repair. Existing Flow and Conversation interfaces remain the consumer boundary. The existing Runtime interface remains the backend boundary.

**Placement reason:** ADR-0139 puts public construction in the driver and native runtime mechanics in core/runtime. Assembly policies, prompt defaults, and limit middleware remain in std.

### Options considered
- option: Wrap driveTurns in one runtime.Step; decision: Rejected; reason: driveTurns executes multiple model calls and batches. This violates GranularityEffect and prevents lifecycle work between effects.
- option: Create a separate native agent loop; decision: Rejected; reason: A second loop duplicates cancellation, batch, repair, and suspension behavior.
- option: Extract effects from driveTurns and invoke them through DriveLifecycle; decision: Chosen; reason: This preserves governed behavior while exposing the required model and batch boundaries.

### Evidence
- core/build.go:47-89
- core/runtime/runtime.go:35-79
- core/conversation.go:115-140
- core/conversation.go:300-339
- core/flow.go:17-32
- docs/adr/0139-core-is-a-package-tree.md:7-29
- docs/design/architecture.md:25-47
- openspec/changes/m0-hardening/design.md:95-98

## Effect step
**Runtime location:** core/runtime/native.go

**Mechanism:** The runtime stores serializable phase data in State.Backend. Driver-supplied model and batch callbacks execute effects. The runtime leaf never imports core/.

### Model step order
- Load history at State.HistoryVersion through the driver callback.
- Build AssembleInput from the resolved configuration and current RunInfo.
- Apply the resolved assembly and request preparation functions.
- Apply resolved output allowance, structured strategy, and provider hints.
- Execute exactly one governed model invocation with the supplied context.
- Retain the existing delta, reasoning, complete-call, truncation, repair, and assistant rendering behavior.
- Increment State.Turn exactly once for the model invocation.
- If calls contain Idempotent or SideEffect effects, append the assistant message before the next batch step.
- If calls are only ReadOnly, retain the assistant message in serializable phase data until the batch settles.
- For a final answer, append the assistant message and return DoneStatus. The lifecycle emits Done after Finish.

### Batch step order
- Reserve the complete batch against the run ledger before any tool executes.
- Gate every call before execution.
- Execute allowed calls through runtime.Batch and its scheduler.
- Append results in call order and advance HistoryVersion.
- For ReadOnly-only batches, append the retained assistant message and results in one append.
- For an Ask decision, retain pending calls and return SuspendError with the advanced state.
- After a settled batch, let the lifecycle drain signals before another model effect.

**Reuse:** Refactor driveTurns and runCalls into shared effect functions. Preserve their parsing and event behavior. Remove their outer model loop after existing tests migrate to the native path.

### Change files
- core/drive_turn.go
- core/runtime/native.go (new)
- core/native_effects.go (new, driver binding and append callbacks)
- core/drive_lifecycle.go
- core/conversation.go
- core/resume.go
- core/recover.go

### Ownership
- Component events use the sink. Step returns only runtime-originated events.
- DriveLifecycle owns heartbeat, mailbox processing, checkpoint writes, terminal transitions, and Done.
- The native runtime owns phase advancement, not store access.
- Journal and cancel-shield behavior has one owner per configured path. The scheduler must not repeat middleware journal operations.

### Important repairs
- runCalls currently omits Scheduler configuration. Wire EffectOf, Parallel, and MaxParallel from the resolved configuration.
- CallTool must receive the original ToolUse identity through the governed tool wrapper. Do not execute chains with a zero ToolUse.
- Preserve cancellation, AbortError, SuspendError, and LimitExceededError as control errors. Do not convert them into ordinary tool failures.
- The current lifecycle drains signals on DoneStatus. Add the required post-batch safe point without treating a batch as a terminal run.

### Evidence
- core/drive_turn.go:63-239
- core/drive_turn.go:251-331
- core/runtime/runtime_batch.go:73-126
- core/runtime/runtime_schedule.go:16-122
- core/tool_call.go:36-75
- core/chains/chain.go:111-143
- core/drive_lifecycle.go:273-367
- openspec/specs/runtime/spec.md:77-139
- openspec/specs/runtime/spec.md:202-205

## Resolved configuration
**Type:** resolvedNativeConfig

**Location:** core/native_resolve.go, package gohan

**Computed:** Build computes one value per registered NativeSpec after all options apply. Stack stores the values in an immutable map keyed by flow name.

### Contents
- Selected model and validated profile snapshots
- StrategyPlan and fidelity decisions
- Registered tools and their spec lookup
- Named model and tool chains with evaluated Applies results
- Assembler and pure request preparation function
- PromptSet and required prompt fields
- Resolved RunLimits
- Instruction, context providers, and filter declarations
- Expected persistence shape and runtime granularity

**Single source:** Execution, Explain, the startup matrix, and release hashing project this value. None independently resolves a profile or rebuilds a chain.

**Middleware:** Existing WithModelMiddleware entries become named KindUser entries in option order. Generated names use model-middleware-1, model-middleware-2, and subsequent indexes. The first entry remains outermost.

**Explain signature:** func (s *Stack) Explain(f any) chains.Explanation

**Explain location:** core/explain.go

**Explain handle:** Native Conversation and native Flow handles retain their resolved configuration identity. Explain uses that identity to find the configuration.

**Explanation extensions:** Extend chains.Explanation with separate model and tool step lists, strategies, fallback, limits, granularity, expected writes, and a sample ModelRequest. Preserve the existing fields.

**Request consistency:** The same pure prepareRequest function produces execution and sample requests. Move preset prompt insertion into this function instead of captured middleware text.

**Startup consistency:** WithNativeAgent registration precedes Build. Build logs all resolved entries once. A constructor must not mutate the Stack manifest or append another startup matrix record.

**Release consistency:** Populate manifest Chains and Definitions from stable configuration data. Do not hash Go function addresses. Manifest.ID, run release attributes, and Explain.Release use the same identifier.

### Evidence
- core/build.go:104-149
- core/build.go:156-207
- core/build_fidelity.go:30-66
- core/build_matrix.go:7-43
- core/chains/explain.go:17-36
- core/release_manifest.go:18-35
- core/release_manifest.go:47-105
- core/build_options.go:108-116
- openspec/specs/build/spec.md:27-59
- openspec/specs/build/spec.md:91-94
- openspec/specs/chains/spec.md:158-163

## Run accounting
**State type:** chains.LimitsState

**Creation:** The driver invocation factory creates one state after acquiring an independent run lease and before the first effect. It records the monotonic start then.

### Context functions
- func WithLimitsState(ctx context.Context, state *LimitsState) context.Context
- func LimitsStateFrom(ctx context.Context) (*LimitsState, bool)

**Context location:** core/chains/limits_context.go

**Middleware access:** Model and tool limit middleware obtain the state from the invocation context. Presets contain configuration and middleware functions, never a captured accumulator.

**Tree sharing:** An explicit child invocation calls parentState.Branch(). Branch creates local turn, tool, warning, and elapsed state while sharing the root cost ledger. An ordinary invocation always creates a root ledger, even when its input context contains another run.

**Concurrency:** Initialize the shared tree pointer before concurrent calls. Branch must not lazily assign an unsynchronized tree pointer.

**Counting:** The ledger reserves a batch once. Individual app tools consume their reserved slots without charging again. Provider tool usage contributes to the same tool total. Denied and Ask calls retain batch reservations.

**Cost:** Price reported usage using the actual selected profile. Root Done.Cost uses TreeCost, not only the root's local spend.

**Resume:** Resume and recovery restore an accounting snapshot for the same run. They do not allocate an empty budget. Serializable phase data contains counters, cost, warnings, and elapsed allowance, not a mutex or context.

**Policy ownership:** Keep the ledger and context vocabulary in core/chains. Move executable Limits and ToolLimits policy into std/limit. Move concrete default values into std. Core validates explicit resolved limits.

### Evidence
- std/presets.go:32-67
- std/presets.go:104-121
- core/chains/limits.go:15-70
- core/chains/limits.go:89-108
- core/chains/limits.go:125-150
- core/chains/limits.go:174-265
- core/limits.go:10-47
- core/runtime/runtime_batch.go:73-98
- openspec/specs/limits/spec.md:45-51
- openspec/specs/limits/spec.md:64-84

## Prompt accounting
**Preset:** Preset.Options registers WithPrompts(p.Prompts). It installs named chain data and prompt-consumer declarations rather than closures that capture another PromptSet.

**Replacement:** Options apply in order. A later WithPrompts replaces the complete set. Execution reads only the final resolved set.

**Recipe:** Extract and Classify obtain the selected governed model and PromptSet from the Stack. Their repair callback reads PromptSet.RepairInstruction. Remove the private repair literal.

**Recipe surface:** Align recipes with the documented Stack/profile construction surface. Retain Flow as their public result. Constructor validation errors must surface before the first provider call.

**Manifest:** Hash every PromptSet field and Version from the final set. A used repair prompt change changes Stack.Manifest().ID().

**Missing prompt:** Build refuses a declared prompt consumer when its required field is empty. The refusal names the consumer and field.

**Empty chain:** Core-only execution adds no library prompt text. Schema generation is request data, not an excuse to insert a repair sentence.

### Evidence
- std/presets.go:17-28
- std/presets.go:116-156
- std/flow/extract.go:115-143
- core/build_options.go:61-69
- core/release_manifest.go:59-79
- openspec/specs/chains/spec.md:161-163
- openspec/specs/chains/spec.md:204-212
- openspec/specs/flow/spec.md:81-92
- docs/design/scenarios.md:117-136

## Behavior contract
- seam: Construction and resolution; seeding: Register NativeSpec and models through Build options. Obtain the handle through NewNativeConversation.; budgets: Unsupported configuration causes 0 provider calls.; refusals: Build refuses registered invalid combinations. NewNativeConversation refuses an unknown registered name before invocation.; red tasks: Record the configured middleware trace and assert 0 provider calls for rejected definitions.; coder tasks: Resolve definitions and construct native handles from the resolved map.
#### Tasks
- C01
- C05

#### States
- Registered native definition
- Resolved immutable configuration
- Rejected construction

#### Transitions
- Valid registered profile and definition -> resolved configuration -> set.
- Unsupported constrained strategy, provider tool, fallback, or fidelity -> construction failure -> forced.
- Unknown native name -> constructor failure -> forced.
- Caller mutation after Build -> existing resolved configuration -> no-op.

#### Forbidden
- A rejected construction must never call a provider.
- A constructor must never replace the model selected by Build.

#### Names
- WithNativeAgent
- NativeSpec
- NewNativeConversation
- resolvedNativeConfig
- ErrStrategyUnsupported
- ErrProviderToolUnsupported
- ErrFallbackUnknown
- ErrFallbackIncompatible
- ErrFidelityUndeclared
- ErrFidelityDropped

#### Scenarios
- build.impossible-combination
- build.provider-tool-unsupported
- build.blob-caps
- build.opaque-compaction-fallback
- build.resolved-matrix

#### Evidence
- core/build.go:156-207
- core/build_fidelity.go:30-66
- openspec/specs/build/spec.md:69-94

- seam: Native effects and lifecycle; seeding: Use Build, NewNativeConversation, and Send with a scripted model. Reach suspended state through a real Ask decision.; budgets: Each Step executes 1 model call or 1 batch. MaxTurns=3 permits 3 model calls. MaxParallelTools=2 permits at most 2 concurrent ReadOnly tools.; refusals: The driver rejects incomplete calls before tool execution. The batch rejects over-reservation before any execution. The lifecycle persists suspension before delivery.; red tasks: Assert public event ordering, append ordering, original call identity, resume replay, and a second suspension.; coder tasks: Extract the existing effects and connect per-run runtime factories to initial, resumed, and recovered execution.
#### Tasks
- C02
- C03
- C06
- C07
- C08
- C09

#### States
- Model phase
- Pending batch phase
- Suspended batch
- Completed run

#### Transitions
- Model text without calls -> appended assistant -> set DoneStatus.
- Model with complete calls -> pending batch -> set Continue.
- Truncated call with available allowance -> repair model phase -> set without tool execution.
- Allowed batch -> ordered appended results -> clear pending calls.
- Ask batch -> checkpoint state -> forced suspension.
- Cancellation at a safe point -> stopped run -> forced.
- Model-call count reaches 3 with MaxTurns=3 -> StopLimit -> forced.

#### Forbidden
- One native Step must never execute a model invocation and a tool batch.
- A delta fragment must never execute as complete tool arguments.
- A SideEffect call must never precede its pending assistant append.
- The runtime must never emit Done before Runs.Finish.

#### Names
- runtime.GranularityEffect
- runtime.Continue
- runtime.DoneStatus
- runtime.Batch
- types.SuspendError
- types.StopLimit
- types.StopCompleted
- runtime.NotExecutedPrefix

#### Scenarios
- runtime.plain-answer
- runtime.tool-round-trip
- runtime.append-before-tool
- runtime.suspend-order
- runtime.done-after-finish
- runtime.batch-limit-before-execute
- runtime.batch-ask-after-allowed
- runtime.parallel-tools-cap
- runtime.max-turns
- flow.steer-applied-at-boundary

#### Evidence
- core/drive_turn.go:108-235
- openspec/specs/runtime/spec.md:194-212
- openspec/specs/runtime/spec.md:259-297
- openspec/specs/runtime/spec.md:304-328

- seam: Run and tree accounting; seeding: Create roots through public invocation. Create children through the explicit driver child factory. Create restored state through suspension and Resume.; budgets: Three spends of 0.04 exceed a tree MaxCost of 0.10. Three provider searches exhaust MaxToolCalls=3. The fourth tool call executes 0 times.; refusals: Limit policy refuses excessive spending after charging and before the next model call. Batch reservation refuses excessive tools before execution.; red tasks: Reuse one preset across sequential and concurrent independent runs. Assert tree cost and resumed budget continuity.; coder tasks: Remove captured state and use context-bound ledgers with explicit branching and snapshots.
#### Tasks
- C04
- C10

#### States
- Independent root ledger
- Child ledger with shared cost
- Restored ledger

#### Transitions
- New independent run -> new root ledger -> set.
- Explicit child run -> local ledger with parent tree cost -> set.
- Model usage -> local and tree cost -> set.
- Batch reservation -> tool total -> set once.
- Resume same run -> restored counters -> set without reset.
- Next independent run -> previous run ledger -> no-op.

#### Forbidden
- Two independent runs must never share counters or elapsed start.
- A batch reservation must never charge an app tool twice.
- A resume must never reset spent cost.

#### Names
- chains.LimitsState
- WithLimitsState
- LimitsStateFrom
- Branch
- TreeCost
- types.LimitExceededError
- MaxCost
- MaxToolCalls

#### Scenarios
- limits.cost-accumulates
- limits.hard-cost-abort
- limits.provider-calls-count
- limits.wall-clock
- limits.wall-clock-monotonic
- limits.limits-under-foreign-backend

#### Evidence
- core/chains/limits.go:49-70
- core/chains/limits.go:125-165
- openspec/specs/limits/spec.md:64-94

- seam: Prompts and explanation; seeding: Build with preset options and an overriding PromptSet. Compare Explain with the captured request from a public invocation.; budgets: Explain causes 0 provider calls. WithRepairs(1) permits 2 model attempts.; refusals: Build refuses missing fields for declared prompt consumers before any model invocation.; red tasks: Change only RepairInstruction and assert a changed manifest and changed repair request. Compare sample preparation with captured execution.; coder tasks: Register final prompts and derive requests and explanation from the same resolved configuration.
#### Tasks
- C11
- C12
- C13

#### States
- Final PromptSet
- Resolved explanation
- Prepared sample request

#### Transitions
- Preset followed by caller WithPrompts -> caller set -> set.
- Repair attempt -> named RepairInstruction -> set.
- Used prompt changes -> release identity -> set.
- Explain -> execution counters and provider calls -> no-op.

#### Forbidden
- A private library repair literal must never reach a model.
- Explain must never invoke a provider or spend a run budget.
- Explain must never resolve a different chain from execution.

#### Names
- chains.PromptSet
- RepairInstruction
- WithPrompts
- Stack.Explain
- chains.Explanation
- Stack.Manifest

#### Scenarios
- chains.empty-chains
- chains.prompt-strings-accounted-for
- chains.preset-is-copyable
- flow.extract-recipe
- flow.classify-recipe

#### Evidence
- std/flow/extract.go:26-35
- std/flow/extract.go:115-143
- std/presets_test.go:17-48
- std/flow/extract_test.go:61-88
- openspec/specs/chains/spec.md:204-217


## Workstream rules
**Size:** Each chunk targets one 20-minute implementation session. These are planning bounds, not measured implementation times.

**File ownership:** Each listed implementation file belongs to exactly one chunk. Regression files are dedicated to their chunk. Existing tests migrate only under their file owner's chunk.

**Landing order:** C01 first. C02-C05 next. C06 follows C02/C03/C05. C07-C09 follow C04/C06. C10-C12 follow the resolved and accounting primitives. C13 follows prompt preparation. C14-C16 follow the public path.

**Integration:** Do not claim native completion until Send, Continue, Resume, and Recover use fresh governed run factories. The main agent performs verification after integration.

### Verification commands
- timeout 5m task lint
- timeout 15m task test
- timeout 10m task spec:gate
- timeout 5m task api:check
- timeout 5m task examples:test
- timeout 2m go -C examples run ./quickstart

**Recommended executor:** go-coder

### Evidence
- openspec/changes/m0-hardening/plan.md:136-138
- openspec/changes/m0-hardening/plan.md:200-214
- openspec/changes/m0-hardening/plan.md:230-247

## Blockers and decisions
- The limits ownership contract requires an ADR amendment before moving policy from core to std.
- The native and general limit contracts disagree on MaxTurns termination. Seal native StopLimit behavior before writing red tests.
- Registration before Build is necessary to satisfy the single complete startup matrix requirement.
- Do not promise full canonical preset governance from the current passthrough gate, journal, hooks, and telemetry entries.
- Do not promise exact provider-request explanation for arbitrary request-mutating middleware without a pure preparation contract.

## Risks
- risk: Refactoring the turn loop changes truncation or repair behavior.; mitigation: Move existing branches without rewriting their policy. Preserve consumer regressions through the public native path.
- risk: Concurrent sessions share runtime state.; mitigation: Use a fresh runtime and ledger for every invocation.
- risk: Journal middleware and the scheduler both reserve calls.; mitigation: Assign journal execution to one configured owner and test one reservation per side effect.
- risk: Prompt overrides affect the manifest but not execution.; mitigation: Resolve the final PromptSet once and remove captured prompt insertion.

# Chunks
- C01 — Seal contracts and ownership in a new ADR plus flow/runtime/build/limits spec edits. Resolve blockers before implementation. Regression: declared native construction and termination rules. No executable behavior in this chunk.
- C02 — Own core/drive_turn.go and its existing turn tests. Extract modelEffect and batchEffect without changing parsing, repair, or truncation behavior. Depends C01. Regression: no tool executes from a fragment or truncated call.
- C03 — Own new core/runtime/native.go and native_test.go. Add serializable model/batch phase advancement with GranularityEffect and driver callbacks. Depends C01. Regression: one Step never performs both effects.
- C04 — Own core/chains/limits.go, its tests, and new limits_context.go. Retain an inert ledger, add context access and snapshots, and make tree initialization concurrency-safe. Depends C01. Regression: independent ledgers remain isolated while Branch shares cost.
- C05 — Own core/build.go, core/build_options.go, new native_options.go/native_resolve.go, and build resolution tests. Add NativeSpec registration and immutable resolved configurations. Invoke strategy, fidelity, and chain validation. Depends C01. Regression: configured middleware is retained in the resolved chain and invalid definitions cause 0 provider calls.
- C06 — Own new core/native_effects.go and its tests. Bind extracted effects to resolved assembly, original call identities, append shapes, and Scheduler configuration. Depends C02/C03/C05. Regression: side-effect pending messages precede execution and batch results retain call order.
- C07 — Own core/conversation.go, new native_conversation.go, and dedicated native conversation tests. Add NewNativeConversation and invocation factories with complete Stack stores and AgentRun. Depends C04/C06. Regression: Build middleware changes observed Send execution and concurrent sessions do not share runtime state.
- C08 — Own core/drive_lifecycle.go and dedicated native lifecycle tests. Add the post-batch safe point, restored accounting context, and Done.Cost projection without another loop. Depends C04/C06. Regression: a steer reaches the next request and Done follows Finish.
- C09 — Own core/resume.go, core/recover.go, and dedicated native recovery tests. Obtain fresh governed runtimes and restored ledgers from the resolved configuration. Depends C07/C08. Regression: resume or recovery can suspend again without repeating completed tools or resetting cost.
- C10 — Own new std/limit/run.go and run_test.go. Move executable model/tool limit policy to context-bound ledgers and support batch reservation consumption. Depends C04. Regression: two independent runs can each spend their full budget and provider calls share the tool total.
- C11 — Own std/presets.go, std/presets_test.go, and new std/prompts.go. Remove captured state and prompts. Register final PromptSet and honest named chain data. Depends C05/C10. Regression: a later caller PromptSet changes actual requests rather than only manifest hashes.
- C12 — Own new core/native_flow.go, core/flow.go, std/flow/extract.go, and recipe tests. Supply the Stack/profile recipe path and reuse native model effects with schema, validation, and named repair callbacks. Depends C06/C10/C11. Regression: typed extraction offers no tools and repairs use the caller's RepairInstruction.
- C13 — Own new core/explain.go, core/chains/explain.go, core/release_manifest.go, core/build_matrix.go, and dedicated explanation tests. Project resolved configuration into Explain, manifests, and startup entries. Depends C05/C11/C12. Regression: Explain and captured request preparation agree and used prompt changes alter release identity.
- C14 — Own examples/quickstart/main.go, main_test.go, and README. Remove quickstartRuntime and manual assistant persistence. Depends C07/C08/C13. Regression: the shipped Run entry calls Build and the governed native constructor and preserves Hello, quickstart!.
- C15 — Own examples/excursions/main.go, main_test.go, and README. Remove tripRuntime and manual batch persistence. Preserve fixture tools and policy through the governed native path. Depends C07/C08/C09/C11. Regression: denied booking never executes and persisted history contains its ordered not_executed result.
- C16 — Own docs/design/architecture.md, docs/design/scenarios.md, and CHANGELOG.md. Document the constructor, final prompt replacement, per-run accounting, and real quickstart imports. Depends C12-C15. Regression: documentation names the shipped public entry rather than a custom Stepper.

# Gaps
- ARCH-01 through ARCH-05 do not occur in the supplied hardening change directory. plan.md contains R09, R11, R12, and R21, but no ARCH identifiers. Their exact findings remain unavailable.
- Build cannot log all flow resolutions when constructors introduce definitions only after Build. The proposal resolves this through WithNativeAgent registration before Build.
- The limits spec declares concrete defaults in core while architecture §4.2a assigns defaults and policy to std. The owner must approve an ADR amendment before a clean ownership migration.
- runtime.max-turns requires Done(StopLimit), but the limits contract and current middleware use LimitExceededError for excessive turns. The proposal selects StopLimit for the native pre-effect boundary, but this requires a sealed contract decision.
- Arbitrary ModelMiddleware can change requests, perform effects, or vary by context. Existing chain data does not provide a pure request projection. Exact Explain equality after such middleware is not derivable from these sources.
- Explanation has no sample input, request, strategy, limits, or persistence fields. The sources do not define how Explain(f any) receives caller-selected sample input or reports preparation errors.
- The current preset's telemetry, gate, hooks, and journal entries are passthrough functions. Connecting them does not implement canonical governance. Real policy wiring must be specified separately rather than described as already present.
- The recipe API currently accepts a Model, while the documented API accepts Stack and profile. The constructor error policy and typed native Flow construction are not defined by a reusable implementation.
- The sources do not define durable accounting snapshot fields or cross-process reconstruction of a shared tree cost ledger. Local Branch sharing does not prove resumed multi-process tree accounting.
- runtime.Batch returns ErrBatchOverrun, while its scenario requires LimitExceededError. The driver needs a sealed translation rule before regression tests name the error.
- The scheduler and std.Journal provide different reservation paths. std.Journal currently continues after Reserve failure and ignores Complete errors. Full side-effect safety cannot follow from choosing that middleware unchanged.
- std.ReadBackResult also appends an authored sentence outside PromptSet. Removing only the recipe repair literal does not satisfy the universal no-hidden-library-string contract.
- The sequential-tools hint lacks an explicit ModelOptions field in the inspected request type. The native proposal must use a sealed provider-neutral field or validated adapter mapping, not an invented Extra key.
- Conversation and lifecycle files changed externally during this review. The current inspected ranges include earlier hardening repairs. The proposed chunks must coordinate with those owners instead of replacing their changes.
- Twenty-minute chunk bounds are unmeasured. C05, C09, C12, and C13 need narrow briefs after the listed contract gaps close. Do not dispatch them as complete executable specifications.

# Evidence
- openspec/changes/m0-hardening/plan.md:95-106 — R09, R10, R11, R12, and policy ownership findings.
- openspec/changes/m0-hardening/plan.md:136-138 — workstreams require bounded chunks before dispatch.
- openspec/changes/m0-hardening/plan.md:200-214 — governed native construction scope and acceptance.
- openspec/changes/m0-hardening/design.md:95-98 — open native constructor decision.
- docs/adr/0139-core-is-a-package-tree.md:7-29 — driver and leaf package ownership.
- docs/design/architecture.md:25-47 — governed component direction and core budget rule.
- docs/design/scenarios.md:93-136 — canonical Stack/profile recipe and empty-chain quickstart.
- core/build.go:47-89 — flow request, strategy plan, and Stack dependencies.
- core/build.go:104-149 — current Build and empty matrix record.
- core/build.go:156-207 — strategy and fallback resolution.
- core/build_options.go:39-69 — stores, recovery runtime, and PromptSet options.
- core/build_options.go:108-116 — first middleware is outermost.
- core/build_fidelity.go:30-66 — primary and fallback fidelity validation.
- core/build_matrix.go:7-43 — resolution matrix projection.
- core/release_manifest.go:18-35 — current manifest and ID.
- core/release_manifest.go:47-105 — prompt hashing and separate matrix identity helper.
- core/drive_turn.go:63-239 — existing governed model loop and rendering.
- core/drive_turn.go:251-331 — batch path and missing scheduler wiring.
- core/runtime/runtime.go:23-79 — State, Runtime, granularity, and governed AgentRun.
- core/runtime/runtime_batch.go:73-126 — reservation, gate-all, scheduling, and asks.
- core/runtime/runtime_schedule.go:16-122 — scheduler configuration and journal execution.
- core/conversation.go:115-140 — existing public runtime-taking constructor.
- core/conversation.go:300-364 — updated lifecycle appender and incomplete AgentRun.
- core/drive_lifecycle.go:208-235 — Start and restored state.
- core/drive_lifecycle.go:273-367 — effect loop and terminal handling.
- core/drive_lifecycle.go:472-517 — terminal signal drain and Finish ordering.
- core/resume.go:311-366 — resumed AgentRun and public event relay.
- core/recover.go:132-179 — recovery runtime lookup and AgentRun construction.
- core/flow.go:17-32 — Flow and FlowFunc consumer seams.
- core/chains/chain.go:111-143 — tool composition currently executes a zero ToolUse.
- core/chains/model_chain.go:28-53 — model-chain ordering validation.
- core/chains/explain.go:17-36 — current Explanation shape.
- core/chains/limits.go:15-70 — local and tree accounting with lazy Branch initialization.
- core/chains/limits.go:89-108 — usage pricing and tree charging.
- core/chains/limits.go:125-165 — turn, wall-clock, and cost checks.
- core/chains/limits.go:174-265 — executable core limit middleware.
- core/limits.go:10-47 — concrete core defaults and limit validation.
- core/tool_call.go:36-75 — tool resolution and control-error propagation.
- core/types/model.go:8-15 — shared Model concurrency contract.
- core/types/model_request.go:10-27 — request and model options.
- core/types/assembly.go:24-41 — complete AssembleInput.
- std/presets.go:17-28 — default library prompts.
- std/presets.go:32-77 — preset-captured accounting state and passthrough steps.
- std/presets.go:104-156 — captured model limits and prompt insertion.
- std/presets_test.go:17-48 — explanation test constructs Explanation directly.
- std/presets_test.go:143-192 — limit and prompt tests invoke middleware directly.
- std/flow/extract.go:42-94 — Model-taking recipe constructors.
- std/flow/extract.go:115-143 — repair loop and private authored literal.
- std/flow/extract_test.go:61-88 — typed recipe request assertions.
- std/journal.go:73-136 — journal reservation and completion behavior.
- std/journal.go:141-145 — additional private model-facing read-back sentence.
- std/shield.go:10-37 — std cancel-shield policy.
- examples/quickstart/main.go:42-151 — custom runtime and manual persistence.
- examples/excursions/main.go:23-216 — custom runtime, batch, and manual persistence.
- openspec/specs/flow/spec.md:81-92 — documented constructors and recipe surface.
- openspec/specs/flow/spec.md:159-169 — extract and classify consumer scenarios.
- openspec/specs/runtime/spec.md:77-139 — native effect boundary, sole driver loop, lifecycle, and batch ordering.
- openspec/specs/runtime/spec.md:194-212 — suspension, append, and terminal ordering scenarios.
- openspec/specs/runtime/spec.md:259-297 — batch and concurrency scenarios.
- openspec/specs/runtime/spec.md:304-328 — plain answer, tool round trip, MaxTurns, and cancellation.
- openspec/specs/chains/spec.md:66-108 — explicit chain semantics and canonical orders.
- openspec/specs/chains/spec.md:158-163 — Explain and prompt accounting contract.
- openspec/specs/chains/spec.md:204-217 — empty chains, accounted prompts, and named failures.
- openspec/specs/build/spec.md:27-59 — resolution precedence and constructor validation.
- openspec/specs/build/spec.md:69-94 — unsupported configurations and complete startup matrix.
- openspec/specs/limits/spec.md:45-51 — defaults, chain enforcement, and tree cost.
- openspec/specs/limits/spec.md:64-94 — tree cost, provider calls, and foreign backend scenarios.
- openspec/specs/telemetry/spec.md:14-18 — dependency-free telemetry and std policy ownership.
- openspec/specs/telemetry/spec.md:149-175 — backend tree and canonical attribute scenarios.
