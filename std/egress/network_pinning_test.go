package egress

import (
	"context"
	"crypto/tls"
	"crypto/x509"
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

// TestNetworkPinningScenarios reproduces the three network-pinning behavior
// contracts through the public egress client boundary:
//
//   - tools.cgnat-destination-denied: CGNAT destinations are refused under
//     DenyPrivate before any dial, while explicit literal allowances and
//     addresses outside 100.64.0.0/10 keep their prior behavior.
//   - tools.proxy-destination-pinned: a proxied request uses the client-
//     validated destination address; the proxy's own DNS resolution can
//     neither replace it nor reach the denied peer.
//   - tools.proxy-tls-authority-preserved: CONNECT pins the checked address
//     while certificate verification and the HTTP Host keep the original
//     authority.
//
// The leaves fail until the matching repairs land; they are the red evidence
// for change repair-network-pinning. They mutate package-level resolution
// globals and therefore run serially (no t.Parallel).
func TestNetworkPinningScenarios(t *testing.T) {
	t.Run("tools.cgnat-destination-denied", testCGNATDestinationDenied)
	t.Run("tools.proxy-destination-pinned", testProxyDestinationPinned)
	t.Run("tools.proxy-tls-authority-preserved", testProxyTLSAuthorityPreserved)
}

const (
	cgnatAllowedMarker   = "allowed-cgnat-marker-v1"
	cgnatDeniedMarker    = "denied-cgnat-marker-v2"
	pinnedAllowedMarker  = "allowed-pinned-marker-v1"
	pinnedDeniedMarker   = "denied-pinned-marker-v2"
	tlsAllowedMarker     = "allowed-tls-marker-v1"
	tlsDeniedMarker      = "denied-tls-marker-v3"
	tlsWrongIdentityName = "proxy.fixture.test"
	tlsOriginName        = "allowed.test"
)

func testCGNATDestinationDenied(t *testing.T) {
	allowed := startControlledPeerSerial(t, cgnatAllowedMarker)
	denied := startControlledPeerSerial(t, cgnatDeniedMarker)
	allowed.checkReady(t)
	denied.checkReady(t)
	allowed.refuseUnready(t)
	denied.refuseUnready(t)
	allowed.resetCount()
	denied.resetCount()

	dns := map[string][]net.IPAddr{
		"ok.test":             {ipAddr("203.0.113.10")},
		"cgnat-low.test":      {ipAddr("100.64.0.0")},
		"cgnat-high.test":     {ipAddr("100.127.255.255")},
		"cgnat-interior.test": {ipAddr("100.100.5.5")},
		"cgnat-mapped.test":   {ipAddr("::ffff:100.64.0.1")},
		"rfc1918.test":        {ipAddr("172.16.5.5")},
		"nbr-below.test":      {ipAddr("100.63.255.255")},
		"nbr-above.test":      {ipAddr("100.128.0.0")},
	}
	seam := dialSeamMapping{
		"203.0.113.10":    allowed.loopbackAddr(),
		"100.64.0.0":      allowed.loopbackAddr(),
		"100.127.255.255": allowed.loopbackAddr(),
		"100.100.5.5":     allowed.loopbackAddr(),
		"100.64.0.1":      allowed.loopbackAddr(),
		"172.16.5.5":      allowed.loopbackAddr(),
		"100.63.255.255":  allowed.loopbackAddr(),
		"100.128.0.0":     allowed.loopbackAddr(),
		"100.64.9.9":      allowed.loopbackAddr(),
	}
	dials := installCountingResolutionSerial(t, dns, seam)
	client := Client(types.EgressPolicy{
		Allow:         []string{"*.test", "100.64.9.9"},
		Schemes:       []string{"http"},
		PrivateRanges: types.DenyPrivate,
		Timeout:       10 * time.Second,
	})

	// Positive control: an allowed public hostname reaches its peer and
	// returns its unique marker.
	resp, err := client.Get("http://ok.test/marker")
	if err != nil {
		t.Fatalf("positive control to allowed host ok.test failed: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("positive control body read failed: %v", err)
	}
	if string(body) != cgnatAllowedMarker {
		t.Fatalf("positive control body = %q, want %q", body, cgnatAllowedMarker)
	}
	if allowed.count() != 1 || denied.count() != 0 {
		t.Fatalf("positive control counts allowed=%d denied=%d, want 1/0", allowed.count(), denied.count())
	}
	allowed.resetCount()
	denied.resetCount()

	for _, tc := range []struct{ host, ip string }{
		{"cgnat-low.test", "100.64.0.0"},
		{"cgnat-high.test", "100.127.255.255"},
		{"cgnat-interior.test", "100.100.5.5"},
		{"cgnat-mapped.test", "100.64.0.1"},
	} {
		dials.reset()
		resp, err := client.Get("http://" + tc.host + "/cgnat-check")
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			t.Fatalf("request to %s resolving to CGNAT address %s succeeded with body %q, want types.EgressDenied private-range refusal before any dial", tc.host, tc.ip, body)
		}
		var refused types.EgressDenied
		if !errors.As(err, &refused) {
			t.Fatalf("error for %s resolving to %s is %T (%v), want types.EgressDenied", tc.host, tc.ip, err, err)
		}
		if !strings.Contains(refused.Reason, "private range") {
			t.Fatalf("denial reason for %s = %q, want it to contain %q", tc.host, refused.Reason, "private range")
		}
		if got := dials.count(); got != 0 {
			t.Fatalf("refused CGNAT request to %s caused %d dial invocations, want zero dials before refusal (pre-dial refusal, not dial-then-close)", tc.host, got)
		}
		if allowed.count() != 0 || denied.count() != 0 {
			t.Fatalf("refused CGNAT request to %s caused peer activity allowed=%d denied=%d, want zero destination dials", tc.host, allowed.count(), denied.count())
		}
	}

	// Existing-private regression: an RFC1918 address is refused under
	// DenyPrivate with the same pre-dial proof, named separately from the
	// CGNAT interior cases.
	dials.reset()
	resp, err = client.Get("http://rfc1918.test/rfc1918-check")
	if err == nil {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		t.Fatalf("request to rfc1918.test resolving to 172.16.5.5 succeeded with body %q, want types.EgressDenied private-range refusal before any dial", body)
	}
	var rfcRefused types.EgressDenied
	if !errors.As(err, &rfcRefused) {
		t.Fatalf("error for rfc1918.test resolving to 172.16.5.5 is %T (%v), want types.EgressDenied", err, err)
	}
	if !strings.Contains(rfcRefused.Reason, "private range") {
		t.Fatalf("denial reason for rfc1918.test = %q, want it to contain %q", rfcRefused.Reason, "private range")
	}
	if got := dials.count(); got != 0 {
		t.Fatalf("refused RFC1918 request caused %d dial invocations, want zero dials before refusal", got)
	}
	if allowed.count() != 0 || denied.count() != 0 {
		t.Fatalf("refused RFC1918 request caused peer activity allowed=%d denied=%d, want zero destination dials", allowed.count(), denied.count())
	}

	// The explicit literal allow exception stays effective inside the range.
	resp, err = client.Get("http://100.64.9.9:80/literal")
	if err != nil {
		t.Fatalf("explicitly allowed CGNAT literal 100.64.9.9 refused: %v", err)
	}
	body, err = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("allowed literal body read failed: %v", err)
	}
	if string(body) != cgnatAllowedMarker {
		t.Fatalf("allowed literal body = %q, want %q", body, cgnatAllowedMarker)
	}
	if allowed.count() != 1 {
		t.Fatalf("allowed literal peer count = %d, want 1", allowed.count())
	}
	allowed.resetCount()

	// A CGNAT literal absent from the allow list is refused as a private
	// range with zero dials: destination policy runs before dispatch.
	dials.reset()
	resp, err = client.Get("http://100.64.9.8/non-allowlisted")
	if err == nil {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		t.Fatalf("request to non-allowlisted CGNAT literal 100.64.9.8 succeeded with body %q, want types.EgressDenied private-range refusal", body)
	}
	var literalRefused types.EgressDenied
	if !errors.As(err, &literalRefused) {
		t.Fatalf("error for 100.64.9.8 is %T (%v), want types.EgressDenied", err, err)
	}
	if !strings.Contains(literalRefused.Reason, "private range") {
		t.Fatalf("denial reason for 100.64.9.8 = %q, want it to contain %q", literalRefused.Reason, "private range")
	}
	if got := dials.count(); got != 0 {
		t.Fatalf("refused literal 100.64.9.8 caused %d dial invocations, want zero dials before refusal", got)
	}
	if allowed.count() != 0 || denied.count() != 0 {
		t.Fatalf("refused literal 100.64.9.8 caused peer activity allowed=%d denied=%d, want zero destination dials", allowed.count(), denied.count())
	}

	// Addresses outside 100.64.0.0/10 keep their prior allowed behavior.
	for _, nbr := range []string{"nbr-below.test", "nbr-above.test"} {
		resp, err := client.Get("http://" + nbr + "/neighbor")
		if err != nil {
			t.Fatalf("neighbor %s outside 100.64.0.0/10 refused: %v", nbr, err)
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			t.Fatalf("neighbor %s body read failed: %v", nbr, err)
		}
		if string(body) != cgnatAllowedMarker {
			t.Fatalf("neighbor %s body = %q, want %q", nbr, body, cgnatAllowedMarker)
		}
	}
}

