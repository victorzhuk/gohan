# Spec Delta

## ADDED Requirements

### Requirement: Conversation handle construction

Both `NewNativeConversation` and `NewForeignConversation` MUST bind the
`Stack`, the tenant, the title profile and the frozen flag values onto the
conversation handle before any option runs. Shutdown admission, tenant-scoped
flag snapshots and titling MUST therefore behave the same on every
constructor. A handle built without a `Stack` MUST NOT silently skip
shutdown admission.

#### Scenario: constructor bindings match
ID: `streams.handle-constructor-bindings`
- WHEN a native conversation and a foreign conversation are built over one stack that carries a tenant, a title profile and frozen flags
- THEN both handles admit runs and expose the same tenant, title profile and frozen snapshot

#### Scenario: native and foreign share stack state
ID: `streams.handle-native-foreign-share-stack`
- WHEN `Shutdown` begins after either constructor returned
- THEN a `Send` on either handle is refused with `types.ErrShuttingDown`

### Requirement: Active-run discovery

The handle MUST expose
`ActiveRun(ctx context.Context, sessionID string) (string, error)`. It MUST be
owner-authorized like `Cancel`. A run is active only when its state is
`Running` or `Resuming` and its lease is live. A `Suspended`, terminal or
lease-expired run is inactive. `ActiveRun` MUST return the ID of an active
run, or `types.ErrRunNotActive`. It MUST resolve a run from another pod
through `SessionRunFinder`. It MUST NOT change `Send` or require an
idempotency key.

#### Scenario: active run is found
ID: `streams.active-run-found`
- WHEN the owner calls `ActiveRun` while a run for that session is active
- THEN it returns that run's ID and a nil error

#### Scenario: absent run reports not active
ID: `streams.active-run-absent`
- WHEN the owner calls `ActiveRun` and the session has no active run
- THEN `types.ErrRunNotActive` is returned with no ID

#### Scenario: inactive run reports not active
ID: `streams.active-run-terminal-or-expired`
- WHEN the session's run is `Suspended`, reached a terminal state, or its lease expired
- THEN `ActiveRun` returns `types.ErrRunNotActive` and no ID

#### Scenario: discovery is owner-authorized
ID: `streams.active-run-owner-authorized`
- WHEN a principal of another tenant, or a principal without session write scope, calls `ActiveRun`
- THEN `ErrSessionForbidden` is returned and no run ID is disclosed

#### Scenario: cross-instance discovery
ID: `streams.active-run-cross-instance`
- WHEN the run was started on another pod and the runs store implements `SessionRunFinder`
- THEN `ActiveRun` returns that run's ID

#### Scenario: detached run is discoverable
ID: `streams.detached-run-discoverable`
- WHEN a detached run is `Running` or `Resuming` with a live lease
- THEN `ActiveRun` returns its run ID
