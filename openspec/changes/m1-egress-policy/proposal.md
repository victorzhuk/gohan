# Proposal: m1-egress-policy

## Why

The `tools` contract declares policy checks and egress behavior, but Build does not call `RequiresEgress`, `CheckEgress`, or `DeriveExfil`; no `std/egress` client or `std/tool/http` exists (openspec/specs/tools/spec.md:155-159; openspec/changes/m1/design.md:56-60,70). M1 must connect those declarations to the public Build and tool-call paths without duplicating the shared security contract (docs/adr/0155-m1-taint-and-egress-contract.md:17-34).

## What Changes

- Add core `ToolOption`s for egress policy, exfil capability, and private-read capability, and have `Build` validate network registrations and derive each resolved tool's `Capabilities.Exfil` (core/types/tool_egress.go:11-24,27-39; docs/adr/0155-m1-taint-and-egress-contract.md:25-27).
- Add `std/egress` enforcement for address validation, redirects, limits, proxy transport, audit, and request metrics (openspec/specs/tools/spec.md:157-159).
- Add `std/tool/http.Fetch` as a governed HTTP tool using the shared client (openspec/specs/tools/spec.md:159).
- Preserve and cover the five assigned M1 scenarios. The M0-ranked `tools.egress-policy-required` remains an integration obligation; this change closes its Build-path gap without re-claiming its registry ID (openspec/changes/m1/design.md:52-70).

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `tools`: complete the Egress requirement's public Build and request behavior in `openspec/specs/tools/spec.md:155-189`.

## Impact

The design affects `core/tool.go`, `core/build.go`, and tool-resolution code, plus new packages under `std/egress` and `std/tool/http`; the core budget reserves policy values and defaults for `std` (docs/design/architecture.md:101-121; core/tool.go:17-27; core/build.go:106-159). It adds no third-party dependency. Sandbox tools with non-empty `Egress` remain outside this change (openspec/specs/tools/spec.md:157).