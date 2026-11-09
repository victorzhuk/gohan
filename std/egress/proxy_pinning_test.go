package egress

// Contract tests for change repair-network-pinning, chunk 1.2/2.2 (seam
// proxy-routing). The behaviors under test stay red until the private
// routing clone and the separate proxy TLS handshake land:
//
//   - TestEgressProxyPinning: the proxied absolute request target carries the
//     client-checked destination IP with its effective port while the raw
//     Host header keeps the original authority; redirects revalidate and pin
//     each hop against the original hostname; proxy credentials reach only
//     the proxy hop; unsupported proxy schemes stay refused before dispatch.
//   - TestEgressProxyAuthority: the four-route http/https destination by
//     http/https proxy matrix keeps proxy TLS authority and destination TLS
//     authority separate, with valid-cert positive controls before every
//     wrong-identity case and zero denied-origin forward attempts.
//
// The tests mutate package-level resolution globals through the serial
// fixture helpers and therefore never call t.Parallel.

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

const (
	proxyPinningAllowedMarker  = "allowed-proxy-pinning-marker-v1"
	proxyPinningDeniedMarker   = "denied-proxy-pinning-marker-v2"
	proxyPinningRedirectMarker = "redirect-proxy-pinning-marker-v3"
	authorityPlainMarker       = "allowed-authority-plain-marker-v4"
	authorityTLSMarker         = "allowed-authority-tls-marker-v5"
	authorityWrongMarker       = "wrong-identity-authority-marker-v6"
	authorityDeniedMarker      = "denied-authority-marker-v7"
	authorityProxyName         = "proxy.fixture.test"
	authorityOriginName        = "origin.fixture.test"
	authorityMatrixRun         = "^TestEgressProxyAuthority$"
)

func headersEqual(a, b http.Header) bool {
	if len(a) != len(b) {
		return false
	}
	for k, vs := range a {
		ovs, ok := b[k]
		if !ok || len(ovs) != len(vs) {
			return false
		}
		for i, v := range vs {
			if ovs[i] != v {
				return false
			}
		}
	}
	return true
}

