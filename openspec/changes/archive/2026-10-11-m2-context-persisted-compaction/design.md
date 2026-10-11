# Design: m2-context-persisted-compaction

## Context

See `proposal.md` — Why. The behaviour is already normative in `openspec/specs/context/spec.md` (*Contract*, "Two stages with different persistence", rules 1, 3, 5, 6; requirement *Compaction is persisted*). This design decides the seams that connect it to the live tree. Read against the tree on 2026-10-07:

- **The `Compactor` seam is declared but unreachable.** `stores.Compactor` exists (`core/stores/projection.go:24-26`) and `ContextPolicy.Compactor` is carried (`core/stores/projection.go:28-33`), but `Compactor` is referenced nowhere outside its declaration: the assembly composition reads only `policy.Projections` (`core/native_effects.go:277-299`), and the policy binds through `WithContextPolicy` (`core/build_options.go:208-215`). The shipped `Compact` returns `History`; the capability Contract returns `Compaction` (`openspec/specs/context/spec.md`, *Contract*).
- **The block and the event already exist.** `types.Compaction{BlockBase, CoversUpTo int64, Kind, Summary []Block, Opaque Raw, Model string, Tokens int}` (`core/types/message.go:156-172`) and `types.Compacted{FromVersion, ToVersion int64, TokensBefore, TokensAfter int, Policy string}` (`core/types/event.go:92-99`). `Compaction` is a registered block kind (`core/types/message.go:225`, `:255-256`, codec at `:408-442`), and `Compacted` is a registered event (`core/stores/events.go:322`). Both marshal through the record envelope.
- **The version mapping is exact.** `History.Version` is the count of appended messages: `Append` does `rec.hist.Version += int64(len(msgs))` (`core/stores/session_memory.go:381`) and `Load` returns `Messages` in append order (`core/stores/session_memory.go:312-330`). A `Compaction` block's `CoversUpTo` therefore names a message count: the first `CoversUpTo` messages are the ones it replaces.
- **The assembly seam.** The native path composes `withProjections(cfg.contextPolicy, cfg.profile, nativeAssemble(...))` (`core/native_effects.go:31`; foreign flows mirror it at `core/foreign_conversation.go:77`). `withProjections` clones the loaded messages and runs the declared projections (`core/native_effects.go:277-299`); `nativeAssemble` is declared at `core/native_effects.go:225` and is the caller assembler plus blobs, redaction and the operator-turn fence (`core/native_effects.go:227-262`).
- **The drive seam.** The turn loop runs in `driveTurns` (`core/drive_turn.go:152`) and the model invocation in `modelEffect` (`core/drive_turn.go:252-271`), which assembles through `c.assemble` before the provider call. Persisting a block goes through `HistoryAppender` (`core/drive_lifecycle.go:95`), read from ctx by `historyAppenderFrom` (`core/drive_turn.go:585-588`).
- **The decision ports ship.** `ContextBudget(p, req, est, ratio)` and `NeedsCompaction(ctx, p, req, m)` (`core/types/context_budget.go:16-58`); `Truncate` carries `WithTruncateEstimator`/`WithTruncateRatio` (`std/context/truncate.go:20-42`) and `Project` returns the history unchanged without an estimator.
- **The module direction forbids a core-to-std import.** `core` imports `std/context` only in `_test.go` files (`core/projection_invariants_test.go:15`, `core/projection_scenarios_test.go:16`, `core/stores/projection_test.go:10`); no non-test `core` file may import `std`. Core therefore cannot construct a fallback projection from `std/context`, and cannot name `std/context.Truncate`.
- **The guard seam ships.** `guards.GuardInput{Stage, Blocks, Origin, Run}` (`core/guards/guard.go:33-44`), `ConsultGuard` opens the `gohan.guard` span and runs the guard (`core/telemetry.go:75`), and `std/guard.ContextGuard.Decide` rejects instruction-like content for tool/provider/model/operator origins (`std/guard/context.go:33-71`).
- **Counters are a shipped port.** `types.Telemetry.Count(ctx, name, n, attrs...)` (`core/types/telemetry.go:9-20`); `gohan.guard.context_rejected` and `gohan.context.compactions` are named in the telemetry spec (`openspec/specs/telemetry/spec.md:96,102`), and the telemetry spec's second column states the attribution (`provider`/`kind`).
- **The invariant guard file already exists.** `core/projection_invariants_test.go` asserts pending-call survival, tool-use/result adjacency, the taint-over-full-log window and journal fingerprints; this change must keep it green.
- **Store identity today is counter-assigned.** `MemorySessionLog.Append` overwrites `Message.ID` with `m%016x` for every message (`core/stores/session_memory.go:331-385`), and the port documents that ids are assigned by the store and stay stable afterwards (`core/stores/session.go:26-28`). A caller therefore cannot know the stored identity before the append.
- **A live event stream cannot prove absence.** `EventLog.Read` delivers persisted events and then live ones (`core/stores/events.go:24-33`), the retained window is bounded (`core/stores/events.go:36-38`), and `MemoryEventLog.Append` allocates the next sequence on every call (`core/stores/events.go:114-123`). Draining `Read` is not a deduplication barrier.
- **The audit chain is one fixed field list in two implementations.** `auditHash` in memory (`core/stores/audit.go:290-298`) and in Postgres (`adapter/postgres/audit.go:196-205`) print the same 26 fields; the memory chain reseals in `core/stores/audit.go` and the Postgres reseal is `adapter/postgres/audit.go:135-163`.

