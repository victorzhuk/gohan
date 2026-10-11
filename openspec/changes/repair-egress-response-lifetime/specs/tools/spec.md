# Spec Delta

## ADDED Requirements

### Requirement: Egress response lifetime

The egress client MUST bound the whole response, not the request only. Its time limit MUST stay in force until the body ends. The client MUST release the per-request transport and cancel its time limit exactly once. The release MUST NOT depend on the caller closing the body. A release MUST be safe to run twice. On a terminal read error the client MUST return the read bytes and the original error unchanged. Cleanup MUST NOT change the checked-address dial, proxy validation, or the redirect rules.

#### Scenario: body read bounded by the policy time limit
ID: `tools.egress-body-read-bounded` (proposed)
- WHEN an allowed host sends response headers and then stalls, and the caller reads the body
- THEN the read stops at the policy time limit with a permanent failure rather than hanging, the transport is released before any caller close, and the returned bytes and error are the ones the underlying body produced

#### Scenario: early request error releases the transport
ID: `tools.egress-early-error-cleanup` (proposed)
- WHEN a request is refused before a response exists (denied address, disallowed scheme, or unsupported proxy)
- THEN no idle connection survives the refusal

#### Scenario: size-limit refusal releases the transport
ID: `tools.egress-oversize-cleanup` (proposed)
- WHEN a response exceeds `MaxBytes`
- THEN the body is closed, the size-limit refusal is returned, and no idle connection survives it

#### Scenario: release is idempotent
ID: `tools.egress-close-idempotent` (proposed)
- WHEN the caller closes the body twice, or closes it after reaching end of file
- THEN the transport is released exactly once and the client does not panic

#### Scenario: cleanup preserves the request checks
ID: `tools.egress-cleanup-preserves-checks` (proposed)
- WHEN a response ends normally and the transport is released
- THEN the dial still uses the checked address, and the rebinding, proxy and redirect rules are unchanged

### Requirement: Egress connection ownership

The egress client MUST NOT reuse or pool a connection before a measurement shows that reuse keeps the checked-address dial, the proxy validation, and the redirect rules intact. Until then, each request MUST own its transport. The client MUST close that transport's idle connections when the response ends.

#### Scenario: per-request transport is released
ID: `tools.egress-per-request-transport-released` (proposed)
- WHEN two requests run in sequence over the same client
- THEN each request owns its transport and no idle connection survives the first response