func testProxyDestinationPinned(t *testing.T) {
	allowed := startControlledPeerSerial(t, pinnedAllowedMarker)
	denied := startControlledPeerSerial(t, pinnedDeniedMarker)
	allowed.checkReady(t)
	denied.checkReady(t)
	allowed.refuseUnready(t)
	denied.refuseUnready(t)

	// The proxy's own hostname map selects the denied peer, independently of
	// the client resolver; an IP-keyed route reaches the allowed peer.
	proxy := startForwardingProxySerial(t, map[string]string{
		"allowed.test": denied.loopbackAddr(),
		"203.0.113.10": allowed.loopbackAddr(),
	})
	allowed.resetCount()
	denied.resetCount()
	proxy.resetCounts()

	// Fixture-only control: hostname forwarding through the proxy reaches the
	// denied peer and delivers its marker.
	plainProxyClient := &http.Client{
		Transport: &http.Transport{Proxy: func(*http.Request) (*url.URL, error) {
			return url.Parse(proxy.URL())
		}},
		Timeout: fixtureBoundedSeconds * time.Second,
	}
	resp, err := plainProxyClient.Get("http://allowed.test/fixture-control")
	if err != nil {
		t.Fatalf("fixture hostname-forwarding control failed: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("fixture control body read failed: %v", err)
	}
	if string(body) != pinnedDeniedMarker {
		t.Fatalf("fixture control body = %q, want the proxy's own map to deliver %q from the denied peer", body, pinnedDeniedMarker)
	}
	if denied.count() != 1 || proxy.originCount(denied.loopbackAddr()) != 1 {
		t.Fatalf("fixture control counts denied=%d proxy=%d, want 1/1", denied.count(), proxy.originCount(denied.loopbackAddr()))
	}
	allowed.resetCount()
	denied.resetCount()
	proxy.resetCounts()
	proxy.resetObservations()

	installControlledResolutionSerial(t, map[string][]net.IPAddr{
		"allowed.test": {ipAddr("203.0.113.10")},
		"127.0.0.1":    {ipAddr("127.0.0.1")},
	}, dialSeamMapping{
		"203.0.113.10": allowed.loopbackAddr(),
	})
	client := Client(types.EgressPolicy{
		Allow:         []string{"*.test"},
		Schemes:       []string{"http"},
		PrivateRanges: types.AllowPrivate,
		Timeout:       10 * time.Second,
	}, WithProxy(parseFixtureProxyURL(t, proxy)))

	req, err := http.NewRequest(http.MethodGet, "http://allowed.test:80/pinned", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "allowed.test"
	originalURL := req.URL.String()
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("proxied request to allowed.test failed: %v (want forwarding to the validated destination 203.0.113.10:80 with the original authority)", err)
	}
	body, err = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("proxied response body read failed: %v", err)
	}
	if string(body) != pinnedAllowedMarker {
		t.Fatalf("proxied response body = %q, want the allowed origin's unique marker %q delivered through the validated destination 203.0.113.10:80", body, pinnedAllowedMarker)
	}

	var productObs rawObservation
	productMatches := 0
	for _, o := range proxy.rawObservations() {
		if strings.HasSuffix(o.Target, "/pinned") {
			productObs = o
			productMatches++
		}
	}
	if productMatches != 1 {
		t.Fatalf("proxy captured %d observations of the pinned product request, want exactly one", productMatches)
	}
	if productObs.Target == "" {
		t.Fatalf("no raw request captured for the pinned product request; want an absolute target naming the checked IP 203.0.113.10:80")
	}
	if !strings.Contains(productObs.Target, "203.0.113.10:80") {
		t.Fatalf("raw absolute request target = %q, want the checked destination 203.0.113.10:80 instead of the hostname", productObs.Target)
	}
	if productObs.Host != "allowed.test" {
		t.Fatalf("raw Host header = %q, want the original authority allowed.test", productObs.Host)
	}
	if proxy.originCount(denied.loopbackAddr()) != 0 {
		t.Fatalf("denied origin forwarded requests = %d, want 0 (proxy DNS must not replace the validated destination)", proxy.originCount(denied.loopbackAddr()))
	}
	if proxy.originCount(allowed.loopbackAddr()) != 1 {
		t.Fatalf("allowed origin forwarded requests = %d, want 1", proxy.originCount(allowed.loopbackAddr()))
	}
	if denied.count() != 0 {
		t.Fatalf("denied peer requests = %d, want 0", denied.count())
	}
	if req.URL.String() != originalURL {
		t.Fatalf("caller request URL mutated from %q to %q", originalURL, req.URL.String())
	}
	if req.Host != "allowed.test" {
		t.Fatalf("caller request Host mutated to %q, want allowed.test", req.Host)
	}

	// DenyPrivate proxied-path pin: a proxied request cannot skip destination
	// policy. proxy.test reaches the proxy through the resolution seam, while
	// the target hostname resolves to a CGNAT address that must be refused
	// with zero dials, zero proxy connections, and zero forwarded origins.
	dials := installCountingResolutionSerial(t, map[string][]net.IPAddr{
		"proxy.test":        {ipAddr("203.0.113.50")},
		"cgnat-target.test": {ipAddr("100.64.7.7")},
	}, dialSeamMapping{
		"203.0.113.50": loopbackHost(proxy.server.Listener.Addr().String()),
	})
	proxyPort, err := url.Parse(proxy.URL())
	if err != nil {
		t.Fatalf("fixture proxy URL parse failed: %v", err)
	}
	denyPrivateURL, err := url.Parse("http://proxy.test:" + proxyPort.Port())
	if err != nil {
		t.Fatalf("proxy URL rebuild for proxy.test failed: %v", err)
	}
	denyClient := Client(types.EgressPolicy{
		Allow:         []string{"*.test"},
		Schemes:       []string{"http"},
		PrivateRanges: types.DenyPrivate,
		Timeout:       10 * time.Second,
	}, WithProxy(denyPrivateURL))
	dials.reset()
	allowed.resetCount()
	denied.resetCount()
	proxy.resetCounts()
	proxy.resetObservations()
	_, err = denyClient.Get("http://cgnat-target.test/proxied-refusal")
	if err == nil {
		t.Fatal("proxied request to cgnat-target.test resolving to 100.64.7.7 succeeded, want types.EgressDenied private-range refusal before dispatch")
	}
	var denyRefused types.EgressDenied
	if !errors.As(err, &denyRefused) {
		t.Fatalf("proxied error for cgnat-target.test is %T (%v), want types.EgressDenied", err, err)
	}
	if !strings.Contains(denyRefused.Reason, "private range") {
		t.Fatalf("proxied denial reason for cgnat-target.test = %q, want it to contain %q", denyRefused.Reason, "private range")
	}
	if got := dials.count(); got != 0 {
		t.Fatalf("proxied refusal caused %d dial invocations, want zero dials before refusal", got)
	}
	if got := len(proxy.rawObservations()); got != 0 {
		t.Fatalf("proxied refusal reached the proxy %d times, want zero proxy connections", got)
	}
	if got := proxy.originCount(allowed.loopbackAddr()); got != 0 {
		t.Fatalf("allowed-origin forward attempts = %d, want 0", got)
	}
	if got := proxy.originCount(denied.loopbackAddr()); got != 0 {
		t.Fatalf("denied-origin forward attempts = %d, want 0", got)
	}
	if allowed.count() != 0 || denied.count() != 0 {
		t.Fatalf("proxied refusal caused peer activity allowed=%d denied=%d, want zero", allowed.count(), denied.count())
	}
}