func TestEgressProxyPinning(t *testing.T) {
	allowed := startControlledPeerSerial(t, proxyPinningAllowedMarker)
	denied := startControlledPeerSerial(t, proxyPinningDeniedMarker)
	redirect := startRedirectPeerSerial(t, proxyPinningRedirectMarker)
	allowed.checkReady(t)
	denied.checkReady(t)
	redirect.checkReady(t)
	allowed.refuseUnready(t)
	denied.refuseUnready(t)
	redirect.refuseUnready(t)

	// The proxy's own hostname map selects the denied peer; only IP-keyed
	// routes reach the allowed peers, so any hostname-routed product request
	// is observable as denied-origin forward traffic.
	proxy := startForwardingProxySerial(t, map[string]string{
		// Only never-served hostnames key to the denied peer, so a
		// hostname-routed product request stays observable as denied
		// forward traffic while the served hosts deliver real content.
		"allowed.test":  denied.loopbackAddr(),
		"pin8080.test":  denied.loopbackAddr(),
		"denied.test":   denied.loopbackAddr(),
		"redirect.test": redirect.loopbackAddr(),
		"allowed2.test": redirect.loopbackAddr(),
		"cred.test":     allowed.loopbackAddr(),
		"203.0.113.10":  allowed.loopbackAddr(),
		"203.0.113.11":  allowed.loopbackAddr(),
		"203.0.113.12":  redirect.loopbackAddr(),
		"203.0.113.13":  allowed.loopbackAddr(),
	})
	dials := installCountingResolutionSerial(t, map[string][]net.IPAddr{
		"allowed.test":      {ipAddr("203.0.113.10")},
		"pin8080.test":      {ipAddr("203.0.113.11")},
		"redirect.test":     {ipAddr("203.0.113.12")},
		"cred.test":         {ipAddr("203.0.113.13")},
		"allowed2.test":     {ipAddr("203.0.113.12")},
		"cgnat-target.test": {ipAddr("100.64.7.7")},
		"proxy.test":        {ipAddr("203.0.113.50")},
		"127.0.0.1":         {ipAddr("127.0.0.1")},
	}, dialSeamMapping{
		"203.0.113.10": allowed.loopbackAddr(),
		"203.0.113.11": allowed.loopbackAddr(),
		"203.0.113.12": redirect.loopbackAddr(),
		"203.0.113.13": allowed.loopbackAddr(),
		"203.0.113.50": loopbackHost(proxy.server.Listener.Addr().String()),
	})
	policy := types.EgressPolicy{
		Allow:         []string{"allowed.test", "pin8080.test", "allowed2.test", "redirect.test", "cred.test", "cgnat-target.test"},
		Schemes:       []string{"http"},
		PrivateRanges: types.AllowPrivate,
		Timeout:       10 * time.Second,
	}

	// The redirect legs run through the in-file relay proxy, whose relay
	// hands redirects back to the product instead of following them itself.
	relayProxy := startCredentialProxySerial(t, map[string]string{
		"redirect.test": redirect.loopbackAddr(),
		"allowed2.test": redirect.loopbackAddr(),
		"denied.test":   denied.loopbackAddr(),
		"203.0.113.12":  redirect.loopbackAddr(),
	})

	resetAll := func() {
		allowed.resetCount()
		denied.resetCount()
		redirect.resetObservations()
		proxy.resetCounts()
		proxy.resetObservations()
		relayProxy.resetObservations()
		dials.reset()
	}

	t.Run("forwarded target pinning table", func(t *testing.T) {
		client := Client(policy, WithProxy(parseFixtureProxyURL(t, proxy)))
		for _, tc := range []struct {
			name      string
			targetURL string
			modify    func(*http.Request)
			authority string
			ip        string
			port      string // effective port; "" means the default port rendering may omit it
			path      string
			rawQuery  string
			wantBody  string
		}{
			{
				name: "default port", targetURL: "http://allowed.test/pin-default",
				authority: "allowed.test", ip: "203.0.113.10", port: "80", path: "/pin-default",
				wantBody: proxyPinningAllowedMarker,
			},
			{
				name: "explicit port", targetURL: "http://pin8080.test:8080/pin-explicit",
				authority: "pin8080.test:8080", ip: "203.0.113.11", port: "8080", path: "/pin-explicit",
				wantBody: proxyPinningAllowedMarker,
			},
			{
				name: "escaped path and query", targetURL: "http://allowed.test/esc%20aped%2Fpath?x=1%26y=2",
				authority: "allowed.test", ip: "203.0.113.10", port: "80", path: "/esc%20aped%2Fpath", rawQuery: "x=1%26y=2",
				wantBody: proxyPinningAllowedMarker,
			},
			{
				name: "force query", targetURL: "http://allowed.test/forced",
				modify:    func(req *http.Request) { req.URL.ForceQuery = true },
				authority: "allowed.test", ip: "203.0.113.10", port: "80", path: "/forced",
				wantBody: proxyPinningAllowedMarker,
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				resetAll()
				req, err := http.NewRequest(http.MethodGet, tc.targetURL, nil)
				if err != nil {
					t.Fatal(err)
				}
				if tc.modify != nil {
					tc.modify(req)
				}
				originalURL := req.URL.String()
				originalHost := req.Host
				originalHeader := req.Header.Clone()
				resp, err := client.Do(req)
				if err != nil {
					t.Fatalf("proxied request %s failed: %v (proxy-observed targets: %q); want the pinned destination %s delivered with the original authority", tc.targetURL, err, proxy.rawObservations(), tc.ip)
				}
				body, err := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				if err != nil {
					t.Fatalf("proxied response body read failed for %s: %v", tc.targetURL, err)
				}
				if string(body) != tc.wantBody {
					t.Fatalf("body = %q, want the allowed origin marker %q delivered via the checked destination %s (hostname routing reached a different origin)", body, tc.wantBody, tc.ip)
				}
				if tc.ip == "203.0.113.10" || tc.ip == "203.0.113.11" {
					if got := proxy.originCount(allowed.loopbackAddr()); got != 1 {
						t.Fatalf("allowed origin forward attempts = %d, want 1 for %s", got, tc.targetURL)
					}
				}
				if got := proxy.originCount(denied.loopbackAddr()); got != 0 {
					t.Fatalf("denied origin forward attempts = %d, want 0 (the proxy must not receive or route the original hostname)", got)
				}
				if got := denied.count(); got != 0 {
					t.Fatalf("denied peer requests = %d, want 0", got)
				}
				var obs rawObservation
				matches := 0
				for _, o := range proxy.rawObservations() {
					if strings.Contains(o.Target, tc.path) {
						obs = o
						matches++
					}
				}
				if matches != 1 {
					t.Fatalf("proxy captured %d observations containing path %s (%q), want exactly one", matches, tc.path, proxy.rawObservations())
				}
				parsed, err := url.Parse(obs.Target)
				if err != nil {
					t.Fatalf("proxy-observed absolute target %q does not parse: %v", obs.Target, err)
				}
				if parsed.Hostname() != tc.ip {
					t.Fatalf("absolute request target = %q, want the checked destination IP %s instead of the hostname %q", obs.Target, tc.ip, parsed.Hostname())
				}
				if tc.port != "" && parsed.Port() != tc.port && parsed.Port() != "" {
					t.Fatalf("absolute request target = %q, want effective port %s on the checked IP", obs.Target, tc.port)
				}
				if tc.port == "8080" && !strings.Contains(obs.Target, "203.0.113.11:8080") {
					t.Fatalf("absolute request target = %q, want the explicit port form 203.0.113.11:8080", obs.Target)
				}
				if tc.rawQuery != "" && parsed.RawQuery != tc.rawQuery {
					t.Fatalf("absolute request target = %q, want RawQuery %q preserved exactly once", obs.Target, tc.rawQuery)
				}
				if parsed.EscapedPath() != tc.path {
					t.Fatalf("absolute request target = %q, want escaped path %q preserved exactly once", obs.Target, tc.path)
				}
				if req.URL.ForceQuery {
					if !strings.HasSuffix(obs.Target, tc.path+"?") {
						t.Fatalf("absolute request target = %q, want the forced query separator surviving exactly once as suffix %q", obs.Target, tc.path+"?")
					}
				} else if strings.HasSuffix(obs.Target, "?") {
					t.Fatalf("absolute request target = %q, want no query separator without RawQuery or ForceQuery", obs.Target)
				}
				if obs.Host != tc.authority {
					t.Fatalf("raw Host header = %q, want the original authority %s", obs.Host, tc.authority)
				}
				if req.URL.String() != originalURL {
					t.Fatalf("caller request URL mutated from %q to %q", originalURL, req.URL.String())
				}
				if req.Host != originalHost {
					t.Fatalf("caller request Host mutated from %q to %q", originalHost, req.Host)
				}
				if !headersEqual(originalHeader, req.Header) {
					t.Fatalf("caller request headers mutated: before %v after %v", originalHeader, req.Header)
				}
			})
		}
	})

	t.Run("slash-less programmatic path stays pinned", func(t *testing.T) {
		client := Client(policy, WithProxy(parseFixtureProxyURL(t, proxy)))
		resetAll()
		req, err := http.NewRequest(http.MethodGet, "http://allowed.test/pin-prog", nil)
		if err != nil {
			t.Fatal(err)
		}
		// Programmatic url.URL construction can produce a slash-less
		// EscapedPath; unless the assembled absolute target's path begins
		// with '/', RFC 3986 authority parsing would read the checked
		// IP:port as userinfo and route off the validated destination.
		req.URL = &url.URL{Scheme: "http", Host: "allowed.test", Path: "@evil.com/x"}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("proxied request failed: %v (proxy-observed targets: %q)", err, proxy.rawObservations())
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			t.Fatalf("proxied response body read failed: %v", err)
		}
		if string(body) != proxyPinningAllowedMarker {
			t.Fatalf("body = %q, want the allowed origin marker %q delivered via the checked destination", body, proxyPinningAllowedMarker)
		}
		var obs rawObservation
		matches := 0
		for _, o := range proxy.rawObservations() {
			if strings.Contains(o.Target, "@evil.com/x") {
				obs = o
				matches++
			}
		}
		if matches != 1 {
			t.Fatalf("proxy captured %d observations containing @evil.com/x (%q), want exactly one", matches, proxy.rawObservations())
		}
		const wantPrefix = "http://203.0.113.10:80/"
		if !strings.HasPrefix(obs.Target, wantPrefix) {
			t.Fatalf("absolute request target = %q, want authority ending at the first '/' with prefix %q", obs.Target, wantPrefix)
		}
		if obs.Host != "allowed.test" {
			t.Fatalf("raw Host header = %q, want the original authority allowed.test", obs.Host)
		}
	})

	// Scheme casing must not bypass the Opaque pin: validateRedirect accepts
	// schemes case-insensitively, so a hand-built "HTTP" scheme must never
	// reach the proxy as hostname-routed traffic. The transport normalizes
	// the scheme at RoundTrip entry; net/http's client may reject the raw
	// capital scheme before the transport, which is an acceptable refusal
	// under the same contract: pinned or refused, never hostname-routed.
	t.Run("hand-built capital scheme stays pinned", func(t *testing.T) {
		client := Client(policy, WithProxy(parseFixtureProxyURL(t, proxy)))
		resetAll()
		req := &http.Request{
			Method: http.MethodGet,
			URL:    &url.URL{Scheme: "HTTP", Host: "allowed.test", Path: "/case-leg"},
			Header: make(http.Header),
		}
		resp, err := client.Do(req)
		if err != nil {
			if !strings.Contains(err.Error(), "unsupported protocol scheme") {
				t.Fatalf("capital-scheme refusal reason = %v, want an unsupported-scheme rejection with zero dispatch", err)
			}
			if got := len(proxy.rawObservations()); got != 0 {
				t.Fatalf("capital-scheme refusal reached the proxy %d times %q, want zero proxy connections (never hostname-routed)", got, proxy.rawObservations())
			}
			if got := proxy.originCount(denied.loopbackAddr()); got != 0 {
				t.Fatalf("capital-scheme refusal caused %d denied-origin forward attempts, want 0", got)
			}
			return
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			t.Fatalf("capital-scheme response body read failed: %v", err)
		}
		if string(body) != proxyPinningAllowedMarker {
			t.Fatalf("body = %q, want the allowed origin marker %q delivered via the checked destination (hostname routing reached a different origin)", body, proxyPinningAllowedMarker)
		}
		var obs rawObservation
		matches := 0
		for _, o := range proxy.rawObservations() {
			if strings.Contains(o.Target, "/case-leg") {
				obs = o
				matches++
			}
		}
		if matches != 1 {
			t.Fatalf("proxy captured %d observations containing /case-leg (%q), want exactly one", matches, proxy.rawObservations())
		}
		parsed, err := url.Parse(obs.Target)
		if err != nil {
			t.Fatalf("proxy-observed absolute target %q does not parse: %v", obs.Target, err)
		}
		if parsed.Hostname() != "203.0.113.10" || parsed.Port() != "80" {
			t.Fatalf("absolute request target = %q, want the checked destination authority 203.0.113.10:80", obs.Target)
		}
		if obs.Host != "allowed.test" {
			t.Fatalf("raw Host header = %q, want the original authority allowed.test", obs.Host)
		}
		if got := proxy.originCount(denied.loopbackAddr()); got != 0 {
			t.Fatalf("denied origin forward attempts = %d, want 0", got)
		}
	})

	t.Run("relative redirect repins original hostname", func(t *testing.T) {
		resetAll()
		client := Client(policy, WithProxy(parseProxyURL(t, relayProxy.URL())))
		resp, err := client.Get("http://redirect.test/start")
		if err != nil {
			t.Fatalf("relative redirect request failed: %v (proxy-observed targets: %q)", err, relayProxy.targetsSeen())
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			t.Fatalf("relative redirect body read failed: %v", err)
		}
		if string(body) != proxyPinningRedirectMarker {
			t.Fatalf("relative redirect body = %q, want the redirect origin marker %q from the repinned second hop", body, proxyPinningRedirectMarker)
		}
		if resp.Request.URL.Hostname() != "redirect.test" {
			t.Fatalf("final response URL host = %q, want the original hostname redirect.test preserved, not a pinned IP", resp.Request.URL.Hostname())
		}
		if resp.Request.URL.Path != "/next" {
			t.Fatalf("final response URL path = %q, want /next (the relay must not consume the redirect; the product follows it)", resp.Request.URL.Path)
		}
		targets := relayProxy.targetsSeen()
		nextMatches := 0
		var hopTarget string
		for _, target := range targets {
			if strings.Contains(target, "/next") {
				hopTarget = target
				nextMatches++
			}
		}
		if nextMatches != 1 {
			t.Fatalf("proxy relayed %d observations of the redirect hop (%q), want exactly one", nextMatches, targets)
		}
		if !strings.Contains(hopTarget, "203.0.113.12") {
			t.Fatalf("redirect hop target = %q, want the revalidated second hop pinned to the checked IP 203.0.113.12 of redirect.test", hopTarget)
		}
		if !strings.Contains(hopTarget, "x=%20y") {
			t.Fatalf("redirect hop target = %q, want the escaped relative query x=%%20y preserved exactly once", hopTarget)
		}
		if !strings.HasSuffix(hopTarget, "/next?x=%20y") {
			t.Fatalf("redirect hop target = %q, want the fragment dropped and path/query preserved exactly once", hopTarget)
		}
		if got := len(targets); got != 2 {
			t.Fatalf("proxy relayed %d requests %q, want one per hop (the product revalidated the second hop)", got, targets)
		}
	})

	t.Run("cross-host redirect refused before dispatch", func(t *testing.T) {
		resetAll()
		client := Client(policy, WithProxy(parseProxyURL(t, relayProxy.URL())))
		resp, err := client.Get("http://redirect.test/cross")
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			t.Fatalf("cross-host redirect to denied.test succeeded with body %q, want refusal before dispatch", body)
		}
		var refused types.EgressDenied
		if !errors.As(err, &refused) {
			t.Fatalf("cross-host redirect error = %T (%v), want types.EgressDenied", err, err)
		}
		// The refusal must precede second-hop dispatch: only the first hop
		// reaches the proxy, and the denied origin sees no forward traffic.
		if got := len(relayProxy.targetsSeen()); got != 1 {
			t.Fatalf("relay proxy dispatched %d requests %q, want only the first hop (refusal before next-hop dispatch)", got, relayProxy.targetsSeen())
		}
		if got := proxy.originCount(denied.loopbackAddr()); got != 0 {
			t.Fatalf("denied origin forward attempts = %d, want 0", got)
		}
	})

	t.Run("authorization stripped on cross-host redirect", func(t *testing.T) {
		resetAll()
		client := Client(policy, WithProxy(parseProxyURL(t, relayProxy.URL())))
		req, err := http.NewRequest(http.MethodGet, "http://redirect.test/cross-auth", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer tok-123")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("cross-host redirect to allowed second host failed: %v", err)
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			t.Fatalf("cross-host redirect body read failed: %v", err)
		}
		if string(body) != proxyPinningRedirectMarker {
			t.Fatalf("cross-host redirect body = %q, want the second-host marker %q", body, proxyPinningRedirectMarker)
		}
		if resp.Request.URL.Hostname() != "allowed2.test" {
			t.Fatalf("final response URL host = %q, want the second hop hostname allowed2.test", resp.Request.URL.Hostname())
		}
		auths := redirect.authsSeen()
		if len(auths) != 2 {
			t.Fatalf("origin observed %d requests %q, want both redirect hops", len(auths), auths)
		}
		if auths[0] != "Bearer tok-123" {
			t.Fatalf("first hop Authorization = %q, want the caller credential delivered to the first hop only", auths[0])
		}
		if auths[1] != "" {
			t.Fatalf("second hop Authorization = %q, want the credential stripped on the cross-host redirect", auths[1])
		}
	})

	t.Run("max redirects reason retained", func(t *testing.T) {
		resetAll()
		client := Client(policy, WithProxy(parseProxyURL(t, relayProxy.URL())))
		_, err := client.Get("http://redirect.test/loop")
		if err == nil {
			t.Fatal("redirect loop succeeded, want the MaxRedirects refusal")
		}
		var refused types.EgressDenied
		if !errors.As(err, &refused) {
			t.Fatalf("redirect loop error = %T (%v), want types.EgressDenied with the existing reason", err, err)
		}
		if !strings.Contains(refused.Reason, "scheme not allowed") {
			t.Fatalf("redirect loop reason = %q, want the retained MaxRedirects reason %q", refused.Reason, "scheme not allowed")
		}
	})

	t.Run("proxy credentials reach proxy hop only", func(t *testing.T) {
		resetAll()
		credProxy := startCredentialProxySerial(t, map[string]string{
			"cred.test":    allowed.loopbackAddr(),
			"203.0.113.13": allowed.loopbackAddr(),
		})
		credURL, err := url.Parse("http://user:secret@" + loopbackHost(credProxy.server.Listener.Addr().String()))
		if err != nil {
			t.Fatalf("credential proxy URL parse failed: %v", err)
		}
		client := Client(policy, WithProxy(credURL))
		wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("user:secret"))
		resp, err := client.Get("http://cred.test/cred")
		if err != nil {
			t.Fatalf("credentialed proxied request failed: %v (proxy-observed Proxy-Authorization: %q)", err, credProxy.proxyAuthsSeen())
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			t.Fatalf("credentialed response body read failed: %v", err)
		}
		if string(body) != proxyPinningAllowedMarker {
			t.Fatalf("credentialed body = %q, want the allowed origin marker %q", body, proxyPinningAllowedMarker)
		}
		seen := credProxy.proxyAuthsSeen()
		if len(seen) == 0 || seen[0] != wantAuth {
			t.Fatalf("proxy observed Proxy-Authorization %q, want the native credential %q at the proxy hop", seen, wantAuth)
		}
		if got := len(proxy.rawObservations()); got != 0 {
			t.Fatalf("credentialed request leaked %d observations into the non-credentialed proxy, want zero", got)
		}
		if got := proxy.originCount(denied.loopbackAddr()); got != 0 {
			t.Fatalf("denied origin forward attempts = %d, want 0", got)
		}
	})

	t.Run("unsupported proxy scheme refused pre-dispatch", func(t *testing.T) {
		resetAll()
		socksURL, err := url.Parse("socks5://proxy.test:1080")
		if err != nil {
			t.Fatal(err)
		}
		client := Client(policy, WithProxy(socksURL))
		resp, err := client.Get("http://allowed.test/socks")
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			t.Fatalf("socks5 proxy request succeeded with body %q, want ErrEgressProxyUnsupported refusal", body)
		}
		if !errors.Is(err, ErrEgressProxyUnsupported) {
			t.Fatalf("socks5 error = %v, want ErrEgressProxyUnsupported", err)
		}
		var refused types.EgressDenied
		if !errors.As(err, &refused) {
			t.Fatalf("socks5 error = %T (%v), want types.EgressDenied", err, err)
		}
		if !strings.Contains(refused.Reason, "proxy scheme not supported") {
			t.Fatalf("socks5 denial reason = %q, want %q", refused.Reason, "proxy scheme not supported")
		}
		if got := dials.count(); got != 0 {
			t.Fatalf("refused socks5 request caused %d dial invocations, want zero dials before refusal", got)
		}
		if got := len(proxy.rawObservations()); got != 0 {
			t.Fatalf("refused socks5 request reached the proxy %d times, want zero proxy connections", got)
		}
		if got := proxy.originCount(denied.loopbackAddr()); got != 0 {
			t.Fatalf("refused socks5 request caused %d denied-origin forward attempts, want 0", got)
		}
	})

	// A proxied request to a CGNAT destination cannot slip through the proxy
	// path: destination policy runs before any proxy connection.
	t.Run("proxied CGNAT destination refused before proxy", func(t *testing.T) {
		resetAll()
		denyPolicy := policy
		denyPolicy.PrivateRanges = types.DenyPrivate
		proxyPort, err := url.Parse(proxy.URL())
		if err != nil {
			t.Fatalf("fixture proxy URL parse failed: %v", err)
		}
		denyProxyURL, err := url.Parse("http://proxy.test:" + proxyPort.Port())
		if err != nil {
			t.Fatalf("proxy URL rebuild for proxy.test failed: %v", err)
		}
		client := Client(denyPolicy, WithProxy(denyProxyURL))
		resp, err := client.Get("http://cgnat-target.test/proxied-refusal")
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			t.Fatalf("proxied CGNAT request succeeded with body %q, want private-range refusal before dispatch", body)
		}
		var refused types.EgressDenied
		if !errors.As(err, &refused) {
			t.Fatalf("proxied CGNAT error = %T (%v), want types.EgressDenied", err, err)
		}
		if !strings.Contains(refused.Reason, "private range") {
			t.Fatalf("proxied CGNAT reason = %q, want %q", refused.Reason, "private range")
		}
		if got := dials.count(); got != 0 {
			t.Fatalf("proxied CGNAT refusal caused %d dial invocations, want zero", got)
		}
		if got := len(proxy.rawObservations()); got != 0 {
			t.Fatalf("proxied CGNAT refusal reached the proxy %d times, want zero proxy connections", got)
		}
		if got := proxy.originCount(denied.loopbackAddr()); got != 0 {
			t.Fatalf("denied origin forward attempts = %d, want 0", got)
		}
	})

	// The destination literal exception must not extend to proxy addresses:
	// under DenyPrivate a proxy endpoint inside a private range is refused
	// before dispatch even when the destination itself is allow-listed
	// verbatim.
	t.Run("literal destination does not excuse loopback proxy", func(t *testing.T) {
		resetAll()
		destHost, destPort, err := net.SplitHostPort(allowed.loopbackAddr())
		if err != nil {
			t.Fatalf("allowed peer address %q does not split: %v", allowed.loopbackAddr(), err)
		}
		literalPolicy := policy
		literalPolicy.PrivateRanges = types.DenyPrivate
		literalPolicy.Allow = []string{destHost + ":" + destPort}
		client := Client(literalPolicy, WithProxy(parseFixtureProxyURL(t, proxy)))
		resp, err := client.Get("http://" + destHost + ":" + destPort + "/literal-proxy-denial")
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			t.Fatalf("loopback proxy under DenyPrivate succeeded with body %q, want refusal before dispatch", body)
		}
		var refused types.EgressDenied
		if !errors.As(err, &refused) {
			t.Fatalf("loopback proxy error = %T (%v), want types.EgressDenied", err, err)
		}
		if !strings.Contains(refused.Reason, "loopback") {
			t.Fatalf("loopback proxy refusal reason = %q, want %q", refused.Reason, "loopback")
		}
		if got := dials.count(); got != 0 {
			t.Fatalf("loopback proxy refusal caused %d dial invocations, want zero dispatch before refusal", got)
		}
		if got := len(proxy.rawObservations()); got != 0 {
			t.Fatalf("loopback proxy refusal reached the proxy %d times, want zero proxy connections", got)
		}
		if got := proxy.originCount(allowed.loopbackAddr()); got != 0 {
			t.Fatalf("allowed origin forward attempts = %d, want 0 (the proxy address denial fires despite the destination literal exception)", got)
		}
		if got := allowed.count(); got != 0 {
			t.Fatalf("allowed peer requests = %d, want 0", got)
		}
	})
}

