package types

import (
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
)

// RequiresEgress reports whether a tool reaches the network and therefore must
// carry an EgressPolicy. Build derives it from registration (WithExfil, an
// HTTP executor, a shipped HTTP tool); the flag cannot be read off ToolSpec
// alone, so Build passes it in.
func RequiresEgress(spec ToolSpec, network bool) bool {
	return network
}

// CheckEgress refuses a network tool without an EgressPolicy.
func CheckEgress(spec ToolSpec, network bool) error {
	if network && spec.Egress == nil {
		return fmt.Errorf("tool %s: %w", spec.Name, ErrEgressPolicyRequired)
	}
	return nil
}

// DeriveExfil derives Capabilities.Exfil from the egress policy: any Allow
// entry outside the private ranges makes the tool exfil-capable. Without a
// policy the author's declaration stands.
func DeriveExfil(spec ToolSpec) bool {
	if spec.Egress == nil {
		return spec.Capabilities.Exfil
	}
	for _, entry := range spec.Egress.Allow {
		if !privateEntry(entry, spec.Egress.PrivateRanges) {
			return true
		}
	}
	return false
}

// privateEntry reports whether an Allow entry stays inside the private
// address space: a private IP literal, or a host glob the author opened up to
// by AllowPrivate (a glob cannot be pinned to public infrastructure).
func privateEntry(entry string, pr PrivateRanges) bool {
	host := entry
	if ap, err := netip.ParseAddrPort(entry); err == nil {
		host = ap.Addr().String()
	} else if h, _, err := splitHostPort(entry); err == nil {
		host = h
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		return privateAddr(addr)
	}
	if strings.ContainsAny(host, "*?") {
		return pr == AllowPrivate
	}
	return false
}

func splitHostPort(entry string) (string, string, error) {
	if ap, err := netip.ParseAddrPort(entry); err == nil {
		return ap.Addr().String(), strconv.FormatUint(uint64(ap.Port()), 10), nil
	}
	host, port, ok := strings.Cut(entry, ":")
	if !ok || !validPort(port) {
		return "", "", errors.New("not host:port")
	}
	return host, port, nil
}

func validPort(port string) bool {
	if port == "" {
		return false
	}
	for _, r := range port {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func privateAddr(addr netip.Addr) bool {
	for _, prefix := range privatePrefixes(addr) {
		if prefix.Contains(addr.Unmap()) {
			return true
		}
	}
	return false
}

func privatePrefixes(addr netip.Addr) []netip.Prefix {
	if addr.Is4() || addr.Is4In6() {
		return []netip.Prefix{
			netip.MustParsePrefix("10.0.0.0/8"),
			netip.MustParsePrefix("172.16.0.0/12"),
			netip.MustParsePrefix("192.168.0.0/16"),
			netip.MustParsePrefix("127.0.0.0/8"),
			netip.MustParsePrefix("169.254.0.0/16"),
			netip.MustParsePrefix("100.64.0.0/10"),
		}
	}
	return []netip.Prefix{
		netip.MustParsePrefix("fc00::/7"),
		netip.MustParsePrefix("fe80::/10"),
		netip.MustParsePrefix("::1/128"),
	}
}
