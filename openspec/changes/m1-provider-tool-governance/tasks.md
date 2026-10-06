# Tasks: m1-provider-tool-governance

Delivered in `feat(tools): govern provider-executed tools`. Verify with `task tools:test`,
`task lint`, `task test`, `task spec` (`missing_in_scope=0`), and
`openspec validate m1-provider-tool-governance --strict --no-interactive`.

## 1. Classification and registration

- [ ] 1.1 Register a provider-executed tool through the ordinary registry as a spec with `Executor: ByProvider`, and read effect, `Trust`, scopes and `Capabilities.Exfil` from the spec, never from a provider block; verify `go test -short -timeout 2m ./core/... -run 'TestProviderToolRegistered'` covers that `Tool.Call` is never invoked for a `ByProvider` spec and that `Explain` lists it with `Executor: ByProvider` (`tools.provider-tool-registered`).
- [ ] 1.2 Ship `std/tool/provider` with `WebSearch(opts...)`, `CodeExec()` and `HostedMCP(url, allowed ...string)`, all `Untrusted`, effect `ReadOnly` for search and `SideEffect` for the other two, `Exfil: true` for search and hosted MCP, each with its own `ToolPolicy` raising `MaxEffect`; verify `go test -short -timeout 2m ./std/tool/provider/...`.
- [ ] 1.3 Keep and exercise the `Build` enable path: a `ByProvider` spec absent from `Caps.ProviderTools` fails `Build` with `ErrProviderToolUnsupported` (`core/build.go:244-251`) and increments `gohan.tool.provider_unsupported`; verify `go test -short -timeout 2m ./core/... -run 'TestProviderToolUnsupported'`.

## 2. Gate before the provider call

- [ ] 2.1 Add the pre-call gate invocation on the registered spec with empty arguments, before request assembly, reusing the existing gate and its sealed order (`core/permission/gate.go:166-195`); verify `go test -short -timeout 2m ./core/... -run 'TestProviderToolGate'`.
- [ ] 2.2 Translate the verdict into the declaration: `Deny` omits the tool, `Ask` sets the provider approval flag when `Caps.ProviderTools[name].Approval` is true and otherwise omits it, `Allow` declares it; verify `go test -short -timeout 2m ./core/... -run 'TestProviderToolGate'` asserts the assembled request carries no search tool when denied (`tools.provider-tool-gate-before-call`).

## 3. Approval as suspension

- [ ] 3.1 Map a provider approval request to a `HumanApproval` suspension with an `ApprovalRequest` built from the pending call, carrying `ArgOrigins` from the pre-call taint hook when present (`core/permission/approval.go:71`, `:112`); verify `go test -short -timeout 2m ./core/... -run 'TestProviderApproval'`.
- [ ] 3.2 Replay the model call with the approval item under the original `CallKey` on `Resume(Approve())`; verify `go test -short -timeout 2m ./core/... -run 'TestProviderApproval'` covers the suspension naming the pending call and the resumed replay (`tools.provider-approval-as-suspension`).

## 4. Provenance and provider result conversion

- [ ] 4.1 Convert the provider call block to a `ToolUse` and its result block to a `ToolResult` whose blocks carry `OriginProvider{Name}` of the registered spec; keep opaque payloads as `Raw` and round-trip them; verify `go test -short -timeout 2m ./core/... -run 'TestProviderResultOrigin'` (`tools.provider-result-origin`).
- [ ] 4.2 Assert a provider tool block is never stored as `Raw` and that `gohan.block.dropped{kind=raw}` does not fire for it; verify `go test -short -timeout 2m ./core/... -run 'TestProviderResultOrigin'`.

## 5. Post-hoc taint

- [ ] 5.1 Run the `taint` rule-1 computation over the arguments the provider reported, when the response arrives and before any result block is appended; fail the run `Failed(Permanent)` on a tainted input into an `Exfil` provider tool and increment `gohan.taint.post_hoc{tool}`; verify `go test -short -timeout 2m ./core/... -run 'TestProviderToolPostHoc'` (`taint.provider-tool-post-hoc`).
- [ ] 5.2 Count a provider tool with `Exfil` as both the exfiltration and the untrusted-content legs of the `Build` trifecta check; verify `go test -short -timeout 2m ./core/... -run 'TestProviderToolTrifecta'`.

## 6. Usage accounting

- [ ] 6.1 Fill `Usage.ProviderToolCalls[name]` from the provider calls observed in a response, on the shared conversion path both adapters use; verify `go test -short -timeout 2m ./core/... -run 'TestProviderToolUsage'` covers `ProviderToolCalls["web_search"] == 2` (`model.provider-tool-usage`).
- [ ] 6.2 Assert the run cost includes `count × Pricing.ProviderCall[name]` through the existing `charge` path (`core/chains/limits.go:142-144`) and that each provider call counts toward `MaxToolCalls` and is journaled `Completed`; verify `go test -short -timeout 2m ./core/... -run 'TestProviderToolUsage'`.

## 7. Scenario behavior and package gates

- [ ] 7.1 Add public-path subtests named exactly `tools.provider-tool-registered`, `tools.provider-tool-gate-before-call`, `tools.provider-approval-as-suspension`, `tools.provider-result-origin`, `model.provider-tool-usage` and `taint.provider-tool-post-hoc`; verify `go test -short -timeout 2m ./... -run 'Test.*Provider'`.
- [ ] 7.2 Run the repository wrappers and resolve failures; verify `task lint` and `task test`.
- [ ] 7.3 Validate the change; verify `openspec validate m1-provider-tool-governance --strict --no-interactive`.
  - The package validates and its six owned IDs bind to public-path tests; the milestone gate `task spec:gate GATE=M1` stays open until the adapter lanes (`m1-openai-provider`, `m1-anthropic-provider`) run the shared suite.

## Workflow follow-up

- Adapter-side conversion, wire encoding and the `Usage.ProviderToolCalls` fill at the real `Send` site are owned by `m1-openai-provider` and `m1-anthropic-provider`, which run the shared `conformance.Model` suite from `m1-provider-conformance-contract`. This change lands the governed seams those adapters call.