## Goals / Non-Goals

**Goals:** reconcile the `Compactor` seam to the Contract, collapse the loaded log by the persisted `Compaction` blocks during assembly, ship a harness-side `Summarize` whose summary is guarded before persistence, run the compaction stage in the drive with a turn-aligned safe prefix chosen before the summary source, persist a durable publication payload with a predetermined identity, reconcile every publication at every actual provider invocation boundary, native and foreign, through atomic identity-keyed sink appends, measure the request that is actually sent in the same turn, hash the additive audit payload with backward verification, render exactly one current summary, and write the `Compacted` audit record and event.

**Non-Goals:** This change ships no `ProviderCompact`, no `Caps.Compaction` handling and no `Build` fallback rejection across providers — those are `m2-provider-compaction` (`context.opaque-bound-to-model`, `context.opaque-fallback-rejected`). It ships no calibration ratio, no `State.Calibration`, no provider-usage-based trigger and no overflow retry — those are `m2-context-calibration-overflow`. It does not add `PromptSet.Compaction`, tuning or a policy-source type: the `Summarize` policy surface is M4 (`openspec/changes/m2/design.md` §3 item 2). It does not change `Truncate` behaviour, `ClearToolResults`, the taint window or the journal. It ships no generic outbox or retry framework: the three optional store interfaces it declares (D10) are compaction-specific and exist only because read-before-append cannot prove absence.

## Decisions

### D1 — The persistence stage runs in the drive, not in the projection wrapper

`withProjections` runs inside assembly and its seam deliberately carries no store handle (`core/stores/projection.go:11-18`). A persisted compaction appends to `SessionLog`, so it runs where the run holds an appender and the run state: the drive turn (`core/drive_turn.go:152`, hooking the model invocation at `core/drive_turn.go:252-271`). The stage reads `policy.Compactor` (nil disables it) and decides with `types.ContextBudget`/`types.NeedsCompaction` (`core/types/context_budget.go:16-58`).

The stage is specified on both execution paths, because the harness owns both. The native path runs it inside the drive turn where `HistoryAppender` is bound. The foreign path runs the same compactor-enabled persistence stage for a registered foreign flow, because a foreign runtime's `AgentRun` carries the governed assembler and the governed model (`core/foreign_conversation.go:62-90`) and its run must persist and publish a compaction under the same rules. An assembly-only hook is not sufficient on either path: a foreign runtime may call `Model.Generate` (`core/foreign_conversation.go:102-103`) without ever reaching harness assembly, so a stage reachable only through the native drive turn would leave that call uncovered.

The foreign run builder takes the caller's context and session instead of discarding both, and binds everything the stage and the reconciliation need before the runtime executes:

```go
func (s *Stack) foreignAgentRun(ctx context.Context, sessionID string, name string, hist stores.History, seed float64, input []types.Message) (context.Context, runtime.Runtime, runtime.AgentRun, *chains.LimitsState, error)
```

Today the builder calls `chains.WithLimitsState(context.Background(), ledger)` (`core/foreign_conversation.go:72`), so every value the caller's context carries — telemetry, deadline, cancellation, and the ledger and history version the run itself installs — is lost, and `NewForeignConversation`'s `foreignRun` closure receives `ctx` and `sessionID` and passes neither (`core/foreign_conversation.go:164-173`). The migrated builder derives its run context from `ctx`, keeps the ledger and `withHistoryVersion` installation on it, and takes `sessionID` to bind the session-scoped capabilities. The same `ctx` reaches the recovery wiring, which today replaces the recovery context with the constructed foreign context (`core/recover.go:274-278`), and the resume wiring (`core/resume.go:375-393`) and the live run path (`core/stream_lifetime.go:157-158`), which both call the same closure.

