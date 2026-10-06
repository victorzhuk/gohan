# Spec Delta

## MODIFIED Requirements

### Requirement: Enforcement

The permission gate MUST apply hard and live denies before the taint hook, then apply session grants and the decider (`openspec/changes/m1/design.md:239`, D6). A taint denial MUST prevent tool execution. A taint Ask MUST preserve matched argument origins for approval. For a provider-executed tool the gate runs on the spec before the model request (`tools` *Provider-executed tools* rule 2) and cannot see the arguments, because the model fills them inside the provider call; the implementation MUST apply the taint computation to those arguments post hoc, when the provider response arrives, and MUST apply it before any provider result block is appended or used. A tainted input into an `Exfil` provider tool MUST fail the run `Permanent` and MUST increment `gohan.taint.post_hoc{tool}`. A provider tool with `Exfil` MUST count as both the exfiltration leg and the untrusted-content leg of the build check.

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
