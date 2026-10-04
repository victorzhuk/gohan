# ADR-0085: `Lang` port with a declared capability set (`Deterministic`, `StepLimit`, `MemoryAccounting`, `TypeCheck`, `Hermetic`, `Snapshot`, `Continuations`) and two admission roles: `ExprLang` (requires `Deterministic + StepLimit`; `TypeCheck` preferred) now, `ScriptLang` (requires `Hermetic` plus either `Deterministic` for replay or `Snapshot`/`Continuations` for native suspension) later. Front-ends for definitions need only a parser. `adapter/cel` is the default `ExprLang`; `adapter/lispico` is a definition front-end and an `ExprLang` where its declaration allows

Status: accepted · Origin: gohan-spec v0.13 decision D85

## Decision

`Lang` port with a declared capability set (`Deterministic`, `StepLimit`, `MemoryAccounting`, `TypeCheck`, `Hermetic`, `Snapshot`, `Continuations`) and two admission roles: `ExprLang` (requires `Deterministic + StepLimit`; `TypeCheck` preferred) now, `ScriptLang` (requires `Hermetic` plus either `Deterministic` for replay or `Snapshot`/`Continuations` for native suspension) later. Front-ends for definitions need only a parser. `adapter/cel` is the default `ExprLang`; `adapter/lispico` is a definition front-end and an `ExprLang` where its declaration allows.

## Context and evidence

Termination and cost limits are free in a non-Turing-complete language; a Turing-complete Lisp would make the harness's safety depend on limits gohan would have to build.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
