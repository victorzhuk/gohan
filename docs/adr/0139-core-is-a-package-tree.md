# core is a package tree, not one package

Status: accepted · Amends ADR-0056 (the module layout stands; the single-package clause does not) · Origin: review round, 2026-10-04

## Decision

`core` is a tree of packages. The driver package stays `core/` with package clause `gohan` and holds what users call directly: `Build`, `Option`/`Stack`, `Flow`/`FlowFunc`/`Conversation`, `Drive`/`DriveResume`, `Recover`, `Inspect`, `Explain`, `NewTool`, `Runnable`, and type aliases for the vocabulary whose declarations live lower. Value types, ports and enum members live in leaf packages that never import the driver:

| Package | Holds |
|---|---|
| `core/` (package `gohan`) | driver, options, aliases, no declarations of shared types |
| `core/types` | value types and their enums: `Seq`, `Origin`, `Message`/`Block`/`Role`, `ToolUse`/`ToolResult`/`Outcome`, `ModelRequest`/`ModelChunk`/`Usage`, `Event`/`EventMeta`/`StopReason`, error classes, sentinels, `ErrorCode`/`Problem`, `Effect`/`Trust`/`ToolSpec`/`Capabilities`/`EgressPolicy`, `RunInfo`/`Principal`/`CostTags`/`RunLimits`/`RunMode`, `Caps`/`ModelProfile`/`Pricing`/`LatencyClass`, `Model`/`Tool`/`Decider`/`CredentialSource`/`Waker`/`ContextProvider` |
| `core/stores` | the nine store ports, their memory implementations, `RunState`, lease constants, `RetentionPolicy` |
| `core/chains` | `Step`/`StepKind`/`ToolChain`/`ModelChain`, ordering validation, `StepError`, `PromptSet`, `Explain`'s types |
| `core/guards` | `GuardInput`, `GuardAction`, `OutputMode`, stages, `GuardBlockedError` |
| `core/permission` | gate skeleton, `ApprovalPolicy`, `ApprovalRequest`, `Verdict`, scope and grant types |
| `core/suspension` | `SuspendError`, reasons, `ResumeInput` constructors, `Waker` use |
| `core/streams` | event kinds and payloads, `Done`, `RunNotice`/`NoticeKind`, deltas, `StreamBuffer`, `Attach`, `Collect`/`Last`/`Drain` |
| `core/runtime` | `Stepper`, `Runtime`, `State`, `Status`, `Drive`'s step types, native runtime |
| `core/flowdef` | definition model only (M4) |

Rules that follow from it:

1. One enum per package. Two enums with a member of the same name never share a package — that is the whole reason for the split.
2. No leaf package imports `core/` or another leaf except downward in the table (`types` is the floor; `runtime` may use `chains`, `stores`, `guards`, `permission`, `suspension`, `streams`; nothing may import `core/` or `core/runtime` except the driver).
3. The driver re-exports vocabulary as type aliases (`type Caps = types.Caps`) so documented call sites (`gohan.Caps`, `gohan.Message`, `gohan.Flow`) stay valid. A const is never re-exported: a const in `gohan` would recreate the collision the split removes.
4. `std/*` stay separate packages; depguard forbids any `std` import from any `core/**` package.

## Context and evidence

The single-package clause came from ADR-0056 with the rationale that ports reference each other's types and Go has no forward declarations across packages, so a split would force import cycles or duplicated types. The 2026-10-04 review found eight package-scope collisions that the clause cannot satisfy at all: `Block` is a `messages` interface and a `guards` `GuardAction` member; `Collect` is a `streams` helper and a `subflows` `OnError` member; `Continue` is a `runtime` `Status` member and a `suspension` constructor; `Suspended` is a `stores` `RunState` member and a `streams` event type; `Failed` is a `messages` `Outcome` member and a `stores` `RunState` member; `Allow` and `Ask` are members of both `permission.Verdict` and `taint.TaintAction`; `Retryable` is a `messages` `ErrorKind` member and a wrapping function. Go rejects a type and a const of the same name in one package, so the collision is a compile error, not a style preference.

## Considered options

- Rename the losing member in each pair, keeping one package. Cheapest for the layer rule, largest prose churn, and it changes the vocabulary the specs use in their scenarios.
- Keep one package and give the code qualified names with a mapping table. Preserves the specs' prose, but the spec Go blocks stop being the API, which is what M0's test binding rests on.

## Consequences

The downward-only import rule replaces "one package" as the property to defend; `depguard` encodes it. `testkit/conformance` and `storetest` import leaf packages, not the driver. `docs/design/types.md` keeps indexing by capability (unchanged), and its generator must additionally fail on two declarations of one name with different kinds in the same package — the check that would have caught all eight collisions.
