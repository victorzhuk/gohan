# Proposal

## Why

M0 shipped the `TaintHook` slot and taint result types, but no matcher installs the hook on a public flow path (`core/types/taint.go:19-40`; `openspec/changes/m1/design.md:37-47`). The registered taint scenarios therefore remain an integration gap or missing behavior, including enforcement at permission gates and the Build-time trifecta check (`openspec/changes/m1/design.md:33-47`).

## What Changes

- Add deterministic provenance matching and policy evaluation through a `std/taint` hook; retain the raw session window since the last user message (`openspec/specs/taint/spec.md:42-48`; `openspec/changes/m1/design.md:230-236`).
- Integrate the hook into the native flow definition and permission gate, with hard/live denies before taint, then session grants, then the decider (`docs/adr/0155-m1-taint-and-egress-contract.md:21-29`).
- Add a `Build` trifecta check and structured refusal when an eligible flow omits explicit policy (`docs/adr/0155-m1-taint-and-egress-contract.md:25-27`).
- Add deterministic boundary and public-path tests for Build, Send, and Resume; matcher-only tests do not prove gate integration (`openspec/changes/m1/design.md:295-297`).
- **BREAKING**: Flows that declare a taint policy enforce it at tool-call boundaries; tainted arguments can now deny calls or require approval (`docs/adr/0155-m1-taint-and-egress-contract.md:35-37`).

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `taint`: Specify deterministic taint computation, gate enforcement, and Build trifecta behavior (`openspec/specs/taint/spec.md:44-55`, `60-124`).

## Impact

The change package modifies the `taint` contract through its delta. Implementation will touch `std/taint`, native flow registration and resolution, `Build`, permission-gate integration, and focused tests; core currently declares `TaintHook`, and `NativeSpec` currently has a `Decider` but no taint-policy field (`core/types/taint.go:37-40`; `core/native_options.go:16-28`). Egress derivation is owned by the separate egress change and taint consumes the resolved `ToolSpec.Capabilities.Exfil` value (`openspec/changes/m1/design.md:234`; `docs/adr/0155-m1-taint-and-egress-contract.md:27`).