Before the foreign runtime starts, the builder binds what the persistence stage and the reconciliation require: the existing session load under `sessionID`, the `SessionCompactionAppender` for that session, the configured audit and event sinks, and the durable run identity that publishes and that reconciles. Two rules follow. The foreign run does not depend on the native turn environment, and it does not depend on an appender installed after `rt.Start` (`core/drive_lifecycle.go:374-387`), because a foreign runtime may reach `Generate` without passing through the lifecycle's post-start context installation. The reconciling run and the publishing run stay distinct identities: reconciliation completes a durable publication under the payload's `RunID` and never substitutes its own.

### D2 — Covered versions are hidden by an intrinsic assembly step, not a declared projection

Hiding is not opt-in: it is how a persisted `Compaction` block is rendered. The change adds `collapseCompactionHistory` in the same composition as `withProjections` (`core/native_effects.go:31`, `core/foreign_conversation.go:77`), running before the declared projections and independent of `ContextPolicy.Projections`. It runs even when the projection list is empty, because the shipped wrapper returns `next` unchanged for an empty list (`core/native_effects.go:279-281`) and the collapse must still run. A log with no `Compaction` block returns the messages unchanged. Rationale: the block is already in history, so hiding needs no store handle and replay reads it straight off the log — a replayed run assembles exactly what the original did (`context.replay-after-compaction`).

### D3 — `Compactor.Compact` returns the block, and the boundary is a safe prefix chosen before the compactor runs

The Contract's signature is `Compact(ctx, h History, p ModelProfile) (Compaction, error)` (`openspec/specs/context/spec.md`, *Contract*, lines 25-27); the shipped stub returns `History`. This is a breaking migration of an exported interface, not an additive change: the result type changes, so the declaration, every implementation and every caller migrate together, and no compatibility alias or deprecated wrapper is kept (`ContextPolicy.Compactor` is unchanged, so no frozen struct gains a field). Before the compactor runs, `selectCompactionPrefix` chooses the covered prefix and its `CoversUpTo`: the prefix ends on a whole-turn boundary, covers a completed pair whole, and excludes any `ToolUse` whose result is absent from history. Pair coverage is decided by matching `ToolUse.ID` against `ToolResult.ID` (`core/types/message.go:136-153`), never by comparing the number of uses with the number of results: two uses and one result are not one pair. The stage passes the prefix-only `stores.History` to `Compact` and requires the returned `CoversUpTo` to equal the selected boundary; a mismatch is refused and nothing is persisted. Selection reuses the turn grouping of `std/context/truncate.go`. Rationale: the summary source then sees only safe messages (`context.compaction-safe-prefix`, `context.compaction-pair-whole`); the write-side rule is `context.pending-call-survives`.

### D4 — `std/context.Summarize` produces `CompactionText` from a declared `SummarySource`

`Summarize` is a `stores.Compactor` in `std/context/summarize.go`, beside `Truncate` and `ClearToolResults`:

```go
type SummarySource func(context.Context, stores.History, types.ModelProfile) ([]types.Block, error)
```

The folded block is `types.Compaction{Kind: CompactionText, CoversUpTo, Summary, Model, Tokens}` whose `Summary` blocks come from the declared source and carry `Origin{Kind: OriginModel}`. The source receives only newly covered ordinary messages: previously covered raw messages and prior `Compaction` blocks never enter it, and the composed `Summary` is the unchanged prior summary blocks followed by the new source output. The `History.Version` the source receives carries the selected absolute `CoversUpTo` boundary, not a relative count, so a source can report the boundary it was given. A nil source is inert and returns an error rather than persisting an empty summary. The `PromptSet.Compaction` instruction, tuning and the policy-source type are M4 (`openspec/changes/m2/design.md` §3 item 2); M2 ships the mechanics and the guard, not the policy. Alternative rejected: feeding the collapsed view to the source (D9), which would re-fold prior summary text into the new summary.

### D5 — The summary is guarded at `StageContext` before the append, and a rejection applies the injected fallback once