func TestEgressProxyAuthority(t *testing.T) {
	if !inTLSFixtureSubprocess() {
		bundle := newTLSFixtureBundle(t, authorityProxyName, authorityOriginName)
		launchTLSFixtureSubprocessSerial(t, bundle, authorityMatrixRun)
		return
	}

	certDir := filepath.Dir(os.Getenv("SSL_CERT_FILE"))
	plainAllowed := startControlledPeerSerial(t, authorityPlainMarker)
	denied := startControlledPeerSerial(t, authorityDeniedMarker)
	tlsOrigin := startRecordingTLSPeerSerial(t, certDir, "origin", authorityTLSMarker)
	wrongOrigin := startRecordingTLSPeerSerial(t, certDir, "proxy", authorityWrongMarker)
	plainAllowed.checkReady(t)
	denied.checkReady(t)
	tlsOrigin.checkReady(t, authorityOriginName)
	wrongOrigin.checkReady(t, authorityProxyName)
	plainAllowed.refuseUnready(t)
	denied.refuseUnready(t)
	tlsOrigin.refuseUnready(t)
	wrongOrigin.refuseUnready(t)

	routes := map[string]string{
		// Hostname keys are the proxy's own resolution; they select the
		// denied peer (or the wrong-identity peer) so a hostname-routed
		// product request is observable as leaked forward traffic.
		// The product hostname is the certified identity origin.fixture.test.
		"origin.fixture.test": denied.loopbackAddr(),
		"wrong.test":          wrongOrigin.loopbackAddr(),
		"plain.test":          denied.loopbackAddr(),
		"203.0.113.20":        tlsOrigin.loopbackAddr(),
		"203.0.113.21":        wrongOrigin.loopbackAddr(),
		"203.0.113.22":        plainAllowed.loopbackAddr(),
	}
	cpPlain := startCredentialProxySerial(t, routes)
	goodProxy := startCredentialTLSProxySerial(t, routes, certDir+"/proxy.crt", certDir+"/proxy.key")
	wrongProxy := startCredentialTLSProxySerial(t, routes, certDir+"/origin.crt", certDir+"/origin.key")
	credProxy := startCredentialProxySerial(t, routes)

	installControlledResolutionSerial(t, map[string][]net.IPAddr{
		"origin.fixture.test": {ipAddr("203.0.113.20")},
		"wrong.test":          {ipAddr("203.0.113.21")},
		"plain.test":          {ipAddr("203.0.113.22")},
		"proxy.fixture.test":  {ipAddr("127.0.0.1")},
		"127.0.0.1":           {ipAddr("127.0.0.1")},
	}, dialSeamMapping{
		"203.0.113.20": tlsOrigin.loopbackAddr(),
		"203.0.113.21": wrongOrigin.loopbackAddr(),
		"203.0.113.22": plainAllowed.loopbackAddr(),
	})
	policy := types.EgressPolicy{
		Allow:         []string{"*.test"},
		Schemes:       []string{"http", "https"},
		PrivateRanges: types.AllowPrivate,
		Timeout:       5 * time.Second,
	}

	resetAll := func() {
		plainAllowed.resetCount()
		denied.resetCount()
		tlsOrigin.resetObservations()
		wrongOrigin.resetObservations()
		cpPlain.resetObservations()
		goodProxy.resetObservations()
		wrongProxy.resetObservations()
		credProxy.resetObservations()
	}

	// hostnameLeaks counts relayed requests still carrying a hostname
	// authority: pinned requests carry only the checked IP:port.
	hostnameLeaks := func() []string {
		var leaked []string
		for _, target := range cpPlain.targetsSeen() {
			if strings.Contains(target, authorityOriginName+":") || strings.Contains(target, "wrong.test:") || strings.Contains(target, "plain.test:") {
				leaked = append(leaked, target)
			}
		}
		return leaked
	}

	deniedUntouched := func(t *testing.T, leg string) {
		t.Helper()
		if got := denied.count(); got != 0 {
			t.Fatalf("%s: denied peer requests = %d, want 0", leg, got)
		}
		if leaked := hostnameLeaks(); len(leaked) != 0 {
			t.Fatalf("%s: hostname-routed requests observed at the proxy %q, want only IP-pinned dispatch", leg, leaked)
		}
	}

	// Positive control first: a valid exact-host certificate pair over the
	// plain proxy must succeed before any wrong-identity case.
	t.Run("https destination via http proxy positive control", func(t *testing.T) {
		resetAll()
		client := Client(policy, WithProxy(parseProxyURL(t, cpPlain.URL())))
		resp, err := client.Get("https://origin.fixture.test/tls-positive")
		if err != nil {
			t.Fatalf("positive-control HTTPS request failed: %v (proxy-observed CONNECT authorities: %q); want CONNECT to the checked destination 203.0.113.20:443 with original-host verification", err, cpPlain.targetsSeen())
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			t.Fatalf("positive-control body read failed: %v", err)
		}
		if string(body) != authorityTLSMarker {
			t.Fatalf("positive-control body = %q (CONNECT authorities: %q), want the origin marker %q through the pinned destination", body, cpPlain.targetsSeen(), authorityTLSMarker)
		}
		connects := cpPlain.targetsSeen()
		if len(connects) != 1 {
			t.Fatalf("proxy observed %d CONNECT-style requests %q, want exactly one for this leg", len(connects), connects)
		}
		if host, port, err := net.SplitHostPort(connects[0]); err != nil || host != "203.0.113.20" || port != "443" {
			t.Fatalf("CONNECT authority = %q, want the checked destination 203.0.113.20:443 selected explicitly for this leg, never the hostname", connects[0])
		}
		hosts, snis, _, _ := tlsOrigin.seen()
		if len(hosts) != 1 || hosts[0] != authorityOriginName {
			t.Fatalf("origin Hosts = %q, want exactly one request with the original authority %s", hosts, authorityOriginName)
		}
		if len(snis) != 1 || snis[0] != authorityOriginName {
			t.Fatalf("origin SNI = %q, want the original hostname %s for destination certificate verification", snis, authorityOriginName)
		}
		if resp.Request.URL.Hostname() != authorityOriginName {
			t.Fatalf("response URL host = %q, want the original hostname %s preserved", resp.Request.URL.Hostname(), authorityOriginName)
		}
		if got := wrongOrigin.count(); got != 0 {
			t.Fatalf("wrong-identity origin requests = %d, want 0 in the positive leg", got)
		}
		deniedUntouched(t, "https-via-http positive control")
	})

	t.Run("wrong destination identity refused by hostname verification", func(t *testing.T) {
		resetAll()
		client := Client(policy, WithProxy(parseProxyURL(t, cpPlain.URL())))
		resp, err := client.Get("https://wrong.test/wrong-identity")
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			t.Fatalf("wrong-identity destination certificate accepted with body %q, want x509 hostname verification failure", body)
		}
		// Go boxes the hostname mismatch as a VALUE x509.HostnameError
		// inside *tls.CertificateVerificationError, so the errors.As target
		// must be the value type, never the pointer type.
		var hostnameErr x509.HostnameError
		if !errors.As(err, &hostnameErr) || !strings.Contains(err.Error(), authorityProxyName) {
			t.Fatalf("wrong-identity error = %v, want x509 hostname verification failure naming the mismatched identity %s", err, authorityProxyName)
		}
		if got := wrongOrigin.count(); got != 0 {
			t.Fatalf("wrong-identity origin requests = %d, want 0 completed HTTP requests after failed verification", got)
		}
		deniedUntouched(t, "wrong destination identity")
	})

	t.Run("http destination via https proxy positive control", func(t *testing.T) {
		resetAll()
		client := Client(policy, WithProxy(namedProxyURL(t, goodProxy, authorityProxyName)))
		resp, err := client.Get("http://plain.test/via-https-proxy")
		if err != nil {
			t.Fatalf("HTTPS-proxy positive control failed: %v; want the outer proxy TLS handshake verified against %s and the plain destination forwarded with the pinned target", err, authorityProxyName)
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			t.Fatalf("HTTPS-proxy positive control body read failed: %v", err)
		}
		if string(body) != authorityPlainMarker {
			t.Fatalf("HTTPS-proxy body = %q, want the plain origin marker %q forwarded through the verified HTTPS proxy", body, authorityPlainMarker)
		}
		if got := len(cpPlain.targetsSeen()); got != 0 {
			t.Fatalf("plain proxy saw %d observations from the HTTPS-proxy leg, want zero (the proxies must not conflate)", got)
		}
		if got := plainAllowed.count(); got != 1 {
			t.Fatalf("plain origin requests = %d, want 1", got)
		}
		deniedUntouched(t, "http via https proxy positive control")
	})

	t.Run("wrong proxy identity refused before dispatch", func(t *testing.T) {
		resetAll()
		client := Client(policy, WithProxy(namedProxyURL(t, wrongProxy, authorityProxyName)))
		resp, err := client.Get("http://plain.test/wrong-proxy")
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			t.Fatalf("proxy presenting the origin-only certificate accepted with body %q, want the outer handshake refused against the proxy name %s", body, authorityProxyName)
		}
		var hostnameErr x509.HostnameError
		if !errors.As(err, &hostnameErr) || !strings.Contains(err.Error(), authorityOriginName) {
			t.Fatalf("wrong proxy identity error = %v, want x509 hostname verification failure naming the certificate identity %s before any dispatch", err, authorityOriginName)
		}
		if got := plainAllowed.count(); got != 0 {
			t.Fatalf("plain origin requests = %d, want 0 (the outer failure must precede proxy dispatch)", got)
		}
		if auths := wrongProxy.proxyAuthsSeen(); len(auths) != 0 {
			t.Fatalf("wrong-identity proxy recorded %d requests %q, want zero dispatched requests", len(auths), auths)
		}
		deniedUntouched(t, "wrong proxy identity")
	})

	t.Run("https destination via https proxy both authorities", func(t *testing.T) {
		resetAll()
		client := Client(policy, WithProxy(namedProxyURL(t, goodProxy, authorityProxyName)))
		resp, err := client.Get("https://origin.fixture.test/nested-tls")
		if err != nil {
			t.Fatalf("nested TLS request failed: %v (relay targets: %q); want the outer handshake verifying %s and the inner handshake verifying %s as separate authorities", err, cpPlain.targetsSeen(), authorityProxyName, authorityOriginName)
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			t.Fatalf("nested TLS body read failed: %v", err)
		}
		if string(body) != authorityTLSMarker {
			t.Fatalf("nested TLS body = %q, want the origin marker %q with both distinct authorities verified", body, authorityTLSMarker)
		}
		if resp.TLS == nil || len(resp.TLS.PeerCertificates) == 0 {
			t.Fatalf("no destination TLS state on the nested response; want inner verification against %s", authorityOriginName)
		}
		names := resp.TLS.PeerCertificates[0].DNSNames
		if !containsString(names, authorityOriginName) || containsString(names, authorityProxyName) {
			t.Fatalf("destination certificate names = %v, want only the origin identity %s and never the proxy identity %s", names, authorityOriginName, authorityProxyName)
		}
		connects := cpPlain.targetsSeen()
		if got := len(connects); got != 0 {
			t.Fatalf("plain proxy observed %d CONNECT requests %q in the HTTPS-proxy leg, want zero", got, connects)
		}
		hosts, snis, _, _ := tlsOrigin.seen()
		if len(hosts) != 1 || hosts[0] != authorityOriginName || len(snis) != 1 || snis[0] != authorityOriginName {
			t.Fatalf("origin Hosts %q SNIs %q, want exactly one request carrying the original authority %s for both", hosts, snis, authorityOriginName)
		}
		deniedUntouched(t, "https via https proxy")
	})

	t.Run("connect credentials native at proxy absent in tunnel", func(t *testing.T) {
		resetAll()
		credURL, err := url.Parse("http://user:secret@" + loopbackHost(credProxy.server.Listener.Addr().String()))
		if err != nil {
			t.Fatalf("credential proxy URL parse failed: %v", err)
		}
		client := Client(policy, WithProxy(credURL))
		wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("user:secret"))
		resp, err := client.Get("https://origin.fixture.test/connect-cred")
		if err != nil {
			t.Fatalf("credentialed CONNECT request failed: %v (proxy-observed Proxy-Authorization: %q); want native CONNECT credentials at the proxy hop only", err, credProxy.proxyAuthsSeen())
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			t.Fatalf("credentialed CONNECT body read failed: %v", err)
		}
		if string(body) != authorityTLSMarker {
			t.Fatalf("credentialed CONNECT body = %q, want the origin marker %q through the tunneled destination", body, authorityTLSMarker)
		}
		seen := credProxy.proxyAuthsSeen()
		found := false
		for _, a := range seen {
			if a == wantAuth {
				found = true
			}
		}
		if !found {
			t.Fatalf("proxy observed Proxy-Authorization %q, want the native credential %q carried on the CONNECT request", seen, wantAuth)
		}
		_, _, _, originProxyAuths := tlsOrigin.seen()
		for _, a := range originProxyAuths {
			if a != "" {
				t.Fatalf("tunneled origin request carried Proxy-Authorization %q, want proxy credentials absent inside the tunnel", a)
			}
		}
		deniedUntouched(t, "connect credentials")
	})
}

func parseProxyURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("proxy URL %q parse failed: %v", raw, err)
	}
	return u
}

// namedProxyURL rebuilds a proxy URL with a hostname carrying the proxy's
// certificate identity while keeping the fixture's loopback port.
func namedProxyURL(t *testing.T, cp *credentialProxy, host string) *url.URL {
	t.Helper()
	base := parseProxyURL(t, cp.URL())
	u, err := url.Parse("https://" + host + ":" + base.Port())
	if err != nil {
		t.Fatalf("proxy URL rebuild for %s failed: %v", host, err)
	}
	return u
}

// redirectPeer is a plain origin serving real 302 redirects and recording
// the Authorization header of every request it receives.
type redirectPeer struct {
	server   *httptest.Server
	marker   string
	requests atomic.Int32
	ready    atomic.Bool
	mu       sync.Mutex
	auths    []string
}

func startRedirectPeerSerial(t *testing.T, marker string) *redirectPeer {
	t.Helper()
	p := &redirectPeer{marker: marker}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.requests.Add(1)
		p.mu.Lock()
		p.auths = append(p.auths, r.Header.Get("Authorization"))
		p.mu.Unlock()
		switch r.URL.Path {
		case "/start":
			w.Header().Set("Location", "/next?x=%20y#preserve")
			w.WriteHeader(http.StatusFound)
		case "/cross":
			w.Header().Set("Location", "http://denied.test/evil")
			w.WriteHeader(http.StatusFound)
		case "/cross-auth":
			w.Header().Set("Location", "http://allowed2.test/next")
			w.WriteHeader(http.StatusFound)
		case "/loop":
			w.Header().Set("Location", "/loop")
			w.WriteHeader(http.StatusFound)
		default:
			_, _ = io.WriteString(w, p.marker)
		}
	})
	p.server = httptest.NewServer(handler)
	t.Cleanup(func() { closeBounded(t, p.server, "redirect peer") })
	return p
}

