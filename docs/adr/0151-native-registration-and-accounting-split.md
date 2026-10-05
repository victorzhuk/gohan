# 0151. Native registration and the run-limit accounting split

Status: accepted

## Context

`Build` records models, middleware and resolved strategies that no public execution path reads, and the native chunks need one declared registration surface so they can be built file-disjoint. Separately, the run-limit policy has to leave `core` (ADR-0148 recorded the exception and its prerequisite: per-run accounting, which has now landed), and that move must not leave a broken tree while the preset callers still point at `core/chains`.

## Decision

1. **Registration.** `WithNativeAgent(NativeSpec) Option` registers a flow definition before `Build` runs. `NativeSpec` carries the flow name, the `FlowRequest` used for validation, the profile name, the instruction blocks, the tools, the assembly function, and the `chains.ModelChain` and `chains.ToolChain` to run. The tool list has one source — `NativeSpec.Tools`; if `FlowRequest` carries a tool list it is populated from it rather than accepted independently.
2. **Resolution.** `Build` resolves every registered definition once — model by profile name, `ResolveStrategies`, `CheckFidelity`, then chain ordering validation — into an immutable resolved configuration held on the `Stack` and read through a single accessor. A rejected definition fails `Build` and no provider call happens.
3. **Vocabulary.** The build error sentinels already exist (`ErrStrategyUnsupported`, `ErrProviderToolUnsupported`, `ErrFallbackUnknown`, `ErrFallbackIncompatible`, `ErrFidelityUndeclared`, `ErrFidelityDropped`); this change adds none.
4. **Accounting split.** The middleware charges and records; the driver decides stops. `std/limit` refuses only cost and wall-clock overruns, with `*types.LimitExceededError`. Reaching `MaxTurns` or `MaxToolCalls` ends the run with `Done(StopLimit)` (ADR-0147), a decision the driver makes at its effect boundary from the ledger snapshot — never a middleware error. A batch charges its reservation up front and releases it when the batch is refused with `runtime.ErrBatchOverrun`, which the driver translates to `*types.LimitExceededError` (ADR-0150). The ledger does not travel with the policy: `core/chains` owns `LimitsState` and the `WithLimitsState`/`LimitsStateFrom` accessors, because the driver is `core` and is what creates a run's ledger, while `std/limit` holds only the policy that charges it and refuses on it. A first attempt forked the ledger into `std/limit` so the policy could charge it; that fork was removed, because a driver that can only create the core type would leave the policy with no ledger and the limits silently inert in production. Charging is exported from `core/chains` for the policy to use, and `chains.Limits` and `chains.ToolLimits` remain the primitives both paths share.
5. **Migration outcome.** The executable policy now lives in `std/limit`, and the presets capture neither a ledger nor a prompt set, so the middleware half of the ADR-0148 exception is closed. The value half is not: `types.InteractiveLimits`, `types.AgenticLimits` and `types.BatchLimits` are still defaults declared in `core/types`, which `docs/design/architecture.md` §4.2a assigns to `std`. The exception stands for those values alone; moving them is a breaking change with no chunk yet.

## Consequences

- The native chunks have one registration and resolution surface, and the constructor reads it rather than recomputing a profile or a strategy.
- No error is invented for a condition the repository already names, so `errors.Is` keeps working for callers that already match these sentinels.
- The stop-versus-abort distinction lives in exactly one place, which is what makes `runtime.max-turns` and `limits.hard-cost-abort` both true at once.
- The tree never breaks mid-migration: `core/chains` and `std/limit` both compile until C17 removes the former.

## Alternatives

- Registering native definitions after `Build`. Rejected: the resolved matrix and the release manifest are computed during `Build`, so a later registration cannot appear in either.
- Letting the middleware end the run with `Done(StopLimit)`. Rejected: a chain step cannot produce a terminal event, and the lifecycle would lose the single ordering rule it owns.
- Moving the limit middleware by deletion. Rejected: the presets still call it; a broken tree between two chunks costs more than a transitional duplicate.
