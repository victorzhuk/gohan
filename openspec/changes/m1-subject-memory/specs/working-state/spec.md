# Spec Delta

## MODIFIED Requirements

### Requirement: Subject memory

Subject memory MUST be the cross-session scope of the notes primitive. The harness MUST build its key from the session owner as `ScopeSubject` plus tenant and subject, so no caller can address another tenant or subject. The store MUST validate every written value against `MemoryPolicy.Schema`, MUST stamp each stored entry with the store clock, and MUST evict the oldest entries past `MaxEntries` or `TTL` at write time. Every `memory_read` result MUST carry the entry's stored origins.

#### Scenario: memory store optional
ID: `working-state.memory-store-optional`
- WHEN `notes.Memory` is registered with a `NotesStore` that does not implement `MemoryStore`
- THEN `Build` fails with `ErrMemoryStoreRequired` and session notes keep working

#### Scenario: subject memory isolated
ID: `working-state.subject-memory-isolated`
- WHEN a run for subject `u1` calls `memory_read` after a run for subject `u2` in the same tenant wrote an entry
- THEN `u2`'s entry is not returned and no key for `u2` is constructible from `u1`'s run

#### Scenario: untrusted turn cannot write memory
ID: `working-state.memory-write-untrusted-denied`
- WHEN a web-page tool result is in the taint window and the model calls `memory_write` without `AllowUntrustedMemory`
- THEN the call fails `Permanent`, nothing is stored and `gohan.memory.write_denied` increments

#### Scenario: memory value must match schema
ID: `working-state.memory-write-schema`
- WHEN `memory_write` carries free text that does not validate against `MemoryPolicy.Schema`
- THEN the call fails `Permanent` and nothing is stored

#### Scenario: recalled memory carries origin
ID: `working-state.memory-read-carries-origin`
- WHEN an entry written from a tool result is recalled and its value is passed to an `Exfil` tool
- THEN the argument is tainted with the entry's stored origin and the default policy denies

#### Scenario: consolidation at done
ID: `working-state.memory-consolidate-at-done`
- WHEN a flow with a `memory` policy finishes
- THEN one `Extract` over the session produces schema-valid entries, an audit record `memory_consolidate` names the session, and no entry is written during compaction

#### Scenario: forget
ID: `working-state.memory-forget`
- WHEN the owner calls `ForgetMemory(owner, id)`
- THEN `Memory(owner)` no longer lists it and the next `memory_read` does not return it

#### Scenario: ttl eviction
ID: `working-state.memory-ttl-eviction`
- WHEN a subject has `MaxEntries` entries and a new one is written
- THEN the oldest by store clock is evicted and `gohan.memory.evicted` increments
