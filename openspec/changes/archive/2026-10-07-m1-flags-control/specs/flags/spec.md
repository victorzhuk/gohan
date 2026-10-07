# Spec Delta

The `flags` main spec already states the two classes, the freshness rule and the frozen snapshot's placement as normative `Contract` prose (openspec/specs/flags/spec.md:41-44), and this delta carries the five scenario IDs and their WHEN/THEN lines unchanged. The requirement below supplies the normative body the main spec left to its scenario blocks; no scenario is reworded.

## MODIFIED Requirements

### Requirement: Frozen and live flags

The implementation MUST treat flags in two classes. Frozen flags MUST be evaluated once at run start with the session hash as the targeting key, MUST be written to `RunInfo.Flags`, the checkpoint `State` and the run-start audit record, and MUST be read from that snapshot for every evaluation inside the run, so a later provider change cannot alter a live run. The frozen carrier MUST be `runtime.FlagSnapshot` (`map[string]any`): `RunInfo.Flags` and the checkpoint `State.Flags` both hold that type, so the checkpoint shape is unchanged. `Replay` MUST assert the recorded snapshot and MUST return `ErrFlagDrift` before any model call when the snapshot cannot be honoured.

Live flags MUST be re-evaluated before every model call and every non-`ReadOnly` tool call, including on resume after approval. The tool-call half MUST be applied in the permission gate's live-control position, after the hard scope deny and before the taint hook, as an action that can allow, force `Ask` or deny; the model-call half MUST be applied by a model-chain step that runs before the call is issued. A live flag can only disable a tool, force `Ask` or block a flow and can never widen a hard block, a taint deny or a registered capability. An approval MUST NOT override a live deny.

A live evaluation MUST carry `EvaluatedAt` and `FromDefault`. When `now - EvaluatedAt > FreshnessLimit` or `FromDefault` is true, the next effect MUST suspend with `AwaitingControl`; the suspension MUST resume through the empty wake, and when no fresh state arrives within `MaxControlWait` the effect MUST be denied and the run MUST continue with a `Failed(Permanent)` tool result.

#### Scenario: frozen snapshot
ID: `flags.frozen-snapshot`
- WHEN a run starts with rollout flag `assist_v2 = "b"`
- THEN `RunInfo.Flags` and the checkpoint carry the value and every evaluation within the run returns it, even if the provider changes

#### Scenario: default is not fresh
ID: `flags.from-default-suspends`
- WHEN a live kill-switch evaluation returns `FromDefault: true` before a `SideEffect` tool call
- THEN the run suspends with `AwaitingControl`

#### Scenario: stale then denied
ID: `flags.stale-then-deny`
- WHEN a live evaluation is older than `FreshnessLimit` and no fresh state arrives within `MaxControlWait`
- THEN the effect is denied and the run continues with a `Failed(Permanent)` tool result

#### Scenario: flags never widen
ID: `flags.never-widen`
- WHEN a live flag would enable a tool that wiring did not register or raise an effect
- THEN nothing changes; flags can only disable, force `Ask` or block

#### Scenario: replay asserts snapshot
ID: `flags.replay-asserts-snapshot`
- WHEN `Replay` resumes a run whose frozen snapshot cannot be honoured by the current provider
- THEN `ErrFlagDrift` is returned before any model call
