# Skills

Capability: `skills` · Spec v1.1 (ADR-0094) · Source of truth for this capability.

> **Contract tiers.** Code blocks in *Contract* are **normative** (ports, interfaces, error classes, stored shapes, event types, ordering rules) unless a block is marked `(illustrative)`, in which case names may change during M0 without a spec change. Prose rules in *Contract* are normative. Requirements are normative and each scenario ID is referenced by a test.

## Purpose

Loads procedural knowledge in the SKILL.md layout with progressive disclosure, under the same trust, guard, manifest and sandbox controls as tools. Out of scope: authoring or vetting pipelines (evals/CI), prompt versioning (`telemetry` `PromptSource`).


## Contract

```go
type SkillMeta struct {
	Name          string
	Description   string
	RequiredTools []string
	Trust         Trust
	Hash          string
}

type Skill struct {
	SkillMeta
	Body      string
	Resources []string
}

type SkillSource interface {
	List(ctx context.Context) ([]SkillMeta, error)
	Load(ctx context.Context, name string) (Skill, error)
	Resource(ctx context.Context, name, path string) (io.ReadCloser, error)
}
```

`SkillSource` is defined in `std/skills` (core has no consumer of it; core budget rule). `std/skills` ships `New(src SkillSource, opts ...Option) (gohan.ContextProvider, gohan.ToolSet)` and a filesystem `SkillSource` reading `<dir>/<name>/SKILL.md` (YAML frontmatter `name`, `description`, optional `required_tools`; body; `scripts/`, `references/`, `assets/`). Rules:

1. **L1 — catalog.** The provider renders every `SkillMeta` (name, description) sorted by name into `SlotStatic`; the catalog is byte-stable for identical sources and its hash is part of the pinned manifest with the tools. Description text goes through the tool description guard; a rejected description excludes the skill and increments `gohan.skill.rejected{reason="description"}`.
2. **L2 — body.** `load_skill(name)` is a `ReadOnly` meta-tool. The body returns as a tool result with `Origin{Kind: OriginTool, Name: "skill/<name>"}`, passes `StageToolResult`, and is re-fetchable: `ClearToolResults` may clear it. Loading never widens the tool set.
3. **L3 — resources.** `read_skill_resource(name, path)` is `ReadOnly`; content above `MaxOutput` goes to `OutputStore`. Scripts under `scripts/` never run on the host: they execute only through the `sandbox` capability, only for `Trusted` skills, and the sandbox tool set's own effect applies.
4. **Trust.** Skills from the service's own repository default to `Trusted`; any other source (`PromptSource`, remote registry, user upload) defaults to `Untrusted`: instructions-only, no scripts, descriptions guarded. Raising trust is an explicit wiring option.
5. **Required tools.** `RequiredTools` must be a subset of registered tools or `Build` fails naming the skill and the missing tool. `ToolFilter` still applies per turn; a skill cannot bypass it.
6. **Manifest.** Each skill's `Hash` (frontmatter + body + resource digests) is pinned; a source that yields a different hash for a pinned name is disabled and `gohan.tool.manifest_drift{source="skill"}` increments.
7. **Prompt hygiene.** Skill bodies are not `PromptSet` strings: `Explain` lists loaded skills by name and hash for a run, and `AuditLog` records each load.

Adapters: `adapter/langfuse` offers a `PromptSource`-backed `SkillSource`.

Metrics: `gohan.skill.loaded{name}`, `gohan.skill.rejected{reason}`, `gohan.skill.resource_reads`.


## Requirements

### Requirement: Progressive disclosure

#### Scenario: catalog in prefix
ID: `skills.catalog-prefix-stable`
- WHEN 30 skills are registered and the same flow runs twice
- THEN the static prefix contains 30 name/description lines in sorted order and is byte-identical across runs

#### Scenario: body on demand
ID: `skills.body-on-demand`
- WHEN the model calls `load_skill("refund-dispute")`
- THEN the body enters history as a tool result with `Origin.Name == "skill/refund-dispute"` and no other skill body is in context

#### Scenario: body clearable
ID: `skills.body-clearable`
- WHEN a loaded skill body is older than the last `Keep` tool uses under `ClearToolResults`
- THEN it is cleared like any re-fetchable result

#### Scenario: resource via store
ID: `skills.resource-to-store`
- WHEN `read_skill_resource` returns content above `MaxOutput`
- THEN the result is a head excerpt plus `Ref`

### Requirement: Trust and containment

#### Scenario: untrusted is instructions-only
ID: `skills.untrusted-no-scripts`
- WHEN an `Untrusted` skill bundles `scripts/run.py` and the model asks to run it
- THEN the request is denied before any sandbox call and `gohan.skill.rejected{reason="script"}` increments

#### Scenario: scripts only in sandbox
ID: `skills.script-sandboxed`
- WHEN a `Trusted` skill script runs
- THEN it runs through the `sandbox` tool set, never `tool/exec`

#### Scenario: injected description excluded
ID: `skills.description-guarded`
- WHEN a skill description contains an instruction the description guard rejects
- THEN the skill is absent from the catalog and cannot be loaded

#### Scenario: body guarded
ID: `skills.body-guarded`
- WHEN a loaded body contains content the `StageToolResult` guard rejects
- THEN the model receives a guard block, not the body

### Requirement: Required tools and manifest

#### Scenario: missing required tool
ID: `skills.required-tool-missing`
- WHEN a skill declares `required_tools: [create_refund]` and no such tool is registered
- THEN `Build` fails naming the skill and the tool

#### Scenario: loading never widens
ID: `skills.no-widening`
- WHEN a skill names a tool that `ToolFilter` hides this turn
- THEN the tool stays hidden after `load_skill`

#### Scenario: skill drift
ID: `skills.manifest-drift`
- WHEN the pinned hash for `refund-dispute` differs from what the source now yields
- THEN the skill is disabled and `gohan.tool.manifest_drift{source="skill"}` increments

#### Scenario: explain lists skills
ID: `skills.explain-lists`
- WHEN `Explain` runs for a run that loaded two skills
- THEN both appear with name and hash