func (p *redirectPeer) loopbackAddr() string {
	return p.server.Listener.Addr().String()
}

func (p *redirectPeer) authsSeen() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, len(p.auths))
	copy(out, p.auths)
	return out
}

func (p *redirectPeer) resetObservations() {
	p.requests.Store(0)
	p.mu.Lock()
	p.auths = nil
	p.mu.Unlock()
}

// checkReady performs a direct health check before any product use.
func (p *redirectPeer) checkReady(t *testing.T) {
	t.Helper()
	client := &http.Client{Timeout: fixtureBoundedSeconds * time.Second}
	resp, err := client.Get(p.server.URL + "/ready")
	if err != nil {
		t.Fatalf("redirect peer readiness check failed: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("redirect peer readiness body read failed: %v", err)
	}
	if string(body) != p.marker {
		t.Fatalf("redirect peer readiness body = %q, want %q", body, p.marker)
	}
	p.ready.Store(true)
}

func (p *redirectPeer) refuseUnready(t *testing.T) {
	t.Helper()
	if !p.ready.Load() {
		t.Fatal("redirect peer refused: readiness check not established")
	}
}

// credentialProxy is a forwarding proxy that records the Proxy-Authorization
// of every request and CONNECT head it receives and strips hop-by-hop proxy
// credentials before any origin dispatch.
type credentialProxy struct {
	server     *httptest.Server
	routes     map[string]string
	client     *http.Client
	relayWG    sync.WaitGroup
	mu         sync.Mutex
	proxyAuths []string
	targets    []string
}

func startCredentialProxySerial(t *testing.T, routes map[string]string) *credentialProxy {
	return startCredentialProxyBase(t, routes, nil)
}

func startCredentialTLSProxySerial(t *testing.T, routes map[string]string, certPath, keyPath string) *credentialProxy {
	pair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		t.Fatalf("credential proxy certificate load failed: %v", err)
	}
	return startCredentialProxyBase(t, routes, &tls.Config{
		Certificates: []tls.Certificate{pair},
		MinVersion:   tls.VersionTLS12,
	})
}

