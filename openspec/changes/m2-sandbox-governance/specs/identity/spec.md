# Spec Delta

## Context

This delta MODIFIES the `identity` *Context values* requirement, which is where `identity.credential-not-in-sandbox` lives (openspec/specs/identity/spec.md:202-229). The requirement block carries no normative prose of its own beyond the sentences earlier changes added, and five scenario blocks; this delta is a strict superset: it reproduces those sentences and all five scenarios unchanged and appends the credential-clause sentences the block lacked. No other `identity` requirement is touched, so the `RunInfo.Flags` and per-tenant notice sentences added by earlier changes stay intact through the archive merge.

The block exceeds the 500-character guidance for one requirement because the archive merge replaces the block whole: the existing prose is 708 characters before this change and cannot be split without dropping sentences earlier changes own. The added clause is one sentence, and the same rule is stated under the `sandbox` *Secrets* requirement, where the `SecretRef` grant itself is defined.

## MODIFIED Requirements

### Requirement: Context values

The idempotency key is transport-only: it orders retries of one logical call and MUST NOT become run identity. It MUST NOT enter `RunInfo`, and a sub-flow invoked through `FlowAsTool` MUST NOT inherit the caller's key or approval — the child observes `IdempotencyKey` and `ApprovalFrom` as `ok == false`. The cancel shield MUST preserve the identity values: a `SideEffect` tool running under `context.WithoutCancel` MUST observe the run's `Principal`, `Credential` and `RunInfo` unchanged. A `Detached` run MUST re-issue its credential through `CredentialSource` for the copied principal, and MUST NOT reuse the caller's `Credential`; the fresh token stays in `ctx` and is never logged, emitted or persisted. The harness `Credential` MUST NOT be exported into a sandbox workspace under any policy: no process environment entry and no mounted file carries its value or any value derived from it. A workspace receives credentials only through a `SecretRef` the operator put in `SandboxPolicy.Secrets`, resolved for the run originator through the `SecretResolver` port and injected through `Workspace.SetEnv`.

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