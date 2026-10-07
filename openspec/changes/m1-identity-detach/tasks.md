# Tasks

Delivered in `fix(identity): keep the idempotency key transport-only and re-issue the detached credential`. A task is done when its listed scenario IDs pass and `task lint`, `task spec` and `task test` are green.

## 1. Transport-only idempotency key

- [ ] 1.1 Add the harness-only `types.WithoutRunIdentity(ctx)` helper in `core/types/identity.go`: it returns a context whose idempotency-key and approval entries are hidden, keeping `Principal`, `Credential` and `RunInfo`; verify `go test -short -timeout 2m ./core/... -run 'TestWithoutRunIdentity'` and assert `IdempotencyKey` and `ApprovalFrom` return `ok == false` while `PrincipalFrom`, `CredentialFrom` and `RunInfoFrom` still return the set values.
  - evidence: `core/types/identity.go` (`WithoutRunIdentity`); `core/types/identity_test.go` (`TestWithoutRunIdentity`)
- [ ] 1.2 Build the child context in `FlowTool.Invoke` (`core/run_tree.go`) with `types.WithoutRunIdentity(ctx)` before `types.WithRunInfo`, so a sub-flow never inherits the caller's idempotency key or approval; verify `go test -short -timeout 2m ./core/... -run 'TestFlowToolDropsKey'` and assert the child's `IdempotencyKey` is `ok == false`.
  - evidence: `core/run_tree.go` (`FlowTool.Invoke`); `core/identity_detach_scenarios_test.go`
- [ ] 1.3 Add `examples/.golangci.yml` with a forbidigo rule that forbids `context.WithValue` in the examples module, run it from `task lint`, and move the `examples/kafka-refunds` delivery token off `ctx` (replace `withDelivery`/`deliveryKey` with an explicit field on the decider) so the module lints clean; verify `task lint` exits 0 and `go -C examples test -timeout 2m -short ./...` is green.
  - evidence: `examples/.golangci.yml`; `examples/kafka-refunds/fixtures.go` (no `context.WithValue`)

## 2. Shield identity-value invariant

- [ ] 2.1 Add a top-level `TestIdentityValuesSurviveShield` in `std/shield_scenarios_test.go` whose subtest is named exactly `identity.values-survive-shield`: run a `SideEffect` tool through the real `std.Shield` middleware with a caller context that is already cancelled, and assert `PrincipalFrom`, `CredentialFrom` and `RunInfoFrom` return the run's values inside the tool; verify `go test -short -timeout 2m ./std/... -run 'TestIdentityValuesSurviveShield'`.
  - evidence: `std/shield_scenarios_test.go` (`identity.values-survive-shield`)

## 3. Detached credential re-issue

- [ ] 3.1 Re-issue the credential at the detach seam: in `conversation.Send` (`core/stream_lifetime.go`/`core/conversation.go`), after `runIdentityCtx`, resolve a fresh `Credential` for the copied principal through `c.creds` and install it on the detached context with `WithCredential`, hiding the caller's value when no source is wired; verify `go test -short -timeout 2m ./core/... -run 'TestDetachedCredentialReissue'` and assert `CredentialSource.Credentials` is called once for the copied principal and the caller's token is not reused.
  - evidence: `core/stream_lifetime.go` (`detachedContext`/`Send` detach path); `core/identity_detach_scenarios_test.go`
- [ ] 3.2 Add the `identity.detached-reissues-credential` and `identity.idempotency-key-transport-only` subtests in `core/identity_detach_scenarios_test.go`, named exactly their IDs: the first driven through `Build` and `NewConversation`, the second through `Build` and the exported `FlowTool`; verify `go test -short -timeout 2m ./core/... -run 'TestIdentityDetach'`.
  - evidence: `core/identity_detach_scenarios_test.go` (`TestIdentityDetach` with the two ID-named subtests)

## 4. Package gates

- [ ] 4.1 Run the repository wrappers and resolve failures; verify `task lint` and `task test`.
  - evidence: driver-run `task lint` 0 issues (root and examples); `task test` green across root, `adapter/otel`, `adapter/openai`, `adapter/anthropic`, `examples`
- [ ] 4.2 Validate the change after all required M1 owners land; verify `openspec validate m1-identity-detach --strict --no-interactive` and `task spec:gate GATE=M1`.
  - evidence: driver-run `openspec validate m1-identity-detach --strict --no-interactive`; `task spec:gate GATE=M1`
