# tools delta

## ADDED Requirements

### Requirement: Egress literal allow exception

An `Allow` entry exempts only an exact literal destination address. A hostname, glob or CIDR entry MUST NOT exempt a resolved private, loopback, link-local or shared-address (`100.64.0.0/10`) destination, and refusal MUST happen before dialing.

#### Scenario: hostname glob cannot exempt a private resolution
ID: `tools.egress-glob-no-exempt`
- WHEN `Allow` carries a hostname glob and DNS resolves the destination to a private address
- THEN the connection is refused before dialing

#### Scenario: exact CGNAT literal remains reachable
ID: `tools.egress-literal-cgnat-allowed`
- WHEN `Allow` lists the exact literal `100.64.9.9` under `DenyPrivate`
- THEN `100.64.9.9` is reachable and other literals inside `100.64.0.0/10` stay denied