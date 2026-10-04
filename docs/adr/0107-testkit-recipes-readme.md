# ADR-0107: Testkit shapes, std/flow recipe signatures, quickstart, README, CI

Status: accepted · Origin: review round 33 (2026-09-30)

## Decision

`docs/design/testing.md` carries illustrative shapes for `gohantest` (`NewScriptedModel` + turn builders, `Record`/`Replay` with the cassette format and `GOHAN_CASSETTES` modes, `Flaky`, `LeakCheck`), `conformance.*` and `storetest.*`, each suite bound to its scenario IDs. `flow` gains the `std/flow` recipe signatures (`Extract` and `Classify` normative in M0; `Route`, `Pipeline`, `MapReduce`, `RAG`, `Judge` by milestone) and the typed-output rule for `Conversation` (`Text` JSON + `Done.Result`). A canonical quickstart listing lives in `docs/design/scenarios.md` §9.0 and the index generator verifies its `gohan.*` identifiers. `README.md` added; task 1 gains the CI workflow and required checks.

## Context and evidence

Task 29 and the recipes used by the example catalog had no API; the repository had no human entry point and no CI definition.

## Consequences

Three `flow` scenarios; `streams.Done` gains `Result`; tasks 1 and 23 updated.
