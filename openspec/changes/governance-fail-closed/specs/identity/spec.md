# identity delta

## ADDED Requirements

### Requirement: Anonymous mode bypasses no authorization

`AllowAnonymous` MUST bypass only the principal-presence check; every store authorization, owner check and scope check MUST still run.

#### Scenario: anonymous bypasses no authorization
ID: `identity.anonymous-bypasses-no-authorization`
- WHEN an anonymous run attempts to read another owner's session
- THEN the store owner check refuses with `ErrSessionForbidden` and no session content is disclosed