# Backlog

Candidates raised after the capability freeze (ADR-0098). Each entry names the evidence and the seam it would touch; none is normative until promoted by an ADR.

| Candidate | Evidence | Seam |
|---|---|---|
| Detached children with push-based completion (Q25) | harness guides: push completion, kill by label | `subflows`, `suspension` |
| CaMeL-style planner over `flowdef`/`ExprLang` (Q26) | CaMeL, dual-LLM | `languages`, `taint` |
| Script tier for embedded languages (Q21/Q22) | WASM host-driven suspension | `languages` |
| `ToolProgress` event for long-running tools (`gohan.Progress(ctx, msg)`) | data-analyst UX | `tools`, `streams`, `agui` |
| Bound the Docker adapter's egress setup commands and replace the shell-less executable lookup in `installEgressChain` | `adapter/docker/egress.go:68-73` runs `command -v iptables` as a direct argv (no shell), so the lookup fails with 127 on any image without `command`, and any lookup error triggers `apk add --no-cache iptables`; `adapter/docker/workspace.go:72-161,283-290` gives setup commands no deadline when the request timeout and policy `MaxExecTime` are both zero; `TestEgressAllowlist`, `TestEgressPrivateRanges` and `TestEgressDenialObserved` fail at the `adapter/docker` module whenever the container cannot fetch packages, and the setup timing varies (a fast exit 1 in one run, a stall past the 2m test deadline in another) | `adapter/docker` egress setup |
| Container egress to the Alpine package CDN from Docker bridge networks | `apk add --no-cache iptables` inside a bridge-network `alpine:3.23` container fails with `WARNING: fetching https://dl-cdn.alpinelinux.org/alpine/v3.23/main/x86_64/APKINDEX.tar.gz: DNS: transient error (try again later)` and `unable to select packages: iptables (no such package)`, while `nslookup example.com` resolves; another container run resolved the CDN address but its download timed out; the host reaches the CDN in about 0.17 s, yet its resolver returns only IPv6 addresses for it, and the host route to the CDN IPv4 goes through the VPN tunnel `v2tun0` (policy table 2022) while the default route is `wlan0`, so forwarded container traffic does not use the same path | host networking / Docker bridge egress |