const tlsPinningRun = "^TestNetworkPinningScenarios$/^tools[.]proxy-tls-authority-preserved$"

func testProxyTLSAuthorityPreserved(t *testing.T) {
	if !inTLSFixtureSubprocess() {
		bundle := newTLSFixtureBundle(t, tlsWrongIdentityName, tlsOriginName)
		launchTLSFixtureSubprocessSerial(t, bundle, tlsPinningRun)
		return
	}

	certDir := filepath.Dir(os.Getenv("SSL_CERT_FILE"))
	origin := startTLSFixturePeerSerial(t, certDir, "origin", tlsAllowedMarker)
	wrongOrigin := startTLSFixturePeerSerial(t, certDir, "proxy", "wrong-identity-marker-v9")
	denied := startControlledPeerSerial(t, tlsDeniedMarker)
	origin.checkReady(t, tlsOriginName)
	wrongOrigin.checkReady(t, tlsWrongIdentityName)
	denied.checkReady(t)
	origin.refuseUnready(t)
	wrongOrigin.refuseUnready(t)
	denied.refuseUnready(t)

	// The proxy's own hostname map selects the denied plain peer; IP-keyed
	// routes reach the TLS origins.
	proxy := startForwardingProxySerial(t, map[string]string{
		"allowed.test":  denied.loopbackAddr(),
		"allowed2.test": denied.loopbackAddr(),
		"203.0.113.20":  origin.loopbackAddr(),
		"203.0.113.21":  wrongOrigin.loopbackAddr(),
	})
	origin.resetCount()
	wrongOrigin.resetCount()
	denied.resetCount()
	proxy.resetCounts()
	origin.resetObservations()
	wrongOrigin.resetObservations()

	// Fixture-only control: hostname forwarding reaches the denied peer.
	plainProxyClient := &http.Client{
		Transport: &http.Transport{Proxy: func(*http.Request) (*url.URL, error) {
			return url.Parse(proxy.URL())
		}},
		Timeout: fixtureBoundedSeconds * time.Second,
	}
	resp, err := plainProxyClient.Get("http://allowed.test/fixture-control")
	if err != nil {
		t.Fatalf("fixture hostname-forwarding control failed: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("fixture control body read failed: %v", err)
	}
	if string(body) != tlsDeniedMarker {
		t.Fatalf("fixture control body = %q, want the proxy's own map to deliver %q from the denied peer", body, tlsDeniedMarker)
	}
	origin.resetCount()
	wrongOrigin.resetCount()
	denied.resetCount()
	proxy.resetCounts()
	proxy.resetObservations()
	origin.resetObservations()
	wrongOrigin.resetObservations()

	installControlledResolutionSerial(t, map[string][]net.IPAddr{
		"allowed.test":  {ipAddr("203.0.113.20")},
		"allowed2.test": {ipAddr("203.0.113.21")},
		"127.0.0.1":     {ipAddr("127.0.0.1")},
	}, dialSeamMapping{
		"203.0.113.20": origin.loopbackAddr(),
		"203.0.113.21": wrongOrigin.loopbackAddr(),
	})
	client := Client(types.EgressPolicy{
		Allow:         []string{"*.test"},
		Schemes:       []string{"https"},
		PrivateRanges: types.AllowPrivate,
		Timeout:       10 * time.Second,
	}, WithProxy(parseFixtureProxyURL(t, proxy)))

	resp, err = client.Get("https://allowed.test/tls-pinned")
	if err != nil {
		t.Fatalf("pinned HTTPS request through the proxy failed: %v (proxy-observed CONNECT authorities: %q; want CONNECT to the checked destination 203.0.113.20:443 with original-host certificate verification)", err, authorityTargets(proxy.rawObservations()))
	}
	body, err = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("pinned HTTPS body read failed: %v", err)
	}
	connects := authorityTargets(proxy.rawObservations())
	if string(body) != tlsAllowedMarker {
		t.Fatalf("pinned HTTPS body = %q (proxy-observed CONNECT authorities: %q), want the allowed origin's unique marker %q delivered through the pinned destination", body, connects, tlsAllowedMarker)
	}
	if resp.TLS == nil || len(resp.TLS.PeerCertificates) == 0 {
		t.Fatalf("no TLS connection state on the pinned response (proxy-observed CONNECT authorities: %q); want end-to-end TLS against the original host certificate", connects)
	}
	certNames := resp.TLS.PeerCertificates[0].DNSNames
	if !containsString(certNames, tlsOriginName) || containsString(certNames, tlsWrongIdentityName) {
		t.Fatalf("destination certificate DNS names = %v (proxy-observed CONNECT authorities: %q), want the origin's own distinct identity %s and not the proxy name %s", certNames, connects, tlsOriginName, tlsWrongIdentityName)
	}

	if len(connects) != 1 {
		t.Fatalf("proxy observed %d CONNECT-style requests %q, want exactly one", len(connects), connects)
	}
	connectTarget := connects[0]
	if host, port, err := net.SplitHostPort(connectTarget); err != nil || host != "203.0.113.20" || port != "443" {
		t.Fatalf("CONNECT authority = %q, want the checked destination 203.0.113.20:443 instead of the hostname", connectTarget)
	}

	originHosts := origin.hostsSeen()
	originSNIs := origin.snisSeen()
	if len(originHosts) != 1 || originHosts[0] != tlsOriginName {
		t.Fatalf("origin observed %d requests with Hosts %q; want exactly one product-side observation of the original authority %s", len(originHosts), originHosts, tlsOriginName)
	}
	if len(originSNIs) != 1 || originSNIs[0] != tlsOriginName {
		t.Fatalf("origin observed %d TLS server names %q; want exactly one product-side observation of %s", len(originSNIs), originSNIs, tlsOriginName)
	}
	if origin.count() != 1 {
		t.Fatalf("allowed origin requests = %d, want 1", origin.count())
	}
	if denied.count() != 0 {
		t.Fatalf("denied origin requests = %d, want 0 product requests", denied.count())
	}

	// A destination presenting the proxy's distinct certificate name must
	// fail verification before any HTTP request completes.
	_, err = client.Get("https://allowed2.test/tls-wrong")
	if err == nil {
		t.Fatal("request against the wrong-identity destination certificate succeeded, want hostname verification failure")
	}
	var hostnameErr x509.HostnameError
	if !errors.As(err, &hostnameErr) || !strings.Contains(err.Error(), tlsWrongIdentityName) {
		t.Fatalf("wrong-identity error = %v, want x509 hostname verification failure naming %s", err, tlsWrongIdentityName)
	}
	if wrongOrigin.count() != 0 {
		t.Fatalf("wrong-identity origin requests = %d, want 0 completed HTTP requests", wrongOrigin.count())
	}
}

