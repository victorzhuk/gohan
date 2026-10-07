# Design: m1-identity-detach

## Context

The `identity` *Identity* requirement fixes the closed set of values gohan carries in `ctx` — `Principal`, `Credential`, `Approval`, `RunInfo`, the idempotency key and shared state — and fixes how each survives a context boundary (openspec/specs/identity/spec.md:99-108). Its Contract rule 8 already states the setters are called by transport and harness code only; the three scenarios this change owns assert the observable consequences of that rule on the public path. The machinery ships in pieces: `WithIdempotencyKey` and its accessor in `core/types/identity.go`, the cancel shield in `std/shield.go`, and detach plus reattach in `core/attach.go` and `core/stream_stall.go`; the credential port `CredentialSource` is already resolved on resume (`core/resume.go:255-263`) and on recovery (`core/recover.go:594-611`), and the decision that credentials are never persisted and are resolved at execution time is recorded (ADR-0029). The ambient-principal and transport-only decision is recorded (ADR-0028), and the sub-flow boundary is the shipped `FlowTool` seam (ADR-0091; `core/run_tree.go:42-53`). The gap is enforcement and proof, not new machinery.

The delta file `specs/identity/spec.md` modifies the *Context values* requirement, not *Identity*: the three scenarios this change owns live under *Context values* (`openspec/specs/identity/spec.md:200-225`), and *Context values* carries no normative prose, so the delta reproduces its five scenario blocks unchanged and adds the three enforcement sentences there. The *Identity* requirement is deliberately NOT reproduced: it already carries the two sentences an earlier change added (the `RunInfo.Flags` clause and the per-tenant notice-endpoint clause), and a requirement absent from a delta is left intact by the archive merge, so this package ships no *Identity* delta at all and cannot drop those sentences (openspec/changes/m1-run-notifications/design.md, *Migration Plan*).

## Goals / Non-Goals

**Goals:** Make the idempotency key transport-only at the sub-flow boundary, prove the cancel shield preserves the identity values, and re-issue the detached run's credential through `CredentialSource`; turn the three owned scenarios green on the public path.

**Non-Goals:** This change adds no second sub-flow seam and no change to `subflows` isolation, effects, nested suspension or limits (those belong to M3). It does not change `Runs.Start` dedup or the `OperationID` contract (ADR-0077), and it adds no credential cache, no token refresh loop and no new credential store. It adds no transport module and no HTTP handler.

## Decisions

### D1 — The idempotency key is transport-only; enforcement is the depguard rule plus the sub-flow strip

The key is a retry-ordering token at the seam: `Send`/`Invoke` pass it to `Runs.Start` as `OperationID` and dedup through `Runs.ByOperation` (`flow` spec *Concurrency and idempotency*; ADR-0077). It MUST NOT become run identity: it never enters `RunInfo`, and a child run MUST NOT inherit it. Enforcement has two legs:

- **Lint.** The examples module carries `examples/.golangci.yml` with a depguard rule that forbids `context.WithValue` outside the identity seam, and `task lint` runs the linter in the examples module in addition to the root module. A tool that smuggles a value onto its own `ctx` through the raw standard-library setter fails the lint gate. The one example that carried a delivery token through `context.WithValue` (`examples/kafka-refunds/fixtures.go`, `withDelivery`) moves the delivery to an explicit field so the tree lints clean; the token is not identity and does not belong on `ctx`.
- **Runtime.** `FlowTool.Invoke` builds the child context with `types.WithoutRunIdentity(ctx)`, a harness-only helper that hides the unexported idempotency-key and approval entries, then sets the child's `RunInfo`. The child therefore observes `IdempotencyKey(ctx)` as `ok == false` and `ApprovalFrom(ctx)` as `ok == false` even when the caller's `ctx` carried both (the propagation table's `FlowAsTool` row). The helper lives in `types` because the keys are unexported there and no other package can hide them.

### D2 — The shield preserves the identity values unchanged

`std.Shield` runs a `SideEffect` call under `context.WithoutCancel(ctx)`, which copies every context value and drops only the cancellation signal. The invariant this change fixes as normative: the shielded context carries the same `Principal`, `Credential`, `RunInfo` and idempotency key as the attached run; the shield MUST NOT drop, replace or re-derive any of them. The tool's own contract already reads identity through `PrincipalFrom`, `CredentialFrom` and `RunInfoFrom` (`tools` spec *Tool* rule), so the shield preserving the values is what keeps a client disconnect from erasing identity mid-call. The invariant is proven by a public-path subtest that runs a `SideEffect` tool through the real `Shield` middleware with a cancelled caller context and asserts all three accessors return the run's values inside the tool.

### D3 — The detached run re-issues its credential; the caller's token is never reused

At the detach seam in `conversation.Send`, after `runIdentityCtx` has installed the run identity, the harness builds the context the detached run executes under:

- `Principal`, `RunInfo` and the idempotency key are copied from the caller's context (the propagation table's `Detached` row).
- `Approval` is dropped, like every other non-resumed boundary.
- `Credential` is re-issued: the harness calls `CredentialSource.Credentials(ctx, principal)` for the copied principal and installs the fresh `Credential` on the detached context, overwriting the caller's value so it cannot be reused. When the caller carried a `Credential` and the build has no source, the detached run carries none — the harness hides the stale value rather than reuse it, because a detached run may outlive the token's expiry and a refresh is impossible. A build that starts a detached run with a credential and no source therefore does not fail the run; it runs credential-free, matching the sandbox row that admits no secret without a source.