The stage runs the produced `Summary` blocks through `ConsultGuard` at `StageContext` with the blocks' own origin (`core/telemetry.go:75`, `core/guards/guard.go:33-44`, `std/guard/context.go:51-71`). A blocked decision persists nothing: the stage leaves the log untouched, projects the history through the injected fallback (D12), reassembles once, and refuses the provider call when the request still exceeds its budget. It emits `types.GuardBlocked` and counts `gohan.guard.context_rejected` with `provider="compaction"` (`types.Telemetry.Count`, `core/types/telemetry.go:9-20`). This is the guarded behaviour `context.summary-guarded` names; the audit and ledger stay clean because no block was written. Scenarios `context.summary-guarded`, `context.compaction-fallback-fails-closed`.

### D6 — The audit record is an additive payload, and the Contract edit is declared

The `Compacted` audit record is written with a new `AuditCompacted AuditKind = "compaction"` and the facts the requirement names, carried on an additive `AuditRecord` payload:

```go
type CompactionAudit struct {
	PublicationID string
	FromVersion   int64
	ToVersion     int64
	TokensBefore  int
	TokensAfter   int
	Policy        string
	SummarySHA    string
}
```

`PublicationID` is the publication identity of D10; `TokensBefore`/`TokensAfter` are the measurements of D7 and D13. `AuditRecord.RunID` and `Event.Meta.RunID` carry the durable publishing run, not the reconciling run. Both the `AuditKind` const block and the `AuditRecord` struct are Contract code (`openspec/specs/stores/spec.md:269-311`); those edits and the regenerated `docs/design/types.md` rows land at apply time (D14). The payload travels in the record envelope beside the record, so it needs no upcaster: ADR-0090 decides the `Compaction`/`Compacted` shapes (context management) and ADR-0157 decides the envelope (`openspec/specs/stores/spec.md:338`). `types.Compacted` gains `PublicationID string` beside its existing fields (`core/types/event.go:92-99`); `AuditLog` and `EventLog` are not widened.

### D7 — One trigger, two measurement sources

Compaction fires on the overflow of the assembled request, measured after the declared projections and the D2 collapse step. The counter is resolved once at build: the model's `types.TokenCounter` when the model implements it (`core/types/tokens.go:19-22`), otherwise the flow's declared `types.TokenEstimator` (`WithEstimator`, `core/build_options.go:198-203`; `config.estimator`, `core/build.go:78`) through `types.ContextBudget` (`core/types/context_budget.go:16-38`). The ratio stays 0 (full window) unless a later owner declares one; `m2-context-calibration-overflow` owns the ratio and the overflow retry, and this change reads no provider `Usage`. Two measurements are kept separate: `Compaction.Tokens` is the count of the rendered `Summary` block at decision time, and the publication's `TokensBefore`/`TokensAfter` are the request's sizes (D13). Scenario `context.compaction-tokens-measured`.

### D8 — The safe prefix is chosen before the summary source and never splits a pair

Selection order: `selectCompactionPrefix` computes `CoversUpTo` first (D3), then `runCompactionStage` calls `Compact` with the prefix-only history, then guards the summary (D5). The summary source therefore cannot see a message the boundary excludes. The boundary respects the capability Contract's rules 1 and 2 (`openspec/specs/context/spec.md` *Contract*): a completed pair is covered whole or left whole, and a pending or approval-suspended `ToolUse` is never covered. Alternatives rejected: letting the compactor choose the boundary (a summary computed before the boundary is what the admission finding rejects) and clamping after the summary (too late — the summary text already contains excluded content).

### D9 — Assembly renders exactly one current summary at the view head

`collapseCompactionHistory` selects the newest `types.Compaction` block by append position and removes that block from its original position; it then removes every superseded `Compaction` block and every message the selected block covers, and inserts exactly one message carrying the selected summary at the head of the view. Unrelated blocks of a retained message stay in place. The view therefore holds at most one summary and never shows the selected summary twice (`context.compaction-single-summary`). `SummarySource` receives only newly covered ordinary messages and prior summary blocks are carried into the composed summary unchanged (D4), so a new summary never re-folds older summary text.

### D10 — Publication identity, durable payload and the reconciliation protocol

The admission finding: publication facts disappear after a crash, and read-before-append cannot prevent concurrent duplicates. Three optional store interfaces in a new `core/stores/compaction.go` close it.

The payload travels with the message:

```go
type CompactionPublication struct {
	PublicationID string
	SessionID     string
	RunID         string
	RootRunID     string
	Flow          string
	Tenant        string
	Subject       string
	FromVersion   int64
	ToVersion     int64
	TokensBefore  int
	TokensAfter   int
	Policy        string
	SummarySHA    string
}
```

