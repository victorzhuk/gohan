# ADR-0084: `core/flowdef`: a behavior-free, language-agnostic definition model (`call`, `do`, `for`, `fork`, `switch`, `try`, `wait`, `listen`, `set`, `raise`, `approval`) with per-step input/output expressions over run-scoped `Data`; canonical JSON/YAML serialization in `std`; `std/flow.Compile(def)` produces a `Flow[In, Out]` from the recipes; `Build` validates every `call` against the flow/tool registry with schemas and hashes the definition into the manifest

Status: accepted · Origin: gohan-spec v0.13 decision D84

## Decision

`core/flowdef`: a behavior-free, language-agnostic definition model (`call`, `do`, `for`, `fork`, `switch`, `try`, `wait`, `listen`, `set`, `raise`, `approval`) with per-step input/output expressions over run-scoped `Data`; canonical JSON/YAML serialization in `std`; `std/flow.Compile(def)` produces a `Flow[In, Out]` from the recipes; `Build` validates every `call` against the flow/tool registry with schemas and hashes the definition into the manifest.

## Context and evidence

The definition model, not any syntax, is the contract shared by front-ends, `Explain`, the manifest and the acceptance processes.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
