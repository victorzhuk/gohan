# Design notes: m0-core

Companion to `proposal.md` and `tasks.md`; the capability specs remain the contract. This file records the M0-specific choices an implementer would otherwise have to make alone.

## Package plan inside the root module

```
core/           package gohan      driver only: Build/Stack/options, Flow/FlowFunc/Conversation, Drive/DriveResume,
                                   Recover, Inspect, Explain, NewTool, and type aliases for the vocabulary declared
                                   below (ADR-0139). Declares no shared type itself.
core/types/     package types      value types and their enums, ports: Seq, Origin, Message/Block/Role,
                                   ToolUse/ToolResult/Outcome, ModelRequest/ModelChunk/Usage, Event/EventMeta/StopReason,
                                   error classes, sentinels, ErrorCode/Problem, Effect/Trust/ToolSpec/Capabilities,
                                   EgressPolicy, RunInfo/Principal/CostTags/RunLimits/RunMode, Caps/ModelProfile/
                                   Pricing/LatencyClass, Model/Tool/Decider/CredentialSource/Waker/ContextProvider
core/stores/    package stores     the nine store ports, memory implementations, RunState, lease constants,
                                   RetentionPolicy
core/chains/    package chains     Step/StepKind/ToolChain/ModelChain, ordering validation, StepError, PromptSet
core/guards/    package guards     GuardInput, GuardAction, OutputMode, stages, GuardBlockedError
core/permission/ package permission gate skeleton, ApprovalPolicy, ApprovalRequest, Verdict, scope and grant types
core/suspension/ package suspension SuspendError, reasons, ResumeInput constructors
core/streams/   package streams    event kinds and payloads, Done, RunNotice/NoticeKind, deltas, StreamBuffer,
                                   Attach, Collect/Last/Drain
core/runtime/   package runtime    Stepper, Runtime, State, Status, native runtime
core/flowdef/   package flowdef    definition model only (M4 fills it)
std/            package std        presets and DefaultPrompts
std/permission  std/guard  std/structured  std/limit  std/retry  std/notes  std/outputs  std/toolsearch
                std/keys  std/route  std/tokens
std/telemetry   std/tool/exec  std/state  std/context (Truncate only in M0)
testkit/gohantest  testkit/conformance  testkit/storetest
examples/quickstart  examples/excursions
```

Imports run downward only: `types` is the floor, then `stores`/`chains`/`guards`/`permission`/`suspension`/`streams`, then `runtime`, then the driver package. No leaf imports the driver. Each enum lives with its own package, so the eight same-name pairs the review found (ADR-0139) cannot collide. `std/*` are separate packages so depguard can forbid `std` imports from `core/**`.

## Ordering rationale

Tasks A–C build governance (types, stores, gate, journal, guards) before any runtime exists; the first end-to-end model call happens in task 22. This follows the proof → identity → verification → scaffold order (ADR-0098) and means every later task lands on already-governed components.

## Memory implementations

Every port ships a memory implementation in `core` (not `std`): they are the reference semantics the `storetest` suites are written against, and they make `quickstart` dependency-free. They must satisfy the same concurrency scenarios as `adapter/postgres` (`stores.concurrent-*`, `stores.lease-exclusivity`); use `sync.Mutex` and versions, no channels.

## Test strategy for M0

- Subtest per scenario ID; table-driven where several IDs share a fixture.
- `gohantest.ScriptedModel` for every model interaction; no network in `-short`.
- `synctest` for every time-dependent scenario (leases, expiry, wall clock, windows).
- Goroutine-leak profile in conformance suites.
- `storetest` runs against memory implementations in M0 and is reused unchanged by `adapter/postgres` in M3.
- Benchmarks in `core` for the paths in `openspec/specs/performance`; baselines recorded on the reference machine (CI runner class) in task 32.

## Known deferrals

Persisted compaction (`context`), sub-flow contract (`subflows`), taint enforcement (`taint`), provider adapters, Postgres/Redis stores. Hooks for each exist in M0 (`ContextPolicy` shape, `Checkpoint.Child`, `TaintHook`, `Capabilities`) so their later changes are additive.
