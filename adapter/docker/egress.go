package docker

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
	"github.com/victorzhuk/gohan/core/types"
)

// egressChain is the per-workspace iptables chain a container's OUTPUT
// path jumps to; egressDenyChain carries the DROP rule whose counter is
// the observable refusal count. The deny chain is separate because the
// counting extensions LOG and limit are unavailable in the container's
// network namespace.
const egressChain = "GOHAN_EGRESS"

const egressDenyChain = "GOHAN_EGRESS_DENY"

// capNetAdmin lets the adapter install the egress chain inside the
// container's own network namespace; no other capability is added.
const capNetAdmin = "NET_ADMIN"

// setupEgress enforces the policy at the container network layer (D4).
// An empty Allow denies all egress including DNS through NetworkMode
// "none". A non-empty Allow returns a per-workspace bridge network the
// container attaches to, plus the NET_ADMIN capability the egress chain
// needs.
func (s *Sandbox) setupEgress(ctx context.Context, p types.SandboxPolicy, owner string) (string, string, error) {
	name := fmt.Sprintf("%s-%d", snapshotPrefix, time.Now().UnixNano())
	created, err := s.c.api.NetworkCreate(ctx, name, client.NetworkCreateOptions{
		Driver: "bridge",
		Labels: map[string]string{
			LabelSandbox: "true",
			labelRole:    roleNetwork,
			labelOwner:   owner,
		},
	})
	if err != nil {
		return "", "", fmt.Errorf("docker: create egress network: %w", err)
	}
	return name, created.ID, nil
}

// attachEgress points the container at the per-workspace network and,
// when a fixture network is supplied, also at that network so the
// provider-controlled names resolve to the fixture listeners. It returns
// the endpoint config the create call needs beside HostConfig.
func attachEgress(host *container.HostConfig, netName string, fxNetIDs []string) *network.NetworkingConfig {
	host.NetworkMode = container.NetworkMode(netName)
	endpoints := map[string]*network.EndpointSettings{netName: {}}
	for _, id := range fxNetIDs {
		endpoints[id] = &network.EndpointSettings{}
	}
	return &network.NetworkingConfig{EndpointsConfig: endpoints}
}

// installEgressChain builds the default-deny egress chain in the
// anchor's network namespace, which the workload shares (D4). Docker's
// embedded DNS on 127.0.0.11 rides loopback and stays reachable; each
// allowlisted host is resolved once and permitted by address; under
// AllowPrivate the private ranges pass, under DenyPrivate they fall to
// the default deny, matching std/egress. The whole installation runs
// through the private administration runner against the anchor only and
// is bounded by adminTimeout independently of the workload policy. The
// chain needs the iptables binary, installed through the still-open
// network before the deny rules land.
func (w *workspace) installEgressChain(ctx context.Context) error {
	actx, cancel := context.WithTimeout(ctx, adminTimeout)
	defer cancel()
	out, err := w.adminExec(actx, "sh", "-c", "command -v iptables")
	if err != nil {
		// An administration transport failure fails the setup closed;
		// only a missing binary (non-zero exit) installs it.
		return fmt.Errorf("docker: probe iptables: %w", err)
	}
	if out.ExitCode != 0 {
		if res, err := w.adminExec(actx, "apk", "add", "--no-cache", "iptables"); err != nil || res.ExitCode != 0 {
			return fmt.Errorf("docker: install iptables: %w", err)
		}
	}
	w.egressAllowIPs = w.resolveAllowInside(actx)
	for _, cmd := range w.egressRules() {
		if _, err := w.adminRun(actx, "sh", "-c", cmd); err != nil {
			return fmt.Errorf("docker: egress rule %q: %w", cmd, err)
		}
	}
	return nil
}

// egressRules returns the iptables commands building the chain for this
// workspace's policy. The order is the match order: loopback, then the
// private-range decision, then the allowlist, then the counted deny.
func (w *workspace) egressRules() []string {
	p := w.policy.Egress
	cmds := []string{
		"iptables -N " + egressChain + " 2>/dev/null || true",
		"iptables -F " + egressChain,
		"iptables -C OUTPUT -j " + egressChain + " 2>/dev/null || iptables -I OUTPUT 1 -j " + egressChain,
		"iptables -A " + egressChain + " -o lo -j ACCEPT",
	}
	if p.PrivateRanges == types.AllowPrivate {
		for _, cidr := range privateCIDRs {
			cmds = append(cmds, fmt.Sprintf("iptables -A %s -d %s -j ACCEPT", egressChain, cidr))
		}
	}
	for _, ip := range w.egressAllowIPs {
		cmds = append(cmds, fmt.Sprintf("iptables -A %s -d %s -j ACCEPT", egressChain, ip))
	}
	cmds = append(cmds,
		"iptables -N "+egressDenyChain+" 2>/dev/null || true",
		"iptables -F "+egressDenyChain,
		"iptables -A "+egressDenyChain+" -j DROP",
		fmt.Sprintf("iptables -A %s -j %s", egressChain, egressDenyChain),
	)
	return cmds
}

