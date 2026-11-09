## ADDED Requirements

### Requirement: Shared-address destinations follow private-range policy

`DenyPrivate` MUST reject resolved destinations in `100.64.0.0/10`, subject to the existing explicit literal allow exception.
Rejection MUST occur before dialing.

#### Scenario: An allowed hostname resolves into CGNAT
ID: `tools.cgnat-destination-denied`
- WHEN an allowed hostname resolves into the shared-address range under `DenyPrivate`
- THEN the request is refused before a connection is opened

### Requirement: Proxy routing preserves the validated destination

A supported proxy route MUST connect to a destination address already validated by the client policy.
Proxy DNS MUST NOT replace that address.
Original HTTP authority, TLS server name, redirect validation and response limits MUST remain correct.
Unsupported modes MUST fail before dispatch.

#### Scenario: Proxy DNS disagrees with client validation
ID: `tools.proxy-destination-pinned`
- WHEN client validation selects an allowed address but proxy DNS would resolve the hostname to a denied address
- THEN the proxy request uses the validated address and cannot reach the denied peer

#### Scenario: HTTPS uses a pinned address with the original authority
ID: `tools.proxy-tls-authority-preserved`
- WHEN HTTPS is routed through a supported proxy to a validated address
- THEN CONNECT uses that address while certificate verification and the HTTP Host use the original authority
