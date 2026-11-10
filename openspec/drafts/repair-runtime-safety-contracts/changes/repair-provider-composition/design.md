# Design

## Context

This child implements parent decision 8 through public provider/runtime composition.
The authority and recovery children establish the governed identity and accounting boundaries it must reach.
These are intended repairs, not claims that current decoders satisfy them.

## Decisions

### Anthropic block ownership

Track tool identity and argument fragments separately for each content-block index.
Emit argument previews with the corresponding call ID.
Emit exactly one executable `ToolUse` after that block completes with full arguments.
Parallel blocks must not share accumulation.
Preserve forced structured-output text mapping.
Mark unfinished arguments as truncated and never execute them.
Fixtures must include actual block boundaries rather than decoder-only shortcuts.

### OpenAI finality and optional usage

Use the same identity-bearing preview/completion protocol.
Return permanently when the consumer rejects a yield.
After a terminal stream error, yield that error once and return.
Emit estimated usage only after clean completion without provider usage.
Release the response body on every exit, including early consumer stop and decoding failure.
Treat missing optional cache details as zero cache-token counts under `CacheAuto`.

### Eino governed composition

Install the existing `NewToolCallIDHandler` in Eino's internal `ChatModelAgent`.
Use the pinned API without upgrading dependencies.
Verify `Build` and `NewForeignConversation`, not only direct runtime doubles.
Carry backend call IDs through permission, accounting, journal and approval resume.
An authorized tool executes exactly once with the backend identity.

## Scope and Ordering

Integrate `repair-call-authority` and `repair-token-recovery` before this child.
Preserve their shared root/testkit contracts and registry entries.
The manifest orders subsequent sandbox, network and release edits to shared docs, tests and changelog.
The release child owns adapter manifest pinning and independent compilation.

## Acceptance

Use controlled provider HTTP fixtures for completed, parallel, forced-schema and unfinished tool blocks.
Observe execution through public native conversations and the foreign constructor.
Use focused iterator early-break, stream-error and body-release assertions where broad tests cannot prove finality.
Retain all existing conformance assertions and test all three affected adapters.
Do not count a direct decoder test as proof of public governed composition.

## Limits and Compatibility

No third-party upgrades, frozen method changes, publication or tag rewrites are authorized.
Do not modify independently owned research-report artifacts.
Update existing adapter/testing docs and changelog entries without inventing new architecture documents.

Focused commands have a three-minute process deadline and a two-minute Go test timeout.
Full module/store commands have a twenty-minute process deadline and a ten-minute test timeout.
Use two package workers, two parallel tests and two lint workers.
Preserve configured cache and temporary build paths.
Allow one local fixture repair and at most two further diagnostic reruns per failing boundary.
Do not treat a flaky retry as acceptance.
Stop for changed contracts, destructive operations, missing credentials, unresolved published dependencies or exhausted repair limits.
Parent integration owns broad validation and cross-child event/identity checks.
