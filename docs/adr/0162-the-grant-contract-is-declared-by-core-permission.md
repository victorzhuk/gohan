# 0162. The grant contract is declared by `core/permission`

Status: accepted

## Context

`ApproveScope(ttl)` was the only way a caller asked for a session grant, and the
spec left the grant's own lifetime in two places at once: the `permission`
Contract prose named a `MaxGrantTTL` default of 4 h, while the shipped store
`std/permission.MemoryGrantStore` caps at 24 h and the driver stored a grant
derived from the recorded request expiry rather than the approval instant.
`permission.Grant` and `permission.GrantStore` also had no normative declaration
in the `permission` Contract, even though `core` consults a store on every
governed call and `Build` binds one through `ApprovalGrants`.

The remediation keeps the driver from choosing a grant's policy: the caller's
`ttl` is a request, the bound store's policy is the cap, and the expiry derives
from the actual approval instant.

## Decision

`permission.Grant` and `permission.GrantStore` are declared once, in
`core/permission`, and the `permission` Contract carries that declaration
verbatim. There is no `gohan.Grant` or `gohan.GrantStore` alias, and no
compatibility shim: the aliases would let a caller keep naming a grant contract
the driver no longer owns, and the two spellings would then drift. Callers bind
the store with `gohan.ApprovalGrants(permission.GrantStore)` and receive the
`gohan.ApproveScope(ttl) ResumeInput` constructor. This is a deliberate breaking
API change inside `v0`; it is listed under *Breaking* in `CHANGELOG.md` by the
owning change, not by this record.

A scoped approval is refused before the token is consumed when no grant store is
bound, so a resume never executes an approved call whose authority was never
persisted. One grant is stored per approval-controlled call that reached its
quorum, keyed by session, tool, fingerprint and the verifying approving subject
— never the session originator. The grant and its `granted_by_scope` audit
record land before the approved call executes, and a store or audit failure
fails the resume closed. A stored grant's lifetime is capped by the bound
store's policy; the driver invents no default TTL, ratio or estimator.

`WithCompactionGuard`-style construction does not apply here: `ApprovalGrants`
is a binding option on `Build`, not a guard, and a build without one still runs
every non-scoped approval path unchanged.