// tlsFixturePeer is a TLS origin with the fixture bundle's own certificate,
// a unique response marker, per-request counting, and request observation.
// Its requests arrive origin-form over a CONNECT tunnel, so r.Host and
// r.TLS.ServerName carry the wire Host header and TLS server name without
// absolute-URI normalization; the listener stays unwrapped because the raw
// head observer cannot pass TLS handshake bytes through.
type tlsFixturePeer struct {
	server   *httptest.Server
	marker   string
	requests atomic.Int32
	mu       sync.Mutex
	hosts    []string
	snis     []string
	ready    atomic.Bool
}

func startTLSFixturePeerSerial(t *testing.T, certDir, kind, marker string) *tlsFixturePeer {
	t.Helper()
	cert, err := tls.LoadX509KeyPair(filepath.Join(certDir, kind+".crt"), filepath.Join(certDir, kind+".key"))
	if err != nil {
		t.Fatalf("fixture %s certificate load failed: %v", kind, err)
	}
	p := &tlsFixturePeer{marker: marker}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.requests.Add(1)
		sni := ""
		if r.TLS != nil {
			sni = r.TLS.ServerName
		}
		p.mu.Lock()
		p.hosts = append(p.hosts, r.Host)
		p.snis = append(p.snis, sni)
		p.mu.Unlock()
		_, _ = io.WriteString(w, p.marker)
	})
	p.server = httptest.NewUnstartedServer(handler)
	// This Go version's httptest Start ignores s.TLS; wrap the listener so
	// Start serves TLS while the handler keeps plain net/http semantics.
	p.server.Listener = tls.NewListener(p.server.Listener, &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	})
	p.server.Start()
	t.Cleanup(func() { closeBounded(t, p.server, "tls fixture peer") })
	return p
}