// startCredentialProxyBase starts the recording proxy. A non-nil tlsCfg makes
// the proxy endpoint HTTPS so the outer proxy TLS handshake is exercised.
func startCredentialProxyBase(t *testing.T, routes map[string]string, tlsCfg *tls.Config) *credentialProxy {
	t.Helper()
	cp := &credentialProxy{routes: routes}
	// The relay must hand every response, including redirects, back to the
	// product so the product's own CheckRedirect revalidates each hop.
	cp.client = &http.Client{
		Timeout:       fixtureBoundedSeconds * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	handler := http.HandlerFunc(cp.serve)
	cp.server = httptest.NewUnstartedServer(handler)
	cp.server.TLS = tlsCfg
	if tlsCfg != nil {
		cp.server.StartTLS()
	} else {
		cp.server.Start()
	}
	t.Cleanup(func() {
		cp.client.CloseIdleConnections()
		closeBounded(t, cp.server, "credential proxy")
		relayDone := make(chan struct{})
		go func() {
			cp.relayWG.Wait()
			close(relayDone)
		}()
		select {
		case <-relayDone:
		case <-time.After(fixtureBoundedSeconds * time.Second):
			t.Fatalf("credential proxy relay join exceeded %ds bound", fixtureBoundedSeconds)
		}
	})
	return cp
}

func (cp *credentialProxy) recordRequest(auth, target string) {
	cp.mu.Lock()
	cp.proxyAuths = append(cp.proxyAuths, auth)
	cp.targets = append(cp.targets, target)
	cp.mu.Unlock()
}

func (cp *credentialProxy) serve(w http.ResponseWriter, r *http.Request) {
	auth := r.Header.Get("Proxy-Authorization")
	cp.recordRequest(auth, r.RequestURI)
	if r.Method == http.MethodConnect {
		cp.serveConnect(w, r)
		return
	}
	target := r.RequestURI
	u, err := url.ParseRequestURI(target)
	if err != nil {
		http.Error(w, "credential proxy cannot parse request target: "+err.Error(), http.StatusBadRequest)
		return
	}
	origin, ok := cp.routes[u.Hostname()]
	if !ok {
		http.Error(w, "credential proxy has no route for "+u.Hostname(), http.StatusBadGateway)
		return
	}
	outURL := &url.URL{Scheme: "http", Host: origin}
	outURL.Path = u.Path
	outURL.RawQuery = u.RawQuery
	outReq, err := http.NewRequest(r.Method, outURL.String(), r.Body)
	if err != nil {
		http.Error(w, "credential proxy forward build failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	outReq.Host = r.Host
	for k, vs := range r.Header {
		if strings.EqualFold(k, "Proxy-Authorization") || strings.EqualFold(k, "Proxy-Connection") {
			continue
		}
		for _, v := range vs {
			outReq.Header.Add(k, v)
		}
	}
	resp, err := cp.client.Do(outReq)
	if err != nil {
		http.Error(w, "credential proxy forward failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (cp *credentialProxy) serveConnect(w http.ResponseWriter, r *http.Request) {
	authority := r.RequestURI
	host, _, err := net.SplitHostPort(authority)
	if err != nil {
		http.Error(w, "credential proxy cannot parse CONNECT authority: "+err.Error(), http.StatusBadRequest)
		return
	}
	origin, ok := cp.routes[host]
	if !ok {
		http.Error(w, "credential proxy has no route for "+host, http.StatusBadGateway)
		return
	}
	originHost, originPort, _ := net.SplitHostPort(origin)
	dest := net.JoinHostPort(loopbackHost(originHost), originPort)
	upstream, err := net.DialTimeout("tcp", dest, fixtureBoundedSeconds*time.Second)
	if err != nil {
		http.Error(w, "credential proxy CONNECT dial failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		_ = upstream.Close()
		http.Error(w, "credential proxy cannot hijack", http.StatusInternalServerError)
		return
	}
	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		_ = upstream.Close()
		return
	}
	_, _ = io.WriteString(clientConn, "HTTP/1.1 200 Connection Established\r\n\r\n")
	cp.relayWG.Add(2)
	go func() {
		defer cp.relayWG.Done()
		relayBounded(clientConn, upstream)
	}()
	go func() {
		defer cp.relayWG.Done()
		relayBounded(upstream, clientConn)
	}()
}

func (cp *credentialProxy) URL() string {
	return cp.server.URL
}

func (cp *credentialProxy) proxyAuthsSeen() []string {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	out := make([]string, len(cp.proxyAuths))
	copy(out, cp.proxyAuths)
	return out
}

func (cp *credentialProxy) targetsSeen() []string {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	out := make([]string, len(cp.targets))
	copy(out, cp.targets)
	return out
}

func (cp *credentialProxy) resetObservations() {
	cp.mu.Lock()
	cp.proxyAuths = nil
	cp.targets = nil
	cp.mu.Unlock()
}

// recordingTLSPeer is a TLS origin with a bundle certificate, a unique
// response marker, and observation of Host, SNI, Authorization and
// Proxy-Authorization on every request arriving over a CONNECT tunnel.
type recordingTLSPeer struct {
	server   *httptest.Server
	marker   string
	requests atomic.Int32
	ready    atomic.Bool
	mu       sync.Mutex
	hosts    []string
	snis     []string
	auths    []string
	proxyAu  []string
}

func startRecordingTLSPeerSerial(t *testing.T, certDir, kind, marker string) *recordingTLSPeer {
	t.Helper()
	cert, err := tls.LoadX509KeyPair(filepath.Join(certDir, kind+".crt"), filepath.Join(certDir, kind+".key"))
	if err != nil {
		t.Fatalf("recording peer %s certificate load failed: %v", kind, err)
	}
	p := &recordingTLSPeer{marker: marker}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.requests.Add(1)
		sni := ""
		if r.TLS != nil {
			sni = r.TLS.ServerName
		}
		p.mu.Lock()
		p.hosts = append(p.hosts, r.Host)
		p.snis = append(p.snis, sni)
		p.auths = append(p.auths, r.Header.Get("Authorization"))
		p.proxyAu = append(p.proxyAu, r.Header.Get("Proxy-Authorization"))
		p.mu.Unlock()
		_, _ = io.WriteString(w, p.marker)
	})
	p.server = httptest.NewUnstartedServer(handler)
	p.server.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	p.server.StartTLS()
	t.Cleanup(func() { closeBounded(t, p.server, "recording TLS peer") })
	return p
}

func (p *recordingTLSPeer) loopbackAddr() string {
	return p.server.Listener.Addr().String()
}

func (p *recordingTLSPeer) count() int32 {
	return p.requests.Load()
}

func (p *recordingTLSPeer) seen() (hosts, snis, auths, proxyAuths []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	copySlice := func(in []string) []string {
		out := make([]string, len(in))
		copy(out, in)
		return out
	}
	return copySlice(p.hosts), copySlice(p.snis), copySlice(p.auths), copySlice(p.proxyAu)
}

func (p *recordingTLSPeer) resetObservations() {
	p.requests.Store(0)
	p.mu.Lock()
	p.hosts, p.snis, p.auths, p.proxyAu = nil, nil, nil, nil
	p.mu.Unlock()
}

// checkReady performs a direct TLS health check through the default trust
// (SSL_CERT_FILE), dialing the peer directly before any product use.
func (p *recordingTLSPeer) checkReady(t *testing.T, verifyName string) {
	t.Helper()
	addr := p.server.Listener.Addr().String()
	conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: verifyName, MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatalf("recording TLS peer readiness check against %s failed: %v", verifyName, err)
	}
	_ = conn.Close()
	p.ready.Store(true)
}

func (p *recordingTLSPeer) refuseUnready(t *testing.T) {
	t.Helper()
	if !p.ready.Load() {
		t.Fatal("recording TLS peer refused: readiness check not established")
	}
}
