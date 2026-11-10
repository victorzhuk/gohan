## ADDED Requirements

### Requirement: Durable local acceptance survives restart
Durable append MUST acknowledge only synced records. Restart MUST replay these records with their original IDs until remote acknowledgement.

#### Scenario: Crash after local acceptance
ID: `telemetry.wal-durable-restart`
- **WHEN** the process is killed after synced acknowledgement and before remote delivery
- **THEN** restart exports every locally acknowledged pending record with unchanged IDs

### Requirement: WAL corruption is explicit
Recovery MUST reject interior corruption and unknown versions. Recovery MUST preserve preceding records when repairing an incomplete final record.

#### Scenario: Corrupt spool recovery
ID: `telemetry.wal-corruption`
- **WHEN** a spool has an incomplete tail, interior checksum failure or unsupported version
- **THEN** only the incomplete tail is repaired and other corruption fails with actionable diagnostics

### Requirement: Destinations retain independent progress
Each destination MUST persist its acknowledgement progress. Ambiguous delivery MUST retain unresolved records and permit duplicate replay.

#### Scenario: Restart with divergent destinations
ID: `telemetry.wal-destination-cursors`
- **WHEN** one destination acknowledges and another fails before a restart
- **THEN** acknowledged progress survives and unresolved records replay with stable IDs

### Requirement: Spool limits preserve durable records
The spool MUST reject concurrent writers and appends beyond capacity. It MUST NOT automatically evict unacknowledged records.

#### Scenario: Ownership and disk failure
ID: `telemetry.wal-capacity-ownership`
- **WHEN** a second writer opens the spool and its owner encounters quota or disk exhaustion
- **THEN** the second writer fails and existing durable records remain recoverable

### Requirement: Persisted content obeys erasure
Subject erasure MUST remove matching captured content before reporting completion and MUST prevent its subsequent replay.

#### Scenario: Erasure survives interruption
ID: `telemetry.wal-content-erasure`
- **WHEN** erasure is interrupted during segment replacement and retried after restart
- **THEN** recovery completes erasure without replaying removed subject content