It is stored under `Message.Meta["gohan.compaction.publication"]` (`core/types/message.go:20-26` carries `Meta map[string]any`) and decoded into the typed payload after `Load`. Metadata and the guarded block are persisted in one session append. `RunID` is the original publisher; a reconciling run never rewrites it. `FromVersion` is the loaded version and `ToVersion` is `FromVersion + 1`. The payload is validated against the session, the message identity and the summary checksum before either is trusted.

The identity is predetermined:

```go
func CompactionPublicationID(sessionID string, runID string, fromVersion int64) string
```

It returns lowercase SHA-256 hex over the domain tag `gohan.compaction.publication.v1` and length-prefixed `sessionID`, `runID` and decimal `fromVersion`. The driver computes it before prospective assembly (D13); the optional append assigns the same value to `Message.ID`. `SessionLog.Append` keeps its current counter-assigned identity (`core/stores/session_memory.go:331-385`).

Three optional interfaces carry the writes:

```go
type SessionCompactionAppender interface {
	AppendCompaction(ctx context.Context, sessionID string, expectedVersion int64, message types.Message) (int64, error)
}

type CompactionAuditAppender interface {
	AppendCompactionAudit(ctx context.Context, record AuditRecord) (bool, error)
}

type CompactionEventAppender interface {
	AppendCompactionEvent(ctx context.Context, publicationID string, runID string, event Event) (bool, error)
}
```

This change owns the PostgreSQL publication schema as well as the capability: no other package adds a compaction table, column or index, and no table design is deferred to the Postgres repair lane. The version number is the next unused goose version after `adapter/postgres/migrations/00004_event_seq.sql`; `00005_output_claims.sql` belongs to `repair-postgres-store-contracts` task 1.1, so this lane never reuses it and re-checks the adapter head at apply time. The migration filename is proposed as `adapter/postgres/migrations/<next-free-goose-version>_compaction_publication.sql`, selected at apply time as the next free goose version after re-checking the adapter head; it adds the identity key the uniqueness argument needs: `ALTER TABLE audit_record ADD COLUMN publication_id text` and `ALTER TABLE event_record ADD COLUMN publication_id text`, then `CREATE UNIQUE INDEX audit_record_publication ON audit_record (session_id, publication_id) WHERE publication_id IS NOT NULL` and `CREATE UNIQUE INDEX event_record_publication ON event_record (run_id, publication_id) WHERE publication_id IS NOT NULL`. Both columns are nullable and every pre-existing row keeps NULL, so existing audit chains and event sequences verify and the `Down` section drops the indexes and the two columns. The payload stays in the record envelope beside the row (D6); the column carries only the identity that the unique index needs, and a non-compaction record leaves it NULL so the partial indexes never constrain it. The publication key therefore survives `Expire` and the retained event window, and the transactional uniqueness is the whole deduplication barrier.

`AppendCompaction` validates the payload and assigns the deterministic identity, then performs authorization, identity uniqueness, version comparison and append atomically under the existing session lock or transaction; it does not call the public `Append` recursively under that lock. The order inside that atomic step is fixed and identical in both stores:

1. Validate the payload against the session, the message identity and the summary checksum.
2. Authorize the request. An invalid payload or an unauthorized request is refused before the identity lookup, so a refused request never exposes a stored publication.
3. Look up the publication identity. An existing identity that is identical returns its original committed append version as a no-op: no second message is appended and the version does not advance, even when `expectedVersion` is stale. An existing identity whose payload conflicts is an error and mutates nothing.
4. For an absent identity, compare `expectedVersion`. A stale value returns `types.ErrVersionConflict` (`core/types/errors.go:40`) and appends nothing.
5. For an absent identity and a current `expectedVersion`, append the one message carrying the deterministic identity and advance the version by exactly one.

