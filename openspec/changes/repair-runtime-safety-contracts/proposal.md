# Proposal

## Why

Approval, tool effects, lease ownership, suspension, recovery and provider execution violate their existing contracts at public composition boundaries. Workspace-only validation also accepts adapter releases that do not build against their declared root dependency.

## What Changes

- Bind ordinary approvals to the approved call, apply current eligibility to every counted vote, preserve exact numeric argument identity, and enforce excluded identity fields during execution.
- Use one advertised effect for host execution and one downstream idempotency key for initial execution and replay.
- Enforce session exclusivity on every lease acquisition, use database time for PostgreSQL ownership, preserve cumulative run accounting, and record recovered events before terminal delivery.
- Separate checkpoint lifetime from execution leases. Add explicit store lifetime options and optional recovery surfaces without changing frozen port methods.
- Emit complete provider tool calls, install the existing Eino identity middleware, and honor iterator termination and optional usage fields.
- Separate Docker setup privileges from untrusted execution, make snapshots immutable, and preserve checked destination addresses through proxy routing.
- Pin adapters to the existing root v1 release, verify declared dependencies with the workspace disabled, and repair semantic compatibility checks and deterministic validation fixtures.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `permission`: ordinary approval authority and current-policy revalidation.
- `identity`: identity argument exclusion at the execution boundary.
- `tools`: effect declaration, idempotency identity, private-address coverage and proxy destination pinning.
- `stores`: exclusive lease acquisition, database-authoritative time, explicit checkpoint lifetime and optional token-aware recovery surfaces.
- `recovery`: consumed-state discovery, current-token fencing and durable recovered output.
- `runtime`: terminal persistence ordering and cumulative tool admission accounting.
- `model`: complete provider tool-call streams and iterator termination.
- `sandbox`: execution privilege separation and immutable snapshot publication.

## Impact

Implementation touches existing `core`, `std`, provider/runtime adapters, PostgreSQL, Docker, testkit, Taskfile, CI, module manifests and compatibility tooling. No frozen interface method is added or changed. Additive optional interfaces and store options require a new ADR, capability deltas and a regenerated types index during implementation.

Security fixes intentionally stop reuse of ordinary approvals and model-supplied excluded identity fields. Docker workload execution loses network-administration capabilities while preserving UID compatibility. A driver no longer supplies a lease-derived checkpoint expiry; finite checkpoint lifetime becomes an explicit store option. These behavior changes require approval of the design before implementation.

The existing research-report change remains independently owned. This change does not publish releases, rewrite existing tags, run production migrations, or upgrade third-party dependencies.
