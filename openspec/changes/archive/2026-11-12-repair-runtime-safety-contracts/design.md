# Design

## Context

See `proposal.md` for motivation. The current audit is grounded at commit `544d11a`; its sixteen reproduced findings and eight source-traced findings are mapped below. Existing research-report edits remain independently owned.

The root module is standard-library-only. Public ports are frozen. Current receipt and fingerprint encodings are persisted, and PostgreSQL and memory stores must implement the same ownership behavior. The archived egress design accepted proxy-side DNS, although the current tools contract promises checked-address dialing.

## Goals / Non-Goals

Restore the existing governance and durability guarantees through public conversation and store boundaries. Keep frozen method sets, existing explicit expiry records, and legacy snapshot reads compatible. Use local helpers for implementation repairs; new seams are limited to the currently missing recovery identity and explicit checkpoint lifetime policy.

This change does not add capabilities, upgrade third-party libraries, publish releases, rewrite tags, execute production migrations, or alter the independently edited research-report change.

## Decisions

### 1. Approval is call-bound authority

Ordinary receipts authorize only the recorded run, argument generation, call ID, tool and complete argument value. They remain readable for deduplication and `DiffFromLast`. Reusable authority is exclusively a subject-keyed, bounded `ApproveScope` grant.

Use the existing invocation `RunInfo` and resume approval context. Strengthen their private approved-call key with the complete arguments and generation. Resolve the current policy before each decision and execution. Revalidate every counted approval against current scope, exclusions, separation and quorum rules. Preserve duplicate-approver refusal and the token on an insufficient or ineligible decision.

The test expecting an ordinary receipt to grant an unrelated later call is inconsistent with the normative permission contract. Replace that expectation with rejection and retain the separate scoped-grant tests. Do not retain the unsafe expectation through a compatibility bypass.

### 2. Argument identity preserves exact values

Canonicalize validated JSON without converting numbers through `float64`. Normalize equivalent decimal spellings exactly, including nested arguments. Use a versioned exact fingerprint for new approval and journal identities. Existing fingerprint strings do not constitute exact-value proof.

Legacy scoped grants require renewed approval. Existing ordinary receipts carry full arguments and remain usable only for their complete recorded call identity. For an unresolved legacy journal intent, query the legacy fingerprint as a compatibility candidate, compare its stored full arguments using exact canonicalization, then reuse the original entry and downstream key only on an exact match. Preserve the original stored fingerprint when reserving that legacy entry. Missing argument evidence fails closed. Successful completed intents retain the existing fresh-call behavior.

Do not rewrite journal history or infer identity from a rounded legacy fingerprint. Update numeric rendering in `DiffFromLast` so the approval UI shows the same distinctions the gate enforces.

### 3. Effect and argument enforcement happen before execution

Resolve the host runner's effective effect once in its advertised `ToolSpec`. Explicit `WithEffect` wins, an existing nonzero declared effect remains honored, and an unset declaration defaults to `SideEffect`. Timeout outcomes, gate, journal, shield and scheduler all read that declaration.

Install the reserved journal key on initial execution and replay. Keep the existing rule that a completed successful intent does not suppress a genuinely new call.

Retain each tool's exclusion matcher and apply it recursively at JSON decoding, using the same field-name rules as schema derivation. Excluded fields remain zero in the typed callback. Preserve rejection of malformed JSON and duplicate keys.

### 4. Every lease acquisition uses one session ownership check

Memory `Start`, `Resuming` and `Reclaim` use the same locked session-owner predicate and update `onSess`. PostgreSQL uses the existing session serialization lock before the target run lock. Reclaim also verifies the current state is `Running` or `Resuming`. A competing live session run refuses acquisition without changing either run.

PostgreSQL ownership timestamps and comparisons come from database time inside the relevant transaction. Read `clock_timestamp()` after ownership locks and use the returned instant consistently for that transition. Apply the same source to checkpoint expiry, recovery discovery, workspace-owner checks and notice claims. Retain exported `WithClock` as an explicit testing override. Without that override, no process clock supplies ownership time. Tests establish production skew resistance using independent adapter instances without the override.

### 5. Checkpoint lifetime belongs to the checkpoint store

Remove the driver's `ExpiresAt = lease.Expires`. Add `WithMemoryCheckpointTTL(time.Duration)` and `postgres.WithCheckpointTTL(time.Duration)` constructor options. For a checkpoint with zero `ExpiresAt`, a positive configured TTL makes `Put` compute expiry from the store clock before encoding. An explicit `ExpiresAt` keeps its existing meaning. Zero TTL retains the store's existing no-automatic-expiry behavior. Negative policy is refused before a checkpoint is written.

There is no new lifetime default in core and no method or field change on `Checkpoints` or `Checkpoint`. Applications requiring finite suspension lifetimes must pass a finite store policy. This explicitly replaces the accidental execution-lease deadline.

### 6. Recovery acquires the current suspension token

Proposed optional surface on `Runs`, declared in `core/stores`:

```go
type CheckpointRuns interface {
	SuspensionToken(context.Context, string) (types.ResumeToken, error)
	ResumingCheckpoint(context.Context, string, types.ResumeToken, time.Duration) (Lease, error)
}
```

