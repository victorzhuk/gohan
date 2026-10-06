# Proposal: m1-schema-read-paths

## Why

`core/stores/schema.go` declares schema versions and pure upcasters, but no store read path applies the registry. `testkit/storetest.Schemas` declares fixture replay, but its fixture set and public-reader binding do not establish replay through current readers (`openspec/changes/m1/design.md:169-170`; `core/stores/schema.go`; `testkit/storetest/schemas.go`).

## What Changes

- Apply registered upcasters before decoding versioned serialized records, without rewriting stored bytes.
- Record a fixture for every stored shape and released schema version, and replay fixtures through current readers.
- Emit `gohan.schema.upcast{from,to}` when a read applies an upcast; return `types.ErrSchemaTooOld` when a chain is incomplete.
- Add deterministic store-boundary and public read-path tests for schema v1→v3 behavior and byte preservation.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `lifecycle`: complete read-time schema evolution and released-fixture replay (`openspec/specs/lifecycle/spec.md:38-46`).

## Impact

Implementation affects versioned record declarations and decode/read paths in `core/stores`, schema conformance in `testkit/storetest`, and the store test bindings. It does not add a persistence adapter or change a serialized store in-place. Root module remains pre-v1 through M2; no release tags are part of this change (`openspec/changes/m1/design.md:27`).