Duplicate-first is chosen because a retried publication is the same fact under the same identity, so its recorded version is already correct, while a stale-version check before the identity lookup would refuse a publication that is already durable and force the caller to re-plan a compaction that has already happened. The conflicting-identity refusal stays separate from `types.ErrVersionConflict`: the first means two different payloads claim one identity, the second means the appender lost a race for a publication that does not exist yet. Memory takes one lock; PostgreSQL uses transactional uniqueness and the existing chain serialization. `AppendCompactionAudit` keys on `record.SessionID` and `record.Compaction.PublicationID`; `AppendCompactionEvent` keys on `runID` and `publicationID`. Both return true only when that call inserted the record, false for an identical existing payload, and an error for a conflicting payload. `AppendCompactionEvent` assigns the sequence only on insertion, so a duplicate never advances it. Event keys are persisted independently of the retained event window, so eviction cannot permit republication. The driver binds `AppendCompaction` into the existing `HistoryAppender` execution path without widening `HistoryAppender` (`core/drive_lifecycle.go:92-97`); it discovers the two sink interfaces by type assertion. `Build` refuses an active compactor whose session store lacks `SessionCompactionAppender` or whose configured sink lacks its interface.

`reconcileCompactionPublications` inspects every durable publication loaded with the session, not only the newest rendered block. It runs at every actual provider invocation boundary, which is three distinct places, and the harness owns all of them:

```go
func reconcileCompactionPublications(ctx context.Context, loaded []types.Message) error
```

- the native turn, immediately before the model call in `modelEffect` (`core/drive_turn.go:294-320`);
- the foreign path, immediately before the governed model calls `Generate` (`core/foreign_conversation.go:102-103`), which the foreign `AgentRun` may reach without passing through harness assembly;
- the recovery and resume foreign wiring, which builds the same governed model before the runtime starts (`core/recover.go:274-278`, `core/resume.go:375-393`).

Reconciliation does not depend on an active `Compactor` and does not depend on assembly: a flow with no compactor and no collapse still reconciles, because the durable publication outlives the turn that published it. A configured successor, fallback or hedge invocation path must not bypass reconciliation; where a chain issues an additional provider call (`successorModelFunc`, `core/foreign_conversation.go:88`, and the hedge sink, `core/drive_turn.go:311`), the reconciliation runs before the outermost entry into the chain so every underlying provider call is covered, and it is idempotent, so the extra calls cost no additional sink records. It publishes to the original run partition under the durable `RunID`, skips an absent sink, and reconciles every configured sink. There is no transaction across `SessionLog`, `AuditLog` and `EventLog`: the session append is durable first, and each sink completes independently and idempotently. A decode, lookup, authorization or append error prevents the provider call. An already recorded identical publication allocates zero additional sink records and zero additional event sequence steps. Scenarios `context.compaction-publication-persisted`, `context.compaction-publication-reconciled`, `context.compaction-publication-once`.

Alternatives rejected: the store counter-assigned `Message.ID` as the identity (it is unknown before the append, so the driver cannot build the prospective request of D13); reading `AuditLog.Read` (`core/stores/audit.go:199`) or `EventLog.Read` (`core/stores/events.go:24-33`) before appending (a read cannot prove absence against a live stream, a bounded retention window, or a concurrent writer); the run lease as the duplicate-prevention argument (a lease protects one run, while the publication outlives it); a generic outbox (unnecessary scope for one producer).

### D11 — The additive payload is covered by a shared hash helper with backward verification

The memory and Postgres hash functions share one fixed field list today (`core/stores/audit.go:290-298`, `adapter/postgres/audit.go:196-205`); this change keeps that list byte-identical and appends one shared segment computed by:

```go
func CompactionAuditHashBytes(p *CompactionAudit) []byte
func CloneCompactionAudit(p *CompactionAudit) *CompactionAudit
```

`CompactionAuditHashBytes` returns nil bytes for a nil payload, so a record sealed before the payload existed still verifies and no separator is added to the legacy bytes. For a non-nil payload it encodes a domain tag and every field with deterministic length prefixes, integers as base-10 ASCII and strings as UTF-8 bytes. The memory hasher, the Postgres hasher, chain verification and both reseal paths (`core/stores/audit.go`, `adapter/postgres/audit.go:135-163`) use this one helper, so the two implementations cannot drift. `CloneCompactionAudit` gives every accept, read, compare and hash site its own copy, so no caller mutation moves the chain. Scenario `context.compaction-audit-hash`.

### D12 — `Build` refuses an active compaction without counting and a validated injected fallback

The admission finding: an active compaction could run with no counting source, and core cannot construct a fallback from `std/context`. The fallback is injected:

```go
func WithCompactionFallback(p stores.CompactionFallback) Option
```

`compactionFallback stores.CompactionFallback` joins the private config (`core/build_options.go:53` sits beside `estimator`), the `Stack` (`core/build.go:78` sits beside `estimator`), the resolved configuration and the turn configuration. No field is added to `ContextPolicy` or `History` (`core/stores/projection.go:17-33`). The port is declared beside `Projection`:

```go
type CompactionFallback interface {
	Projection
	ValidateCompactionFallback(p types.ModelProfile) error
}
```

`std/context.Truncate` implements `ValidateCompactionFallback` in `std/context/truncate.go`: it rejects a nil estimator and a non-positive usable budget. `Build` resolves the counter (D7) and, when a flow carries a compactor, refuses with an error naming flow and profile unless a count is resolvable and the injected fallback validates against the profile. A model that implements `types.TokenCounter` does not require `WithEstimator` when the injected fallback is valid, because the fallback's own estimator is what it reduces with. No default estimator, ratio or fallback is invented. The check lands beside the existing context-policy bind (`core/build_options.go:208-215`); a flow with no compactor is unaffected. Scenarios `context.compaction-counting-required`, `context.compaction-fallback-fails-closed`.

### D13 — The same turn measures the request it actually sends

The stage assembles a prospective post-append history using the predetermined message identity and version (D10), persists `TokensBefore` and the prospective `TokensAfter` in the publication payload, and then, after the append, loads the actual history and replaces the working history and its version. It reassembles through the complete shipped assembly composition — collapse, declared projections, blobs, redaction, operator-turn fence, prompt insertion (`core/native_effects.go:225-262`, `:277-299`) — and applies the same request options and provider declaration transformations used for the actual invocation (`core/drive_turn.go:268-280`). The measured `TokensAfter` MUST equal the durable value before the harness publishes to a sink or invokes the provider. A mismatch fails closed: no incorrect measurement is emitted and no provider call is made. This contract requires deterministic assembly for the same durable history and run inputs; a caller-supplied nondeterministic assembler cannot guarantee it, and the failure is the observable outcome rather than a silent wrong number. Scenario `context.compaction-same-turn-measurement`.

### D14 — Contract, registry, ADR and index edits land at apply time

Each Contract edit is assigned to the capability that actually owns the declaration. The `stores` Contract owns the `AuditCompacted` kind, the additive `AuditRecord.Compaction` payload, the three optional compaction interfaces and the shared hash helper (`openspec/specs/stores/spec.md:269-311`); the `Compactor` declaration is not declared there, so the stores Contract edit does not carry it. The `context` Contract already declares `Compact(ctx context.Context, h History, p ModelProfile) (Compaction, error)` (`openspec/specs/context/spec.md:25-27`), so the source migration (task 1.4) makes the code match the Contract and no `context` Contract edit is needed for the signature; `openspec/specs/context/spec.md` gains only the new requirements' main-spec text. `openspec/scenarios.json` gains the proposed ids; `docs/design/types.md` is regenerated (`task spec:types`). Every one of these is a tracked task in `tasks.md`. The main specs, the registry, the ADR log and the generated index are driver-owned apply-time work; the implementation driver runs them after the source migration lands, never inside a coder task. No ADR number is reserved during authoring: the numbers are assigned when the apply-time documentation tasks run, and the ADR log entry records that ADR-0090 and ADR-0157 already decide the shapes and that an additive record change needs no upcaster (`openspec/specs/stores/spec.md:338`).

### D15 — ADR closeout log: what each prior record already owns

One table fixes the boundary between the prior records and the compaction ADR, so the apply-time documentation tasks cite instead of re-decide.

| Surface | Decided by |
|---|---|
| Projection vs persisted compaction, `CoversUpTo` hiding, the summary invariants (no split pairs, no pending-call coverage, `OriginModel`, `StageContext` guard, model-bound opaque compaction) | ADR-0090 (`docs/adr/0090-context-management.md:5-19`) |
| Core-owned ports, `stores.RecordEnvelope` with `SchemaVersion`, `DecodeRecord[T]`, canonical argument retention | ADR-0157 (`docs/adr/0157-capability-ports-record-envelopes-and-journal-arguments.md:41-120`) |
| Publication identity and the `AppendCompaction` order, the provider-invocation reconciliation boundary, one trigger with two measurements, the three `Build` refusals, and the additive payload needing no upcaster | ADR-0163 as amended (`docs/adr/0163-persisted-compaction-owns-its-publication-and-its-build-gates.md:65-93`) |

The ADR log entry records this mapping and nothing else: a later reader of ADR-0163 sees which paragraphs it owns and which two records it cites, and neither cited record is amended by this change.

## Risks / Trade-offs