This mirrors the shipped resume path (`core/resume.go:255-263`), which resolves the originator's credential through the same port before any tool runs, and the shipped recovery path (`core/recover.go:594-611`). The key material stays in `ctx` only: the fresh token is never written to a checkpoint, a journal, an audit record, a span or a log line, and the caller's token is shadowed so it cannot leak back through the detached context (ADR-0029; `identity.token-never-exported`).

### D4 — Public-path proofs

The three owned scenarios are subtests named exactly their IDs, registered through the public `Build`/`NewConversation` path and the shipped middleware, with no test reaching the network:

- `identity.idempotency-key-transport-only` (`core/identity_detach_scenarios_test.go`) — a tool sets an idempotency key on its own context and invokes a `FlowTool` sub-flow; the child observes `IdempotencyKey` as `ok == false`, and the lint gate covers the raw setter.
- `identity.detached-reissues-credential` (same file) — a `Detached` conversation started with a `Credential` in the caller's context records a `CredentialSource` call for the copied principal, the detached run's `CredentialFrom` returns the fresh token, and the caller's token is absent.
- `identity.values-survive-shield` (`std/shield_scenarios_test.go`) — a `SideEffect` tool runs under `Shield` with a cancelled caller context and observes the run's `Principal`, `Credential` and `RunInfo`; the subtest lives in `std` because `core` never imports `std` (ADR-0139).

## Risks / Trade-offs

- [Risk] A detached run with a credential and no `CredentialSource` runs credential-free, which can fail a tool that needs a token. → Accepted and stated in D3: reusing an expired token is worse, and the failure is an ordinary tool failure the host sees; a build that wants a credential for a detached run wires a source.
- [Risk] The depguard rule is package-level and cannot name a single setter, so it forbids the raw `context.WithValue` rather than the gohan wrapper. → Accepted: the wrapper delegates to the raw setter and is transport-only by rule 8; the raw-setter ban is the enforceable half, and the runtime strip covers a wrapper call that slips through.
- [Risk] Hiding the idempotency key at the sub-flow boundary could break a host that relied on the key propagating into a child. → Accepted: the propagation table drops the key on the `FlowAsTool` row, and `OperationID` dedup is a root-run concern; a child that needs its own dedup key must receive it as typed input, never through ambient `ctx`.

## Migration Plan

No data migration is required. `types` gains the harness-only `WithoutRunIdentity` helper, `FlowTool.Invoke` calls it, and the detach seam re-issues the credential; no stored shape and no exported contract changes. `examples/.golangci.yml` is added and `task lint` runs the examples module linter; the kafka-refunds fixture moves its delivery token off `ctx`. The `identity` delta adds three sentences to the *Context values* requirement and reproduces its five scenarios; it does not reproduce or modify *Identity*, so the archive merge leaves that requirement's `RunInfo.Flags` and notice-endpoint sentences untouched. Keep the module pre-v1 and create no release tag during M1 authoring (openspec/changes/m1/design.md:20-27).