`SuspensionToken` reports only the current suspended token. `ResumingCheckpoint` atomically verifies suspended state, the same token and exclusive session ownership before minting a lease generation. Existing frozen `Runs` methods remain unchanged.

External resume requires this surface before consumption. An absent surface returns `ErrCheckpointIncompatible` with the token untouched. Automatic preempted or consumed-token recovery skips that optional path when the required surface is absent; ordinary stale-run recovery remains available. Concrete stores implement it.

Pre-consumption refusals preserve the token. A failure acquiring the lease after successful conditional consumption preserves the recorded decision for recovery instead of pretending the token remains unused.

Preemption recovery reads the current token, peeks and verifies `Preempted`, conditionally consumes `Continue()`, then acquires the lease for that exact token. Consumed discovery uses the persisted consumed bit, never a guess from nonzero resume input. Discovery filters current token, suspended run, expiry and reason before applying a limit.

Memory checkpoint discovery receives the run surface through `WithMemoryCheckpointRuns(CheckpointRuns)`. Its discovery takes a checkpoint snapshot before consulting Runs and rechecks the token at acquisition, avoiding a checkpoint/run mutex cycle. An unbound memory store refuses the recovery-discovery operation explicitly. PostgreSQL filters checkpoint and run rows in the query. Update all concrete wiring and conformance factories that exercise this optional behavior.

Use the existing PostgreSQL suspension-reason columns for bounded preempted discovery. Memory retains the reason from `SuspendWithReason`. Legacy reasonless records require the current checkpoint's reason before selection. Do not infer a human approval to be a preemption merely because its run is suspended. No new SQL column is planned.

### 7. One durable event and accounting boundary

Reuse the existing conversation event-recording semantics for recovery: persist each event, terminal failure and boundary event with the next stored sequence before proceeding. A recovered terminal event must be visible to a fresh conversation's `Attach`. Keep exactly one final boundary event.

Check `finishTurn` errors before restoring any carried `Done`. Persist the cumulative admitted tool count, restore it once on resume or recovery, and avoid charging an already admitted pending call again. Preserve the existing cost and terminal-reason behavior.

### 8. Providers satisfy the public tool-stream contract

Anthropic tracks tool identity and arguments per content-block index, emits identity-bearing argument previews, and emits one complete `ToolUse` per completed block. Preserve forced-schema text output and mark unfinished arguments as truncated rather than executable. Update fixtures to include actual block boundaries.

OpenAI emits the same argument-preview/completion protocol, stops permanently after a rejected yield or terminal error, and emits estimated usage only on clean completion without provider usage. Missing optional cache details produce zero cache tokens, not a panic.

Install the existing `NewToolCallIDHandler` in Eino's internal `ChatModelAgent`. The pinned Eino API supports it. Verify through `Build` and `NewForeignConversation`, including approval and journal execution, rather than only direct runtime doubles.

### 9. Network administration is outside workload authority

A trusted namespace anchor starts from the configured trusted base image, never from a restored workspace snapshot. It owns network administration and establishes the complete policy before any workload container starts. The workload then joins the anchor's namespace, drops all capabilities, enables `no-new-privileges`, overrides the image entrypoint and disables inherited healthchecks. Keep the existing UID behavior and file API semantics. A restored snapshot can replace its idle executable or loader, so entrypoint override alone is not a pre-policy execution barrier.

The anchor uses a separate trusted root filesystem and holds `NET_ADMIN` for setup and counter reads. Never administer policy through public workload `Exec`. Label anchors, associate them with their workload resource, and remove both on close or failed setup. Enumeration and crash cleanup must recognize orphaned anchors and preserve anchors serving live workloads. Bound each administration operation to ten seconds. Deny-all keeps `NetworkMode: none` and needs no anchor.

Return the immutable image ID from `ContainerCommit`. Retain reads and cleanup of existing labelled tagged snapshots. Enumeration and close-by-ref must also recognize labelled immutable snapshot IDs. Two publications from the same live workspace must never change the content behind an earlier reference.

### 10. Egress pinning applies through proxies

Add the explicit CGNAT prefix to `DenyPrivate`, preserving the existing explicit literal allow exception.

For supported HTTP proxies, send the already validated target IP as the proxy destination. Preserve the original HTTP Host, TLS server name, proxy credentials, per-response deadline, redirect checks and release behavior. Deny unsupported proxy modes before dispatch. This supersedes the archived acceptance of proxy-side DNS. Tests control independent client/proxy DNS results and prove the checked destination remains the actual peer.

### 11. Release checks use declared module dependencies

Move adapter root requirements to the existing `v1.0.0` root version. No third-party upgrade is required. Verify publication availability and checksums rather than substituting a local replacement for a passing release verdict. Examples remain a development workspace consumer of the new root interfaces/options, with their existing local wiring; they are not independently published and cannot claim compatibility with a released root that lacks the new options.

