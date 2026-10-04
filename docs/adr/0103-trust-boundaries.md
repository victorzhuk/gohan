# ADR-0103: Session ownership, resume boundary, credentials off `Principal`, `Stores` bundle

Status: accepted · Amended by ADR-0121 (approval eligibility). · Origin: review round 29 (2026-09-30)

## Decision

Sessions are owned: `SessionLog` records `SessionOwner{Tenant, Subject}` at first append; `Send`, `Resume`, `Inspect`, shared state and `evals.Import` require the ctx principal to match tenant and subject or hold `session:read`/`session:write`, else `ErrSessionForbidden` before any store read; child and shadow sessions inherit the owner. `Resume` fails with `ErrResumeInsideRun` when ctx carries a `RunInfo`. `Approve`, `Reject`, `EditArgs`, `ApproveScope` take no principal; the harness sets `Approver` from the verified transport principal. `Principal` loses `Token`; `Credential` travels only in ctx via `WithCredential`, and `CredentialSource.Credentials` returns a `Credential`. `Build` takes `WithStores(Stores)` with a bundle type; options are declared in a code block and `std` presets are `Option`s.

## Context and evidence

Two bypasses existed by construction: any caller with a session ID could act on any tenant's session, and any code inside a run could self-approve its pending token by calling the public constructors. Both are cheap to close before M0 and API-breaking after M0.5. The S1 example did not match the `build` option list.

## Consequences

Five identity scenarios; `stores.History` gains `Owner`; `suspension`/`permission` constructors change; `build` code block and S1 example updated; tasks 4, 7, 24 updated.