// privateCIDRs are the ranges DenyPrivate blocks, matching
// std/egress's link-local and private rules.
var privateCIDRs = []string{
	"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "169.254.0.0/16",
}

// resolveAllowInside resolves the literal allow entries with the
// container's own resolver, so the chain permits the addresses the
// container itself dial. Wildcard entries resolve to nothing and so
// permit nothing at the network layer; the deny stays default. Resolved
// private addresses are dropped under DenyPrivate, matching std/egress.
func (w *workspace) resolveAllowInside(ctx context.Context) []string {
	var ips []string
	for _, pattern := range w.policy.Egress.Allow {
		host := pattern
		if i := strings.LastIndex(pattern, ":"); i >= 0 {
			host = pattern[:i]
		}
		if strings.ContainsAny(host, "*?[") {
			continue
		}
		rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		out, err := w.adminRun(rctx, "nslookup", host)
		cancel()
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(out), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 0 || !strings.HasPrefix(fields[0], "Address") {
				continue
			}
			ip := net.ParseIP(fields[len(fields)-1])
			if ip == nil || ip.To4() == nil {
				// The OUTPUT filter chain is IPv4; IPv6 dials fall to
				// the deny and stay unpermitted.
				continue
			}
			if deniedAddress(ip, w.policy.Egress.PrivateRanges) {
				continue
			}
			ips = append(ips, ip.String())
		}
	}
	return ips
}

// deniedAddress mirrors std/egress deniedAddress so the network layer
// and the HTTP client layer apply the same private-range and hop rules.
func deniedAddress(ip net.IP, ranges types.PrivateRanges) bool {
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}
	if ranges == types.AllowPrivate {
		return false
	}
	return ip.IsPrivate() || ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast()
}

// dialFailureMarkers are the stderr signatures of a connect refused at
// the network layer, used on the NetworkMode "none" path where no
// netfilter counter exists.
var dialFailureMarkers = []string{
	"dial failed",
	"bad address", "name resolution", "network unreachable",
	"no route to host", "connection refused", "timed out", "try again",
}

// observeEgress reports denials to the Recorder for the exec just run
// (D4): one refusal observation per affected Exec, never per dropped
// packet. On the bridge path a positive DROP delta is the primary
// evidence; the classified resolution-refusal fallback records only
// when the same Exec carries no DROP evidence, so the two sources are
// never double-counted. A process failure, a sleep timeout or a
// reachable server refusing a connection is not policy denial.
func (w *workspace) observeEgress(ctx context.Context, res types.ExecResult) {
	if w.rec == nil {
		return
	}
	if len(w.policy.Egress.Allow) == 0 {
		// NetworkMode "none" has no counter: the refusal is classified
		// from the exec's stderr alone.
		if res.ExitCode != 0 && containsAny(strings.ToLower(string(res.Stderr)), dialFailureMarkers) {
			w.rec.RecordDenial()
		}
		return
	}
	if !w.chainReadyIs() {
		return
	}
	// The observation runs against the anchor, which owns the network
	// namespace the workload shares; the dedicated egress mutex
	// serializes the counter read-modify-write across concurrent Exec
	// calls without blocking the exec environment.
	w.egressMu.Lock()
	defer w.egressMu.Unlock()
	out, err := w.adminRun(ctx, "iptables", "-L", egressDenyChain, "-vnx")
	if err != nil {
		// The observation cannot be read; record nothing rather than a
		// false clean result. Acceptance fails on the missing count.
		return
	}
	var dropped uint64
	// `iptables -L -vnx` prints a Chain header and a column header
	// before the rule lines; the counted DROP rule is the line whose
	// target field is DROP.
	for _, line := range strings.Split(string(out), "\n") {
		lineFields := strings.Fields(line)
		if len(lineFields) >= 3 && lineFields[2] == "DROP" {
			if _, err := fmt.Sscanf(lineFields[0], "%d", &dropped); err != nil {
				return
			}
			break
		}
	}
	delta := int64(dropped) - int64(w.denySeen)
	w.denySeen = dropped
	if delta > 0 {
		// Packet retransmissions are one refusal observation.
		w.rec.RecordDenial()
		return
	}
	if res.ExitCode != 0 && containsAny(strings.ToLower(string(res.Stderr)), dialFailureMarkers) {
		w.rec.RecordDenial()
	}
}

func containsAny(s string, markers []string) bool {
	for _, m := range markers {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}
