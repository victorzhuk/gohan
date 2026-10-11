# Spec Delta

This delta ADDS one requirement to the `build` capability. It does not restate or replace the `Build validation` requirement or its scenarios, which stay with their current package; the new ids below are unused by another package.

## ADDED Requirements

### Requirement: Foreign resolution preserves native registrations

Resolving foreign definitions MUST NOT remove or replace the resolved configuration of any native flow. After `Build` succeeds, `Stack` MUST return the resolved configuration of every registered native flow and every registered foreign flow, each with its resolved model, tools, chains and policies. A foreign definition registered under a name already registered native MUST fail `Build` naming the flow.

#### Scenario: native configuration survives foreign resolution
ID: `build.foreign-preserves-native`
- WHEN `Build` registers native flow `chat` and foreign flow `agent` and succeeds
- THEN the resolved configuration of both `chat` and `agent` is returned for their runs

#### Scenario: duplicate name fails closed
ID: `build.foreign-native-name-clash`
- WHEN a foreign definition is registered under a name already registered native
- THEN `Build` fails naming the flow and no run is built