# 0150. Governed native construction: sealed interfaces

Status: accepted

## Context

`Build` records models, middleware and resolved strategies that no public execution path reads; the governed turn loop exists but only tests reach it; and no reusable native `Stepper` ships, so every example hand-writes one. `openspec/changes/m0-hardening/design-native-path.md` splits the repair into sixteen chunks. Several of them touch one interface, so the interface is sealed here before they are dispatched in parallel.

## Decision

1. **Public seam.** `WithNativeAgent(NativeSpec)` registers a flow definition with `Build`; `NewNativeConversation(stack, name, opts…)` returns a handle built from the resolved configuration; `Explain`, the release manifest and the startup record read that same value rather than recomputing anything.
2. **One Step is one effect.** The native runtime declares `GranularityEffect` and keeps its phase in the serializable `State.Backend`. A `Step` executes exactly one of the model effect or the batch effect, never both.
3. **Effect boundary.** Package `runtime` declares
   `type EffectFunc func(ctx context.Context, st State) (State, []types.Event, Status, error)`,
   and `AgentRun` gains `ModelEffect EffectFunc` and `BatchEffect EffectFunc` beside the existing `Assemble` and `Save` callbacks. The extracted turn-loop code provides them; the driver binds them to the resolved configuration. `DriveLifecycle` stays the only execution loop.
4. **Per-run accounting.** One `LimitsState` per run, reached from the context through `WithLimitsState` and `LimitsStateFrom`, shared with descendants only through an explicit tree parent. A preset never captures one.
5. **Error rule.** At the driver boundary a batch overrun — `runtime.ErrBatchOverrun` — is translated to `*types.LimitExceededError`. The native runtime does not return a raw overrun to a caller.
6. **Prompts.** The resolved `PromptSet` is the only source of harness-authored text. No component appends a literal of its own, which includes `std.ReadBackResult`'s sentence and the recipe repair line; both become named, replaceable values.
7. **What `Explain` claims.** It reports the request the resolved configuration prepares, from the same value execution reads. With arbitrary caller middleware in the chain, byte-equality between an explanation and the executed request is not claimed.

## Consequences

- Wave 2a (C02, C03, C04) can proceed file-disjoint against one declared boundary instead of inventing three.
- The native runtime stays a leaf: it calls driver-supplied functions and never reaches a store or a policy itself, so it satisfies the core budget rule and works under any backend.
- `State.Backend` carries the phase, so a suspended native run resumes at its phase rather than at the start of a turn.
- A `Native` runtime instance holds the run it was started with, because `Stepper.Step` takes no `AgentRun`: one instance drives exactly one run, and the driver creates one per invocation instead of sharing one across runs.
- Declaring `Explain`'s claim narrowly means the C13 regression asserts agreement on the resolved preparation, not on middleware the caller injected afterwards.

## Alternatives

- Keeping the loop inside `driveTurn`. Rejected: it is a closure, so a public constructor would have to duplicate the lifecycle or the loop.
- Letting the native runtime own the limit ledger. Rejected: the ledger is policy and the runtime is a leaf; it also breaks the per-run rule the moment two runs share a process.
- Claiming `Explain` byte-equality with the executed request. Rejected: with caller middleware in the chain the claim would be false, and a false equality claim is worse than a narrow one.

## Out of scope

- Durable snapshot fields that would let a shared tree cost ledger be reconstructed in another process. Locally shared tree accounting is what this change proves; the scenario that would need the rest is deferred.
