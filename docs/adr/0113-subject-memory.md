# ADR-0113: Cross-session memory is a subject scope of notes, retrieved by tool, written through a schema gate

Status: accepted · Origin: grill round 39 (2026-09-30)

## Decision

`working-state` gains `ScopeSubject` on the existing notes primitive instead of a new capability. The key is built by the harness from `SessionOwner`, so isolation holds by construction. Memory enters context only through `memory_read` (never a `SlotSession` provider) and its blocks carry the stored origins, so fencing and taint apply. `memory_write` accepts only schema-valid values, is `Ask` when tainted and denied while an `Untrusted` block is in the taint window unless the flow opts in. Consolidation is an audited `Extract` at `Done`, never part of compaction. Owners can list and forget entries; `EraseSubject` removes the tier; `MaxEntries`/`TTL` evict oldest first on the store clock.

## Context and evidence

The 2026 survey of long-term memory security shows query-only and environment-only poisoning reaching >90 % success, toxins amplified by summarisation, and retrieved entries dictating tool choice; its recommended controls are provenance per entry, write-gate validation, consolidation as a privileged write, principal-scoped retention and user-visible deletion in a tier separate from audit. gohan's notes already had guards and taint but were session-scoped; the first product needing cross-session memory would have added an unguarded provider.

## Consequences

`working-state` v1.1 (`NotesKey`, `MemoryEntry`, `MemoryPolicy`, `notes.Memory`, `Stack.Memory`/`ForgetMemory`, seven scenarios), `redaction` v1.1 (one scenario), `taint` rule 6 widened; M0 task 12 keys notes by `NotesKey`; subject scope ships in M1.
