# Durable working state

Capability: `working-state` · Spec v1.4 (ADR-0132) · Source of truth for this capability.

> **Contract tiers.** Code blocks in *Contract* are **normative** (ports, interfaces, error classes, stored shapes, event types, ordering rules) unless a block is marked `(illustrative)`, in which case names may change during M0 without a spec change. Prose rules in *Contract* are normative. Requirements are normative and each scenario ID is referenced by a test.

## Purpose

See `docs/overview.md` for how `working-state` fits the architecture. Out of scope for this capability is anything owned by another capability spec; cross-references are by capability name.


## Contract

### 6.15b Durable working state

```go
type NotesScope int

const (
	ScopeSession NotesScope = iota
	ScopeSubject
)

type NotesKey struct {
	Scope   NotesScope
	Tenant  string
	Subject string
	Session string
}

type MemoryEntry struct {
	ID      string
	Value   json.RawMessage
	Origins []Origin
	RunID   string
	Version int64
}

type NotesStore interface {
	Read(ctx context.Context, key NotesKey) (string, int64, error)
	Write(ctx context.Context, key NotesKey, expectedVersion int64, notes string) (int64, error)
}

type MemoryStore interface {
	Entries(ctx context.Context, key NotesKey) ([]MemoryEntry, error)
	Put(ctx context.Context, key NotesKey, e MemoryEntry) error
	Forget(ctx context.Context, key NotesKey, ids ...string) error
}

type MemoryPolicy struct {
	Schema     json.RawMessage
	MaxEntries int
	TTL        time.Duration
}

var ErrMemoryStoreRequired = errors.New("gohan: subject memory needs a MemoryStore")

func notes.New(store NotesStore) (gohan.Tool, gohan.ContextProvider)
func notes.Memory(store NotesStore, p MemoryPolicy, opts ...notes.Option) gohan.Tool
func notes.AllowUntrustedMemory() notes.Option
func (s *Stack) Memory(ctx context.Context, owner SessionOwner) ([]MemoryEntry, error)
func (s *Stack) ForgetMemory(ctx context.Context, owner SessionOwner, ids ...string) error
```

`Notes` is a `ReadOnly`-effect tool pair (`notes_write`, `notes_read`) plus a `SlotSession` provider that injects the current notes (capped, e.g. 2 KiB) into every request. Agents record progress, discovered constraints and decisions there; the content survives truncation, compaction and full context resets (`ClearToolResults` never clears `notes_*` results), and is visible to the next run in the session. Backed by `SessionLog` metadata by default.

**Subject memory** (`ScopeSubject`) is the cross-session tier: what the same principal's later sessions may recall. It is deliberately a second scope of the notes primitive, not a capability, so every rule that governs notes governs it. Rules:

0. **Store.** Subject memory needs a `NotesStore` that also implements the optional `MemoryStore` interface; `Build` fails with `ErrMemoryStoreRequired` when `notes.Memory` is registered on a store that does not.
1. **Key.** `NotesKey{ScopeSubject, Tenant, Subject}` is built by the harness from `SessionOwner`; tools and flows never supply it, so a cross-tenant or cross-subject read is impossible by construction. Memory follows the session-ownership rule of `identity`.
2. **Tools, not ambient context.** `notes.Memory` registers `memory_write` and `memory_read` (`ReadOnly` effect, `Trusted`, `Risk: High`). Subject memory is never injected by a `SlotSession` provider: it enters context only through a visible `memory_read` call, whose result blocks carry `OriginTool{"memory_read"}` with each entry's stored `Origins` attached, so fencing (`guards`) and taint rule 1 treat remembered text like the tool result it came from.
3. **Write gate.** `memory_write` accepts only values valid against `MemoryPolicy.Schema` (validated like `Extract` output; free text is rejected `Failed(Permanent)`); it is forced `Ask` when any argument is tainted (`taint` rule 6 applies to both scopes) and is denied (`Failed(Permanent)`, `gohan.memory.write_denied`) whenever the taint window since the last user message contains an `Untrusted` block, unless the flow declares `notes.AllowUntrustedMemory()`. Every entry records `Origins` of the blocks its value was derived from and the writing `RunID`.
4. **Consolidation is a privileged write.** Session-to-subject summarisation runs only as `std/flow.Extract` over `SessionLog` with the same `Schema`, at `Done`, audited as `memory_consolidate` with the source session; compaction (`context`) never writes subject memory.
5. **Controls and retention.** `Stack.Memory` lists and `Stack.ForgetMemory` deletes entries for an owner (owner-checked like `Inspect`); `EraseSubject` (`redaction`) removes the tier. `MaxEntries` (default 64) and `TTL` (default 90 days, store clock) evict oldest first; `gohan.memory.evicted{tenant}`. Flow definitions (`languages`) declare `memory: {schema, max_entries, ttl}`.

```go
type OutputStore interface {
	Put(ctx context.Context, ri RunInfo, content []Block) (ref string, err error)
	Get(ctx context.Context, ref string) ([]Block, error)
}
```

Tool results larger than `ToolSpec.MaxOutput` are stored and replaced inline by a head excerpt plus `Ref`; a built-in `read_output(ref, range)` tool lets the model page through them. Implementations: memory, postgres (same module), user-provided (S3 etc.).

Shared state: `SharedState[S](ctx) (S, int64, bool)` (`ok == false` outside a run) and `SetSharedState[S](ctx, next) (int64, error)` — typed, JSON-serialisable, versioned, persisted in session metadata, restored on resume; every `SetSharedState` emits `StateChanged{Version, Patch}` (RFC 6902) on the run's stream. Client-supplied state (`agui`) passes `StageInput` and becomes a new version.


## Requirements

### Requirement: Subject memory

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

### Requirement: Notes, outputs and shared state

#### Scenario: notes follow working retention
ID: `working-state.notes-follow-working-retention`
- WHEN a note was last written 200 days ago under the default policy and its session was purged 100 days ago
- THEN `Maintain` deletes the note on the Working clock, independently of the session's deletion

#### Scenario: notes survive compaction
ID: `working-state.notes-survive-compaction`
- WHEN a run writes notes, then compaction hides every earlier message
- THEN the next request still contains the notes via the `SlotSession` provider

#### Scenario: fork copies notes and shared state
ID: `working-state.fork-copies-notes-and-state`
- WHEN a session with notes and shared state at version 5 is forked
- THEN the fork reads the same notes, its shared state is the same value at version 1, and later writes in either session do not affect the other

#### Scenario: notes versioned
ID: `working-state.notes-versioned`
- WHEN two concurrent `notes_write` calls carry the same expected version
- THEN one succeeds and the other fails with a version conflict and re-reads

#### Scenario: output paging
ID: `working-state.output-paging`
- WHEN a tool result exceeds `MaxOutput`
- THEN the model receives a head excerpt plus `Ref` and `read_output(ref, range)` returns the requested slice

#### Scenario: shared state restored
ID: `working-state.shared-state-restored`
- WHEN a run sets shared state, suspends and is resumed on another pod
- THEN `SharedState` returns the same value and version

#### Scenario: shared state versioned
ID: `working-state.shared-state-versioned`
- WHEN `SetSharedState` is called twice
- THEN versions increase monotonically and each emits one `StateChanged`