Add a verification-only adapter release task and CI coverage with `GOWORK=off`, no manifest replacements, tidy cleanliness and module conformance. The task produces no tag or publication. Adapters implement the optional recovery methods structurally using already released parameter/result types; avoid a compile-time assertion against an unpublished root interface. Repair `tools/apicheck` using standard-library type information and module-aware package/export loading. Detect generic method removal, inferred type changes and imported type identity changes while accepting the documented additive keyed-struct growth. Keep baseline and candidate module dependency graphs separate.

The historical adapter `v1.0.0` baseline declares the erroneous root `v0.1.0` dependency. Its matching root `v1.0.0` tag points to the same release commit. Permit one explicit baseline-manifest correction in a temporary comparison copy, limited to those verified historical adapter tags and that root requirement. Print the original requirement, corrected requirement and exception. Keep every other dependency unchanged and reject unrelated load failures. This comparison exception neither repairs nor hides the original release defect, modifies a tag, nor substitutes a local root in independent release verification.

Run examples through the workspace acceptance wrappers. Independent examples publication would require published root and adapter versions containing its newly used APIs and is outside this implementation's publication authority.

### 12. Validation repairs preserve assertions

Replace public DNS in the standalone Docker allowlist test with existing controlled network fixtures. Correct the fourteen unchecked iterator errors without disabling lint or dropping assertions. Update the existing architecture, compatibility, testing and changelog documents to describe the resulting behavior. Add an ADR and regenerate the types index when synchronizing approved capability deltas.

## Finding Coverage

| Findings | Decision | Primary paths |
|---|---|---|
| 1 | 3 | `std/tool/exec` |
| 2-3 | 1 | `core/approval_receipt.go`, `core/resume.go`, `core/scoped_approval.go` |
| 4 | 2 | `core/tool_fingerprint.go`, `core/permission/approval.go`, `std/journal.go` |
| 5-6 | 3 | `core/tool.go`, `std/journal.go` |
| 7-8 | 4 | `core/stores/runs*`, `adapter/postgres` |
| 9 | 5 | `core/lifecycle_suspend.go`, checkpoint stores |
| 10-12 | 7 | `core/drive_turn.go`, `core/recover.go`, lifecycle/event helpers |
| 13-14 | 6 | `core/recover_preempted.go`, run/checkpoint stores |
| 15-18 | 8 | `adapter/anthropic`, `adapter/eino`, `adapter/openai` |
| 19-20 | 9 | `adapter/docker` |
| 21-22 | 10 | `std/egress` |
| 23-24 | 11 | module manifests, Taskfile, CI, `tools/apicheck` |

## Risks / Trade-offs

- Existing stores implementing only frozen ports cannot prove token-aware resume. They fail closed for resume or omit optional automatic recovery until extended.
- Zero checkpoint TTL means no automatic expiry. Finite retention is an explicit caller policy and requires application wiring.
- Legacy rounded grants lose authority. Unresolved journal intents need exact stored argument evidence to retain a pinned key.
- Privileged Docker setup adds namespace anchors. Labelled cleanup, workload ownership and controlled fixture tests are required before acceptance.
- Proxy pinning can expose proxies that reject IP destinations. Those configurations fail explicitly instead of retaining an unproved DNS guarantee.
- Published module access may fail in the local environment. Independent release verification remains incomplete in that case.

## Migration Plan

Use additive record fields and migrations only. Read existing explicit checkpoint expiry and legacy snapshot references unchanged. For fingerprint-format deployment, drain old side-effect workers before transferring ownership to the new version; do not mix old and new intent lookup indefinitely. New code reads safe legacy journal candidates. A rollback first drains new side-effect workers and preserves their journal records; it must not resume uncertain new-format intents through old fingerprint lookup.

Do not run production migrations, publish modules, or move tags as part of implementation. Review generated schema and dependency changes with the code. Update release order documentation to require a published root containing any newly required optional interfaces before an adapter release uses them.

## Acceptance and Recovery Limits

Testing depth is existing-service-strict because authority, external effects and durable state change. Primary acceptance uses public `Build`, conversation, store and workspace APIs. Provider HTTP and network peers are controlled fixtures. PostgreSQL tests use a real isolated container. Harness time tests use deterministic time.

Focused tests cover residual risks that broad acceptance cannot prove economically: numeric canonicalization, rare consume/lease crash windows, concurrent lock ordering, iterator early break, injected terminal-write failure and API compatibility mutations.

Run affected scenarios first, then `task lint`, `task spec`, `task test`, targeted race checks, full PostgreSQL conformance, `task tools:test`, `task examples:test`, `task api:check` and independent adapter dependency verification. The M2 gate remains an additional milestone check; do not lower the existing gate floor.

Ordinary focused runs have a three-minute process deadline and a two-minute Go test timeout. Full module/store runs have a twenty-minute process deadline and a ten-minute test timeout. Use two build/package workers and two parallel tests; lint uses two workers. Preserve configured Go cache and temporary build paths. Kernel-emitted commands, if separately invoked, retain their literal execution contract.

Allow one local fixture/environment repair and at most two further diagnostic reruns per failing boundary. A retry pass is not acceptance for a flaky required scenario. Stop for a changed contract, destructive operation, missing credential, unresolvable published dependency, or exhausted repair limit. Do not weaken assertions or change security policy to obtain a passing run.
