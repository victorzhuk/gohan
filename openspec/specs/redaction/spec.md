# Redaction, erasure and residency

Capability: `redaction` · Spec v1.3 (ADR-0132) · Source of truth for this capability.

> **Contract tiers.** Code blocks in *Contract* are **normative** (ports, interfaces, error classes, stored shapes, event types, ordering rules) unless a block is marked `(illustrative)`, in which case names may change during M0 without a spec change. Prose rules in *Contract* are normative. Requirements are normative and each scenario ID is referenced by a test.

## Purpose

See `docs/overview.md` for how `redaction` fits the architecture. Out of scope for this capability is anything owned by another capability spec; cross-references are by capability name.


## Contract

### 6.8d Redaction, erasure and residency

```go
// (illustrative) names may change during M0; see contract tiers
type RedactionPolicy struct {
	Detectors []Detector
	Mode      TokenMode
	Preserve  []EntityKind
}

type TokenMode int

const (
	HMACToken TokenMode = iota
	FormatPreserving
	Vaulted
)

type EntityKind string

const (
	EntityPerson EntityKind = "person"
	EntityEmail  EntityKind = "email"
	EntityPhone  EntityKind = "phone"
	EntityCard   EntityKind = "card"
	EntityIBAN   EntityKind = "iban"
	EntityCustom EntityKind = "custom"
)

type Entity struct {
	Kind  EntityKind
	Value string
}

type Detector interface {
	Detect(ctx context.Context, blocks []Block) ([]Span, error)
}

type Span struct {
	Block  int
	Start  int
	End    int
	Kind   EntityKind
}

type EraseReport struct {
	Sessions int
	Entries  int
	Outputs  int
	Held     []string
}

type RedactionMap struct {
	Tenant  string
	Session string
	Entries map[string]Entity
}

type Redactor interface {
	Redact(ctx context.Context, blocks []Block, p RedactionPolicy) ([]Block, RedactionMap, error)
	Rehydrate(ctx context.Context, blocks []Block, m RedactionMap) ([]Block, error)
}

type KeySource interface {
	Key(ctx context.Context, tenant string) ([]byte, error)
}

func (s *Stack) EraseSubject(ctx context.Context, tenant, subject string) (EraseReport, error)
```

Placement is fixed in the chains and the harness, not chosen by callers:

1. **Before the prompt** (assembler): user input, tool results and provider context are redacted under the flow's policy; the model sees stable pseudonyms (same entity → same token within a session, tenant-keyed via `KeySource`), so it can still reason about "the customer" and "the booking".
2. **Before every store write**: `SessionLog`, `EventLog`, `OutputStore`, `Checkpoints` and `notes` receive redacted blocks; the `RedactionMap` is stored separately, tenant-keyed, with its own retention.
3. **Before telemetry content capture** (when enabled).
4. **Rehydration at egress** inside the output stage: the `Windowed` output guard already holds back a token window; rehydration runs on each released window so a pseudonym is never split across chunks. Tokens the model invents that are not in the map are dropped and counted (`gohan.redact.unknown_token`); never leaked.

Detection is layered: `std/redact` ships rules with checksums (cards, IBANs, phone, email, national ID formats); NER and LLM detectors plug in as `Decider`s. Erasure: `EraseSubject` deletes the subject's sessions, events, outputs, checkpoints, grants and redaction maps across all stores, records `AuditErasure`, and returns a report; audit records remain (checksums only). Residency: `ModelProfile.Region`, `Region` on every store implementation, `Tenant.Residency` in `RunInfo`; `Build` fails when any reachable router target or configured store for a residency-bound tenant is outside the region; runtime routing never overrides it. An external PII proxy or vendor service is an implementation of `Redactor`, not a substitute for the placement rules.


## Requirements

### Requirement: Redaction, erasure, residency

#### Scenario: erase reports held sessions
ID: `redaction.erase-reports-held`
- WHEN `EraseSubject` runs for a subject with three sessions, one under legal hold
- THEN the two others are erased, the held one is untouched and listed in `EraseReport.Held`, and the `AuditErasure` record names it as held

#### Scenario: pseudonym stable within session
ID: `redaction.pseudonym-stable-within-session`
- WHEN "Anna Petrova" appears in turn 1 and turn 4
- THEN the model receives the same token both times and stored history contains only the token

#### Scenario: stores never see raw entities
ID: `redaction.stores-never-see-raw-entities`
- WHEN a run completes
- THEN `SessionLog`, `EventLog`, `OutputStore` and checkpoints contain no detected entity; the `RedactionMap` is stored separately, tenant-keyed

#### Scenario: streaming rehydration
ID: `redaction.streaming-rehydration`
- WHEN the model's answer contains a pseudonym that spans two windows
- THEN the client receives the real value intact and never a partial token

#### Scenario: hallucinated token dropped
ID: `redaction.hallucinated-token-dropped`
- WHEN the model emits a token not present in the map
- THEN it is removed from the output and `gohan.redact.unknown_token` increments

#### Scenario: erasure
ID: `redaction.erasure`
- WHEN `EraseSubject(tenant, subject)` runs
- THEN no session, event, output, checkpoint, grant or redaction map for the subject remains; audit records remain with checksums; `AuditErasure` is appended

#### Scenario: erasure removes blobs
ID: `redaction.erase-removes-blobs`
- WHEN `EraseSubject(tenant, subject)` runs for a subject whose sessions hold blob refs
- THEN `OutputStore.Get` fails for every ref owned only by those sessions and shared refs owned by other subjects remain

#### Scenario: erasure removes memory
ID: `redaction.erase-removes-memory`
- WHEN `EraseSubject(tenant, subject)` runs for a subject with memory entries
- THEN `Stack.Memory` returns nothing for the owner and the entries are absent from the store

#### Scenario: residency enforced at build
ID: `redaction.residency-enforced-at-build`
- WHEN a residency-bound tenant's flow can route to a profile in another region
- THEN `Build` fails naming tenant, profile and regions
