# Change: m0-core

## Why

Establish the core module and `std` presets so that S1 (`quickstart`, `excursions` day-1) runs on the native runtime with memory stores, and the API can be frozen for review at M0.5.

## What changes

| M0 | **core**: types incl. `Origin`, `Seq`, error classes, `PromptSet`, chain-as-data + ordering validation, `Explain`, `StepError`, Flow/Conversation, native runtime, NewTool, scopes, Principal, run trees, suspension + Replay resume, `Runs` + `Recover` + `Inspect`, RunLimits, uncertainty surfacing, memory stores + EventLog, Build + profiles + fallback validation + manifest + tool policy, `ToolFilter`, OTel, scripted model, conformance (incl. leak checks) + storetest. **std**: canonical chains with `Applies` gating, gate, journal, shield, guards, StablePrefix + fencing + Truncate, ToolSchema + ValidateRepair, DefaultPrompts, presets | R1–R6 incl. R5a–R5l, R8–R12 on native; S1 green; core API frozen for review |

Deltas: this change ADDS the M0 capabilities — `flow`, `messages`, `suspension`, `identity`, `model`, `tools`, `decider`, `chains`, `guards`, `permission`, `assembly`, `build`, `stores`, `recovery`, `limits`, `runtime`, `streams`, `working-state`, `structured-output` (app-side validation only), `telemetry` (OTel seam only), `performance` (baselines). Deferred to later changes: M1 `taint`, `cache`, `redaction`, `lifecycle`, `flags`; M2 `context`, `sandbox`; M3 `subflows`, `interop`, `release`, `engines`; M4 `skills`, `agui`, `languages`. Adapters beyond `native` are out of scope.

## Out of scope

`adapter/*` modules, postgres/redis stores, guards beyond rules, Notes/OutputStore, `Detached` mode, flowdef compiler, redaction beyond the port definition.
