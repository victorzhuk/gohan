# Tasks

Use the existing-service-strict public acceptance contract and limits in `design.md`.

## 0. Local pre-fix evidence

- [x] 0.1 Preserve existing egress baseline checks before local repairs. Record pre-fix behavior failures through public HTTP tool/egress boundaries for CGNAT and divergent proxy DNS. Include source revision, bounded command, scenario, expected behavior and failing assertion. Use controlled peers and allowed-traffic positive controls. Reject compilation, missing-command and infrastructure errors as reproductions. Supply this evidence to integration-owned parent 1.3.

## 1. Checked outbound destinations

- [x] 1.1 Reject resolved CGNAT peers under `DenyPrivate`. Preserve explicit literal exceptions. Verify `tools.cgnat-destination-denied` at both range boundaries and existing allow/private cases. Parent 7.4.
- [x] 1.2 Pin validated proxy destinations. Preserve HTTP/TLS authority, proxy auth, redirects, per-response deadlines and release behavior. Refuse unsupported modes before dispatch. Verify `tools.proxy-destination-pinned` and `tools.proxy-tls-authority-preserved` with controlled divergent DNS. Parent 7.5.
- [ ] 1.3 Integrate sandbox documentation evidence. Update existing sandbox/egress architecture docs and changelog. Verify explicit supersession of archived DNS-risk acceptance and documented helper cleanup without editing archived historical bytes. Parent 7.7.

## 2. Child acceptance

- [ ] 2.1 Run all assigned scenarios through the public HTTP tool/egress path within the design bounds. Assert positive allowed-peer traffic and zero connections for refused destinations.
- [ ] 2.2 Verify supported proxy auth, redirect checks, TLS certificate authority, deadlines and body release remain correct on the integrated sandbox/network tree.
- [ ] 2.3 Supply evidence for parent integration without claiming broad validation, milestone completion or release publication.
