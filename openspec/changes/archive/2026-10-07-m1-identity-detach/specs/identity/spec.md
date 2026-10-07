# Spec Delta

## Context

This delta MODIFIES the `identity` *Context values* requirement, which is where the three scenarios this change owns live (`openspec/specs/identity/spec.md:200-225`). The requirement block carries no normative prose and five scenario blocks; this delta is a strict superset: it reproduces all five scenarios unchanged and adds the three enforcement sentences the requirement lacked. The `Identity` requirement is NOT touched: it already carries the two sentences an earlier change added (`RunInfo.Flags`, notice-endpoint isolation), and a requirement absent from a delta is left intact by the archive merge, so this package ships no `Identity` delta at all.

## MODIFIED Requirements

### Requirement: Context values

The idempotency key is transport-only: it orders retries of one logical call and MUST NOT become run identity. It MUST NOT enter `RunInfo`, and a sub-flow invoked through `FlowAsTool` MUST NOT inherit the caller's key or approval — the child observes `IdempotencyKey` and `ApprovalFrom` as `ok == false`. The cancel shield MUST preserve the identity values: a `SideEffect` tool running under `context.WithoutCancel` MUST observe the run's `Principal`, `Credential` and `RunInfo` unchanged. A `Detached` run MUST re-issue its credential through `CredentialSource` for the copied principal, and MUST NOT reuse the caller's `Credential`; the fresh token stays in `ctx` and is never logged, emitted or persisted.

#### Scenario: accessors outside a run
ID: `identity.accessor-outside-run`
- WHEN `PrincipalFrom`, `CredentialFrom`, `ApprovalFrom`, `RunInfoFrom`, `IdempotencyKey` and `SharedState` are called on `context.Background()`
- THEN each returns `ok == false` and zero values without panicking

#### Scenario: values survive the cancel shield
ID: `identity.values-survive-shield`
- WHEN a `SideEffect` tool runs under the cancel shield after the caller's ctx is cancelled
- THEN `PrincipalFrom`, `CredentialFrom` and `RunInfoFrom` still return the run's values inside the tool

#### Scenario: detached run re-issues credential
ID: `identity.detached-reissues-credential`
- WHEN a `Detached` flow is started with a `Credential` in the caller's ctx
- THEN the harness ctx carries the caller's `Principal` and `RunInfo`, `CredentialSource.Credentials` is called for the principal, and the caller's `Credential` value is not reused

#### Scenario: credential not in sandbox
ID: `identity.credential-not-in-sandbox`
- WHEN a `sandbox` tool set executes a command while a `Credential` is present in the run ctx
- THEN the process environment and mounted files contain no value derived from the `Credential`

#### Scenario: idempotency key set by transport only
ID: `identity.idempotency-key-transport-only`
- WHEN a tool calls `WithIdempotencyKey` on its own ctx and invokes a sub-flow
- THEN `task lint` fails on the depguard rule and, at runtime, the sub-flow's `IdempotencyKey` returns `ok == false`
