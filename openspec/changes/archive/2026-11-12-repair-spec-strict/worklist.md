# Strict validation worklist

## Baseline

OpenSpec 1.17.0 against the main specs at `51b1a66` reports **2 passed, 34 failed, 36 items**. Its output contains 67 ERROR lines and 37 WARNING lines.

The warnings comprise 30 over-long bodies, six missing-keyword guidance warnings, and one scenario-format warning. The original planning count of 37 over-long bodies was incorrect. The measured count is **30 bodies across 15 capabilities**. The validator checks `req.text.length > 500`.

Indices below are zero-based positions in the original capability's requirement list. They are not indices in the repaired specs.

## Missing MUST or SHALL

These 64 empty-body requirements produce ERROR findings. The repaired bodies must state existing contracts without changing their meaning.

| Capability | Index and requirement |
|---|---|
| agui | 0 Core additions; 1 Mapping |
| assembly | 0 Context assembly |
| build | 0 Build validation |
| decider | 0 Decider contract |
| engines | 0 Composition, dedup, states and flags |
| guards | 0 Guards; 1 Provenance and context guards |
| interop | 0 AwaitingInput; 1 MCP client; 2 HTTP API; 3 MCP server |
| languages | 0 Definitions and languages |
| limits | 0 Defaults and cost; 1 Run limits |
| messages | 2 Block model and fidelity |
| model | 2 Provider keys; 3 Token budget; 4 Timeouts and mid-stream failure; 5 Failover and version pinning; 6 Tool argument deltas; 7 Iterator contract |
| performance | 0, 1, 2 Performance budgets |
| permission | 0 Approval scope and requests |
| recovery | 0 Crash recovery |
| redaction | 0 Redaction, erasure, residency |
| release | 0 Shadow mode; 1 Release identity; 2 Evals |
| skills | 0 Progressive disclosure; 1 Trust and containment; 2 Required tools and manifest |
| stores | 1 Two clocks; 3 Intent-level idempotency and uncertainty; 8 Forking; 9 Feedback store; 11 Session deletion; 12 Audit trail |
| streams | 1 Error-tuple protocol; 2 Previews |
| structured-output | 0 Structured output; 1 Partial results; 2 Structured output hardening |
| subflows | 0 Isolation and typing; 1 Effect derivation; 2 Nested suspension; 3 Limits and failure; 4 Propagation |
| suspension | 0 Suspension and resume |
| taint | 0 Taint computation; 2 Trifecta at build |
| telemetry | 0 Telemetry; 1 Attributes, labels and logging |
| tools | 4 Tool contract; 5 Tool author contract; 6 Tool spec pinning; 7 Dynamic tools and inspection; 8 Deferred tools; 9 tool/exec; 11 Verification |
| working-state | 0 Subject memory; 1 Notes, outputs and shared state |

Six non-empty bodies produce missing-keyword WARNING findings:

| Capability | Index | Requirement |
|---|---|---|
| messages | 0 | Error catalog |
| stores | 5 | Notice outbox |
| stores | 6 | Run mailbox |
| stores | 10 | Retention and legal hold |
| tools | 0 | Egress |
| tools | 3 | Names and identity |

## Empty requirement and duplicate headings

- `tools[9]`, `tool/exec`, has no scenario. It produces one scenario ERROR and one scenario-format WARNING in addition to its missing-keyword ERROR.
- `performance` declares `Performance budgets` three times, at original lines 32, 39 and 46. The latter two headings produce duplicate-name ERROR findings. Consolidate the headings while retaining all three existing scenario blocks.

These account for the remaining three ERROR lines.

## Bodies above 500 characters

Lengths use the pinned parser's `req.text.length`. The parser stops at the first header and excludes blank lines, fenced blocks and metadata lines.

| Capability | Index | Requirement | Characters |
|---|---:|---|---:|
| chains | 1 | No hidden behavior | 1780 |
| context | 0 | Projections are views | 1395 |
| context | 2 | Compaction is persisted | 520 |
| flags | 0 | Frozen and live flags | 1646 |
| flow | 0 | Flow contract | 2136 |
| identity | 2 | Context values | 1106 |
| lifecycle | 0 | Retirement, schema evolution, cost | 1565 |
| messages | 0 | Error catalog | 5344 |
| messages | 1 | Blobs | 1011 |
| model | 0 | Provider adapter contract | 1957 |
| runtime | 0 | Run lifecycle ordering | 573 |
| runtime | 1 | Health | 707 |
| runtime | 2 | Graceful shutdown | 930 |
| runtime | 4 | Runtime conformance | 511 |
| runtime | 5 | Component mixing | 556 |
| sandbox | 0 | Policy and effect | 1163 |
| sandbox | 1 | Secrets | 761 |
| sandbox | 2 | Lifecycle and resume | 1432 |
| stores | 4 | Optional interfaces | 765 |
| stores | 5 | Notice outbox | 552 |
| stores | 6 | Run mailbox | 928 |
| stores | 10 | Retention and legal hold | 2116 |
| stores | 13 | Checkpoint versioning and quota pools | 934 |
| stores | 14 | Postgres contract | 1672 |
| streams | 0 | Run notices | 2266 |
| taint | 1 | Enforcement | 909 |
| telemetry | 2 | Telemetry conventions and prompts | 806 |
| tools | 0 | Egress | 1709 |
| tools | 3 | Names and identity | 1136 |
| tools | 10 | Provider-executed tools | 4014 |

Split at existing scenario boundaries. Move scenario-less prose verbatim into `Contract`. Keep the error catalogue table in place inside a fence so the existing index generator retains its source boundaries.

`cache` and `routing` have no baseline findings. Sibling change deltas and uncommitted appended requirements are outside this baseline inventory.
