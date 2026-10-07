# Proposal: m1-identity-detach

## Why

The `identity` *Identity* requirement fixes the closed set of values gohan carries in `ctx` and their propagation across context boundaries, but three registered scenarios stay `integration gap` because nothing enforces the contract (openspec/specs/identity/spec.md:99-108; openspec/changes/m1/design.md:184-187). `WithIdempotencyKey` ships in `core/identity.go` and `core/types/identity.go` with no rule that keeps it transport-only, so a tool can set it and have a sub-flow inherit it as if it were run identity. `std/shield.go` ships and derives its shielded context with `context.WithoutCancel`, but no test observes the identity values inside a shielded `SideEffect` tool. `core/attach.go` and `core/stream_stall.go` ship detach and reattach, but the detached harness context copies the caller's `Credential` unchanged instead of re-issuing it, so a detached run can execute a tool with a token the harness never refreshed. The three scenarios `identity.idempotency-key-transport-only`, `identity.values-survive-shield` and `identity.detached-reissues-credential` are the milestone record's decomposition target for `m1-identity-detach` (openspec/changes/m1/design.md:224).

## What Changes

- Enforce the transport-only idempotency key: the harness drops the key (and the approval) at the sub-flow boundary, and the examples module carries a depguard rule that refuses a raw `context.WithValue` outside the identity seam.
- Fix the cancel shield's identity-value invariant: a `SideEffect` tool running under `context.WithoutCancel` observes the run's `Principal`, `Credential` and `RunInfo`, and a public-path subtest proves it.
- Re-issue the credential on detach: the detached harness context copies the caller's `Principal` and `RunInfo`, drops the `Approval`, and resolves a fresh `Credential` through the stack's `CredentialSource`; the caller's token is never reused, logged or exported.
- Preserve and cover the three scenarios this lane owns: `identity.idempotency-key-transport-only`, `identity.values-survive-shield`, `identity.detached-reissues-credential`.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `identity`: the three scenarios this change owns live under the *Context values* requirement (`openspec/specs/identity/spec.md:200-225`), which carries no normative prose, so the delta reproduces its five scenario blocks unchanged and adds the three enforcement sentences the requirement lacked. The *Identity* requirement is not touched, so its two added sentences (`RunInfo.Flags`, notice-endpoint isolation) stay intact.

## Impact

The design adds one harness-only context helper to `core/types/identity.go`, one call in `FlowTool.Invoke` (`core/run_tree.go`), the credential re-issue in the detach seam (`core/stream_lifetime.go`), a depguard rule for the examples module, and the two scenario test files. It adds no new package, no third-party dependency and no transport module; the detached path reuses the shipped `CredentialSource` port (ADR-0029) and the sub-flow boundary reuses the shipped `FlowTool` seam (ADR-0091). The lane is a `core`/`std`/`examples` change only — adapter and `interop` work is out of scope (openspec/specs/interop/spec.md:48).
