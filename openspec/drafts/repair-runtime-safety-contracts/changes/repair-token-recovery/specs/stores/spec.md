## ADDED Requirements

### Requirement: Every lease acquisition preserves session exclusivity

Start, resume and reclaim MUST atomically refuse another live run in the same session.
Every successful acquisition MUST update session ownership consistently.
Reclaim MUST accept only an expired stored `Running` or `Resuming` state.

#### Scenario: A suspended run resumes while a newer run owns the session
ID: `stores.resume-session-exclusive`
- WHEN run A is suspended and run B holds a live lease for the same session
- THEN resuming A returns `ErrRunActive` without changing either run

#### Scenario: Reclaim races another acquisition
ID: `stores.reclaim-session-exclusive`
- WHEN an older expired run is reclaimed while a newer run owns the session
- THEN reclaim returns `ErrRunActive` and the newer owner's lease remains usable

#### Scenario: A stored terminal or suspended run is reclaimed
ID: `stores.reclaim-state-refused`
- WHEN a caller attempts to reclaim a stored terminal or suspended run
- THEN reclaim returns `ErrRunNotActive` without minting ownership

### Requirement: PostgreSQL is authoritative for production ownership time

Without an explicit testing clock override, PostgreSQL MUST compute and compare persisted lease and checkpoint expiry times using database time.
Process clock differences MUST NOT change ownership, expiry, staleness or active-session discovery.

#### Scenario: Independent adapter instances have skewed process clocks
ID: `stores.postgres-ownership-database-time`
- WHEN independent adapter instances share a database and their process clocks differ
- THEN a live lease remains owned and an unexpired checkpoint remains resumable by database time

### Requirement: Checkpoint lifetime is independent store policy

The driver MUST NOT derive checkpoint expiry from its execution lease.
A positive configured checkpoint TTL MUST use store time at creation when explicit expiry is absent.
Zero TTL MUST retain no automatic expiry.
Explicit expiry MUST remain unchanged.
Negative policy MUST be refused before writing.

#### Scenario: A healthy execution outlives its initial lease
ID: `stores.checkpoint-lifetime-independent`
- WHEN an execution heartbeats past its initial lease deadline and then suspends
- THEN the fresh checkpoint remains usable until its own configured expiry

#### Scenario: Explicit and absent checkpoint expiry
ID: `stores.checkpoint-lifetime-policy`
- WHEN checkpoints are written with explicit expiry, a positive store TTL or neither
- THEN explicit expiry is preserved, configured TTL uses store time, and the absence of both leaves expiry unset

### Requirement: Optional checkpoint resume port

The optional `CheckpointRuns` port MUST expose this normative method set.
Existing frozen `Runs` methods MUST remain unchanged.

```go
type CheckpointRuns interface {
    SuspensionToken(context.Context, string) (types.ResumeToken, error)
    ResumingCheckpoint(context.Context, string, types.ResumeToken, time.Duration) (Lease, error)
}
```

#### Scenario: A token-aware store supports the optional port
ID: `stores.optional-checkpoint-resume-port`
- WHEN the concrete memory or PostgreSQL store is checked for token-aware resume support
- THEN it implements the optional port without changing the frozen run port

### Requirement: Optional checkpoint resume is token-fenced

The optional surface MUST expose the current suspended token.
It MUST atomically acquire a resume lease only for that token with exclusive session ownership.
Frozen `Runs` methods MUST remain unchanged.
A mismatched token MUST leave the current suspension untouched.

#### Scenario: An old token is used after a newer suspension
ID: `stores.resume-current-token-fenced`
- WHEN a run currently waits on token B and a caller requests a lease for token A
- THEN the request returns `ErrTokenMismatch` and B remains current

### Requirement: Recovery discovery limits eligible current checkpoints

Consumed-checkpoint discovery MUST filter current, unexpired, consumed tokens with suspended runs before its limit.
Preempted discovery MUST filter the actual current checkpoint reason before its limit.
Empty resume input MUST NOT determine consumption.

#### Scenario: Completed checkpoints precede a recoverable checkpoint
ID: `stores.resume-ready-filter-before-limit`
- WHEN older consumed checkpoints belong to completed runs and the discovery limit is one
- THEN a newer eligible consumed checkpoint is returned

#### Scenario: Human approval precedes a preempted checkpoint
ID: `stores.preempted-filter-before-limit`
- WHEN an older human approval and a newer preempted checkpoint are suspended and the limit is one
- THEN preempted discovery returns the preempted run only
