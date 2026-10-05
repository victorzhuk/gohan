# Tasks

## 1. Build declaration and policy enforcement

- [ ] 1.1 Add `WithEgress`, `WithHTTP`, `WithExfil`, `WithPrivateRead` and `EgressClient` in `core/tool.go`, `Network` on `ToolSpec`, and the audit `Host`/`Reason` fields and `egress_denied` kind in `core/stores/audit.go`; verify `go test -short -timeout 2m ./core/... -run 'TestToolEgress'`.
- [ ] 1.2 Wire `RequiresEgress`, `CheckEgress`, `DeriveExfil` and the `WithEgressClient` factory into Build, resolving one client per tool spec that carries a policy and failing with `ErrEgressClientRequired` when a policy has no factory; verify `go test -short -timeout 2m ./core/... -run 'TestEgressBuild'` covers network-without-policy refusal before provider or tool calls and policy-derived `Capabilities.Exfil`.
- [ ] 1.3 Bind the resolved client on the call context with `withEgressClient` around the tool function only, and map a `types.EgressDenied` error to `Failed(Permanent)` in `classifyFailure`; verify `go test -short -timeout 2m ./core/... -run 'TestEgressClient'`.

## 2. Egress client and HTTP tool

- [ ] 2.1 Implement `std/egress.Client` with address checks, checked-address dialing, redirect validation, cross-host `Authorization` stripping, schemes, byte limits, timeouts, and supported proxy behavior; verify `go test -short -timeout 2m ./... -run 'TestEgressClient'`.
- [ ] 2.2 Record the refusal in the driver: extend `classifyFailure` to take the call's context and spec, map a `types.EgressDenied` to `Failed(Permanent)`, append the `egress_denied` audit record through `AuditLogFrom`, and count `gohan.egress.requests{decision=denied}`; verify `go test -short -timeout 2m ./core/... -run 'TestEgressAudit'` and assert bodies never appear in audit fields.
- [ ] 2.3 Implement `std/tool/http.Fetch` with the selected typed input and output and register its network capability and policy; verify `go test -short -timeout 2m ./... -run 'TestHTTPFetch'`.
- [ ] 2.4 Cover the client behaviour the requirement states and no owned scenario covers: a disallowed scheme is refused before any dial, `Timeout` cancels a slow response, a cross-host redirect strips `Authorization`, an `http` proxy is used with each hop validated, and a non-`http(s)` proxy URL fails construction with `ErrEgressProxyUnsupported`; verify `go test -short -timeout 2m ./... -run 'TestEgressClient'`.

## 3. Scenario behavior and package gates

- [ ] 3.1 Add public-path subtests named exactly `tools.egress-metadata-blocked`, `tools.egress-redirect-revalidated`, `tools.egress-rebinding-safe`, `tools.egress-size-limit`, and `tools.egress-audited`; verify `go test -short -timeout 2m ./... -run 'Test.*Egress'`.
- [ ] 3.2 Run the repository wrappers and resolve failures; verify `task lint` and `task test`.
- [ ] 3.3 Validate the change and M1 milestone acceptance after all required M1 owners land; verify `openspec validate m1-egress-policy --strict --no-interactive` and `task spec:gate GATE=M1`.
