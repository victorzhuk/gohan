# Tasks

## 1. Deterministic taint computation

- [ ] 1.1 Add `std/taint` policy defaults, `New`, and the raw-window context accessors; run `task lint`, `task test`, then `go test -short -timeout 2m ./... -run 'TestTaintPolicy'` (`docs/adr/0155-m1-taint-and-egress-contract.md:19-21`; `core/types/taint.go:37-40`).
- [ ] 1.2 Implement recursive string argument matching against bounded raw session blocks, preserve origins, exclude user/system and pre-user-turn blocks, and run `task lint`, `task test`, then `go test -short -timeout 2m ./... -run 'TestTaintMatcher'` (`openspec/specs/taint/spec.md:46`; `openspec/changes/m1/design.md:230,236`).
- [ ] 1.3 Verify truncation increments `gohan.taint.window_truncated` and projection or compaction does not change matcher inputs; run `task lint`, `task test`, then `go test -short -timeout 2m ./... -run 'TestTaintWindow'` (`openspec/specs/taint/spec.md:46,55`; `openspec/changes/m1/design.md:236`).

## 2. Native registration and permission enforcement

- [ ] 2.1 Add `NativeSpec.TaintPolicy`, resolve it in `Build`, and pass it to the gate through `permission.WithTaintHook`; run `task lint`, `task test`, then `go test -short -timeout 2m ./... -run 'TestNativeTaintHook'` (`core/native_options.go:16-38`; `core/permission/gate.go:79-84`; `docs/adr/0155-m1-taint-and-egress-contract.md:23`).
- [ ] 2.2 Verify public Send/Resume denial for a tainted Exfil call, including permanent `TaintDenied`, no tool effect, and `gohan.taint.denied`; run `task lint`, `task test`, then `go test -short -timeout 2m ./... -run 'TestTaintExfilDenied'` (`openspec/specs/taint/spec.md:84-87`).
- [ ] 2.3 Verify a tainted SideEffect call suspends with origins despite an existing grant, while a tainted ReadOnly call proceeds; run `task lint`, `task test`, then `go test -short -timeout 2m ./... -run 'TestTaintPermission'` (`openspec/specs/taint/spec.md:89-102`).
- [ ] 2.4 Add `permission.WithLiveDeny` and consult it after the missing-scope check and before the taint hook; then verify a bound live deny beats a taint `Ask`, returns a denial and requests no approval; run `task lint`, `task test`, then `go test -short -timeout 2m ./... -run 'TestTaintGateOrder'` (`openspec/specs/taint/spec.md:104-107`; `openspec/specs/flags/spec.md:42`; `openspec/changes/m1/design.md:235`).

## 3. Build trifecta enforcement

- [ ] 3.1 Compute Exfil, PrivateRead, and untrusted legs per resolved flow; refuse a trifecta without policy using `ErrTaintTrifecta{Flow, Exfil, PrivateRead, Untrusted}`; run `task lint`, `task test`, then `go test -short -timeout 2m ./... -run 'TestTaintTrifecta'` (`docs/adr/0155-m1-taint-and-egress-contract.md:25`; `openspec/specs/taint/spec.md:111-114`).
- [ ] 3.2 Verify an explicit default policy permits the trifecta and records `gohan.build.trifecta{flow}=1`; verify a flow without Exfil builds without policy; run `task lint`, `task test`, then `go test -short -timeout 2m ./... -run 'TestTaintBuild'` (`openspec/specs/taint/spec.md:116-124`).

## 4. Egress integration and package acceptance

- [ ] 4.1 After `m1-egress-policy` lands, consume its Build-resolved `ToolSpec.Capabilities.Exfil` value without adding a taint-local classifier; verify a registered egress tool receives taint enforcement. Run `task lint`, `task test`, then `go test -short -timeout 2m ./... -run 'TestTaintEgressIntegration'` (`openspec/changes/m1/design.md:234`; `docs/adr/0155-m1-taint-and-egress-contract.md:27`).
- [ ] 4.2 Run the assigned scenario tests, then run `task lint`, `task test`, and `task spec:gate GATE=M1`; verify all 11 assigned scenario IDs pass. Validate with `openspec validate m1-taint-enforcement --strict --no-interactive` (`openspec/changes/m1/design.md:281-297,332-339`).
