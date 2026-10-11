# Change: m1

The preceding M-review is closed on the current tree (`design.md` §1). `design.md` holds the acceptance matrix that assigns every obligation in this scope to one change, the shared contract decisions the first changes implement, and the milestone conflicts that need a decision before implementation.

## Scope

`taint` (`Capabilities`, `std/taint`, trifecta check), subject memory (`ScopeSubject`, `notes.Memory`, consolidation, `Stack.Memory`/`ForgetMemory`), `Verify` end-to-end, `std/redact` + HMAC `Redactor` + `EraseSubject` (`redaction`), `std/cache` (`cache`), `std/hedge` (`chains` hedging), `std/notify` + `std/notify/webhook` (`streams` run notices), `std/egress` + `std/tool/http.Fetch` (`tools` egress), `std/flags` + adapter/openfeature later (`flags`), sunset/successor + schema versions + cost tags (`lifecycle`), `quickstart` and `excursions` S1–S2 complete, adapter/openai + adapter/anthropic Model/Decider passing `conformance.Model` (scenarios `model.status-to-class`, `model.retry-after-parsed`, `model.undeclared-fidelity`, `model.cache-explicit-breakpoints`, `model.schema-never-ignored`, `model.usage-estimated`, `model.extra-collision`, `model.raw-round-trip`).

## Exit

See the milestone table in `docs/overview.md`.