func (p *tlsFixturePeer) loopbackAddr() string {
	return p.server.Listener.Addr().String()
}

func (p *tlsFixturePeer) count() int32 {
	return p.requests.Load()
}

func (p *tlsFixturePeer) resetCount() {
	p.requests.Store(0)
}

// checkReady performs a direct TLS health check through the default trust
// (SSL_CERT_FILE), dialing the peer directly before any product use.
func (p *tlsFixturePeer) checkReady(t *testing.T, verifyName string) {
	t.Helper()
	client := &http.Client{
		Transport: &http.Transport{DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, p.loopbackAddr())
		}},
		Timeout: fixtureBoundedSeconds * time.Second,
	}
	resp, err := client.Get("https://" + verifyName + "/ready")
	if err != nil {
		t.Fatalf("tls fixture peer %s readiness check failed: %v", verifyName, err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("tls fixture peer readiness body read failed: %v", err)
	}
	if string(body) != p.marker {
		t.Fatalf("tls fixture peer marker = %q, want %q", body, p.marker)
	}
	p.ready.Store(true)
}

func (p *tlsFixturePeer) refuseUnready(t *testing.T) {
	t.Helper()
	if !p.ready.Load() {
		t.Fatal("tls fixture peer refused: readiness check not established")
	}
}

func (p *tlsFixturePeer) hostsSeen() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.hosts...)
}

func (p *tlsFixturePeer) snisSeen() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.snis...)
}

// resetObservations clears recorded Host and SNI observations alongside
// resetCount so readiness traffic cannot satisfy product assertions.
func (p *tlsFixturePeer) resetObservations() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.hosts = nil
	p.snis = nil
}

// authorityTargets returns the observed CONNECT-style targets: observations
// whose request target is neither an absolute http nor https URL.
func authorityTargets(obs []rawObservation) []string {
	var targets []string
	for _, o := range obs {
		if !strings.HasPrefix(o.Target, "http://") && !strings.HasPrefix(o.Target, "https://") {
			targets = append(targets, o.Target)
		}
	}
	return targets
}

func parseFixtureProxyURL(t *testing.T, fp *forwardingProxy) *url.URL {
	t.Helper()
	u, err := url.Parse(fp.URL())
	if err != nil {
		t.Fatalf("fixture proxy URL parse failed: %v", err)
	}
	return u
}

func ipAddr(literal string) net.IPAddr {
	return net.IPAddr{IP: net.ParseIP(literal)}
}

func containsString(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}