- [Risk] The collapse step drops a message a projection still needs, or the wrong ones under a rejected `CoversUpTo`. → Mitigation: hiding uses only `CoversUpTo` against the store's `Version == len(Messages)` invariant (`core/stores/session_memory.go:381`), the boundary check runs before the append, and the view step is asserted against `core/projection_invariants_test.go` (pending-call survival, adjacency) which must stay green.
- [Risk] Guarding after the append would persist a rejected summary. → Mitigation: D5 orders guard → append, and `context.summary-guarded` asserts nothing is persisted.
- [Risk] Extending `AuditRecord` touches a `stores` Contract block. → Mitigation: the payload is additive and the edit is declared in D6; the envelope carries the record's `SchemaVersion` regardless, and no upcaster is required.
- [Risk] A `Summarize` without the M4 prompt produces a summary the model never sees the intent of. → Accepted and stated: M2 ships the guarded, persisted mechanics; the policy surface is M4, and a nil `SummarySource` is inert rather than silently persisting.
- [Risk] Two drivers reconcile the same publication and one allocates a second record. → Mitigation: the atomic identity-keyed appends of D10 return false for an identical existing payload and error on a conflicting one, so the store is the deduplication barrier and the run lease is not relied upon.
- [Risk] The appended hash segment changes a chain that already carried payload-bearing records. → Mitigation: the fixed field list is untouched and a nil payload writes no segment bytes, so pre-change records verify byte-for-byte; memory and Postgres share one encoding (D11).
- [Risk] A compactor returns a `CoversUpTo` different from the selected prefix. → Mitigation: the stage refuses any mismatch, persists nothing, and the invariant file stays green (D3).
- [Risk] A foreign runtime invokes `Model.Generate` before any harness assembly, so a compaction publication is never reconciled on that path. → Mitigation: reconciliation is bound at the invocation boundaries (D10), not through assembly, and the foreign model's `Generate` is one of them; the public-path suite exercises a foreign run with no active compactor (tasks 7.1, 7.2, 8.2).
- [Risk] Middleware creates an additional provider call, so an invocation-scoped reconciliation is entered twice. → Mitigation: reconciliation is idempotent and the sinks are the deduplication barrier, so the second entry allocates no additional record and no event sequence step (D10); the `context.compaction-publication-once` scenario asserts the budgets under concurrent entry.
- [Risk] A duplicate-first `AppendCompaction` retry returns before authorization exposes stored publication state. → Mitigation: authorization and validation are the first two steps of the atomic order, before the identity lookup (D10).
- [Risk] The breaking `Compactor` result-type migration leaves a caller or an implementation on the old signature. → Mitigation: task 1.4 migrates the declaration, `std/context.Summarize` and the drive stage in one change, and the build gate of the same task fails on any leftover implementation; the rollback moves the declaration and its callers together.
- [Risk] A caller supplies a nondeterministic assembler, so the same-turn measurement cannot reproduce the prospective value. → Mitigation: the mismatch fails closed (D13) and is reported rather than accepted; deterministic assembly is a stated precondition of the contract.

## Migration Plan

One additive goose migration, proposed as `adapter/postgres/migrations/<next-free-goose-version>_compaction_publication.sql` with its version selected at apply time as the next free goose version (D10), adds `publication_id text` to `audit_record` and `event_record` with two partial unique indexes (D10). It alters no existing column, drops none, and backfills nothing: existing rows stay NULL and keep verifying. No other table, column or index for compaction is added by any other package. Beyond that migration the change reconciles the `Compactor` signature, adds one assembly collapse step, one `std/context` compactor, one drive stage with the safe-prefix selection, the durable publication payload and the reconciliation protocol, and the `AuditCompacted` payload. `Build` now refuses an active compaction policy without a resolvable counter or a validated injected fallback (D12) — a new build-time validation, not a data change. A `MemorySessionLog` or `adapter/postgres` session store must implement the optional `AppendCompaction` before any flow may declare a compactor; a flow with no compactor and a log with no `Compaction` block behaves exactly as today. Existing audit chains stay valid, so no reseal of history is needed. `docs/design/types.md` is regenerated for the changed declarations; no version is cut by this change. Shared driver files (`core/native_effects.go`, `core/drive_turn.go`) follow the milestone's admission-order rule (`openspec/changes/m2/tasks.md:19`): the `m2-sandbox-lifecycle-resume` lane is admitted after this one, and the two lanes never touch a shared driver file concurrently.