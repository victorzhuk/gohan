# routing Specification

## Purpose

Defines the endpoint-ordering policies the router exposes and the invariants they keep, so a caller can reason about which endpoint is tried first and what class preference may never change.

## Requirements

### Requirement: Endpoint ordering policies

The router MUST offer two endpoint-ordering policies. `Static` MUST keep the endpoints in declaration order and MUST be the policy in effect when the caller selects none. `ByLatencyClass` MUST be selected by naming one latency class, and MUST place every endpoint whose model profile carries that class before every other endpoint. Both groups MUST keep declaration order.

#### Scenario: static order
ID: `routing.static-order`
- WHEN a caller builds a router over endpoints without selecting a latency class
- THEN the candidate and eligible views return the endpoints in declaration order

#### Scenario: latency class order
ID: `routing.latency-class-order`
- WHEN a caller selects a latency class and the endpoints span several classes
- THEN every endpoint of the selected class precedes every other endpoint, and both groups keep declaration order

### Requirement: Class selection invariants

Selecting a class MUST NOT remove the other endpoints: they remain fallback candidates. It MUST NOT reorder endpoints inside either group, and it MUST NOT modify the caller's endpoint slice. When no endpoint carries the selected class, the router MUST keep the complete declaration order.

#### Scenario: latency class with no match
ID: `routing.latency-class-no-match`
- WHEN a caller selects a latency class that no endpoint carries
- THEN the router keeps the complete declaration order

### Requirement: Ordering under failures

Breaker eligibility MUST keep the policy order: an endpoint whose breaker is open is omitted from the candidate views without changing the order of the rest. Reading the eligible view MUST NOT consume a half-open probe. A fallback MUST NOT select another endpoint after the first emitted chunk, and MUST serve the remaining endpoints in policy order.

#### Scenario: latency class with breaker order
ID: `routing.latency-class-breaker-order`
- WHEN an endpoint of the selected class has an open breaker
- THEN the candidate and eligible views omit it without changing the order of the rest, and the eligible view leaves a half-open probe unconsumed

#### Scenario: latency class fallback
ID: `routing.latency-class-fallback`
- WHEN the endpoints of the selected class return a rate-limited error before any chunk is emitted
- THEN the fallback serves an endpoint of another class, in the policy order
