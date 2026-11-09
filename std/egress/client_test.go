package egress

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

func TestEgressMetadataRefused(t *testing.T) {
	resetNetworkFuncs(t)
	dials := 0
	resolveHost = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("169.254.169.254")}}, nil
	}
	dialAddress = func(context.Context, string, string) (net.Conn, error) {
		dials++
		return nil, errors.New("unexpected dial")
	}
	client := Client(policy("other.test"))
	_, err := client.Get("http://169.254.169.254/latest/meta-data/")
	assertDenied(t, err, "link-local")
	if dials != 0 {
		t.Fatalf("dial count = %d", dials)
	}
}

func TestEgressRedirectPrivateRefused(t *testing.T) {
	resetNetworkFuncs(t)
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "http://10.0.0.5/secret")
		w.WriteHeader(http.StatusFound)
		_, _ = io.WriteString(w, "first body secret")
	}))
	defer first.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(first.URL, "http://"))
	resolveHost = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("203.0.113.20")}}, nil
	}
	dialAddress = func(ctx context.Context, network, address string) (net.Conn, error) {
		_, p, _ := net.SplitHostPort(address)
		return (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort("127.0.0.1", p))
	}
	client := Client(policy("first.test:" + port))
	reqURL := "http://first.test:" + port + "/"
	resp, err := client.Get(reqURL)
	assertDenied(t, err, "private range")
	if resp != nil && resp.Body != nil {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if strings.Contains(string(body), "first body") {
			t.Fatal("redirect body returned")
		}
	}
}

func TestEgressRebindingSafe(t *testing.T) {
	t.Run("tools.egress-rebinding-safe", func(t *testing.T) {
		resetNetworkFuncs(t)
		calls := 0
		dialed := ""
		resolveHost = func(context.Context, string) ([]net.IPAddr, error) {
			calls++
			if calls == 1 {
				return []net.IPAddr{{IP: net.ParseIP("203.0.113.9")}}, nil
			}
			return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
		}
		dialAddress = func(_ context.Context, _, address string) (net.Conn, error) {
			dialed = address
			return nil, errors.New("stop")
		}
		_, _ = Client(policy("rebind.test")).Get("http://rebind.test/")
		if calls != 1 {
			t.Fatalf("resolution calls = %d", calls)
		}
		if !strings.HasPrefix(dialed, "203.0.113.9:") {
			t.Fatalf("dialed %q", dialed)
		}
	})
}

func TestEgressSizeLimit(t *testing.T) {
	resetNetworkFuncs(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "123456789") }))
	defer server.Close()
	host, port, _ := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	resolveHost = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	}
	p := types.EgressPolicy{Allow: []string{host + ":" + port}, Schemes: []string{"http"}, PrivateRanges: types.AllowPrivate, MaxBytes: 4}
	resp, err := Client(p).Get(server.URL)
	if resp != nil {
		_ = resp.Body.Close()
	}
	assertDenied(t, err, "size limit 4")
}

type countingReader struct {
	reader *strings.Reader
	read   int
}

func (r *countingReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.read += n
	return n, err
}

func TestEgressLimitedBodyCapsUnderlyingRead(t *testing.T) {
	const limit = 64
	source := &countingReader{reader: strings.NewReader(strings.Repeat("x", 100))}
	body := &limitedBody{ReadCloser: io.NopCloser(source), limit: limit, deny: func() error {
		return types.EgressDenied{Reason: "size limit 64"}
	}}
	got, err := io.ReadAll(body)
	var denied types.EgressDenied
	if len(got) > limit {
		t.Fatalf("caller received %d bytes, want at most %d", len(got), limit)
	}
	if !errors.As(err, &denied) || !strings.Contains(denied.Reason, "64") {
		t.Fatalf("error = %v, want EgressDenied naming limit %d", err, limit)
	}
	if source.read > limit+1 {
		t.Fatalf("underlying reader returned %d bytes, want at most %d", source.read, limit+1)
	}
}

func TestEgressDisallowedSchemeDoesNotDial(t *testing.T) {
	resetNetworkFuncs(t)
	dials := 0
	dialAddress = func(context.Context, string, string) (net.Conn, error) { dials++; return nil, nil }
	_, err := Client(policy("example.test")).Get("https://example.test/")
	assertDenied(t, err, "scheme not allowed")
	if dials != 0 {
		t.Fatalf("dial count = %d", dials)
	}
}
func TestEgressTimeout(t *testing.T) {
	resetNetworkFuncs(t)
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { time.Sleep(100 * time.Millisecond) }))
	defer server.Close()
	host, port, _ := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	resolveHost = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	}
	p := types.EgressPolicy{Allow: []string{host + ":" + port}, Schemes: []string{"http"}, PrivateRanges: types.AllowPrivate, Timeout: 10 * time.Millisecond}
	_, err := Client(p).Get(server.URL)
	assertDenied(t, err, "timeout")
}

func TestEgressCrossHostRedirectStripsAuthorization(t *testing.T) {
	resetNetworkFuncs(t)
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("authorization header forwarded")
		}
	}))
	defer destination.Close()
	_, destPort, _ := net.SplitHostPort(strings.TrimPrefix(destination.URL, "http://"))
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://destination.test:"+destPort+"/", http.StatusFound)
	}))
	defer origin.Close()
	_, originPort, _ := net.SplitHostPort(strings.TrimPrefix(origin.URL, "http://"))
	resolveHost = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("203.0.113.7")}}, nil
	}
	dialAddress = func(ctx context.Context, network, address string) (net.Conn, error) {
		_, port, _ := net.SplitHostPort(address)
		return (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort("127.0.0.1", port))
	}
	p := types.EgressPolicy{Allow: []string{"origin.test:" + originPort, "destination.test:" + destPort}, Schemes: []string{"http"}}
	req, _ := http.NewRequest(http.MethodGet, "http://origin.test:"+originPort+"/", nil)
	req.Header.Set("Authorization", "secret")
	resp, err := Client(p).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
}

func TestEgressHTTPProxyChecked(t *testing.T) {
	resetNetworkFuncs(t)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") }))
	defer proxy.Close()
	proxyURL, _ := url.Parse(proxy.URL)
	host, port, _ := net.SplitHostPort(strings.TrimPrefix(proxy.URL, "http://"))
	dialed := ""
	resolveHost = func(_ context.Context, name string) ([]net.IPAddr, error) {
		if name == "target.test" {
			return []net.IPAddr{{IP: net.ParseIP("203.0.113.4")}}, nil
		}
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	}
	dialAddress = func(ctx context.Context, network, address string) (net.Conn, error) {
		dialed = address
		_, p, _ := net.SplitHostPort(address)
		return (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort("127.0.0.1", p))
	}
	p := types.EgressPolicy{Allow: []string{"target.test"}, Schemes: []string{"http"}, PrivateRanges: types.AllowPrivate}
	resp, err := Client(p, WithProxy(proxyURL)).Get("http://target.test/")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if dialed != net.JoinHostPort(host, port) {
		t.Fatalf("dialed %q, expected proxy %s", dialed, net.JoinHostPort(host, port))
	}
}

func TestEgressUnsupportedProxyRefused(t *testing.T) {
	resetNetworkFuncs(t)
	dials := 0
	dialAddress = func(context.Context, string, string) (net.Conn, error) {
		dials++
		return nil, errors.New("unexpected dial")
	}
	proxy, _ := url.Parse("socks5://proxy.test:1080")
	resolveHost = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("203.0.113.4")}}, nil
	}
	_, err := Client(policy("target.test"), WithProxy(proxy)).Get("http://target.test/")
	assertDenied(t, err, "proxy scheme not supported")
	if !errors.Is(err, ErrEgressProxyUnsupported) {
		t.Fatalf("error %v does not name ErrEgressProxyUnsupported", err)
	}
	if dials != 0 {
		t.Fatalf("dial count = %d, want zero dispatch before proxy-scheme refusal", dials)
	}
}

func TestEgressProxyAddressDenied(t *testing.T) {
	resetNetworkFuncs(t)
	dials := 0
	dialAddress = func(context.Context, string, string) (net.Conn, error) {
		dials++
		return nil, errors.New("unexpected dial")
	}
	resolveHost = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("169.254.169.254")}}, nil
	}
	proxy, _ := url.Parse("http://proxy.test:8080")
	p := types.EgressPolicy{Allow: []string{"target.test"}, Schemes: []string{"http"}}
	_, err := Client(p, WithProxy(proxy)).Get("http://target.test/")
	assertDenied(t, err, "link-local")
	if dials != 0 {
		t.Fatalf("dial count = %d, want zero dispatch on proxy-address denial", dials)
	}
}

// TestEgressCGNATPolicy classifies CGNAT (100.64.0.0/10) destination-policy
// behavior through the public Client.Get surface with an explicit
// types.EgressPolicy. Serial globals: no t.Parallel while resolveHost and
// dialAddress are swapped.
func TestEgressCGNATPolicy(t *testing.T) {
	resetNetworkFuncs(t)

	t.Run("refusals", func(t *testing.T) { fillRefusals(t) })
	t.Run("peer-reachability", func(t *testing.T) { fillPeerReachability(t) })
	t.Run("characterization", func(t *testing.T) { fillCharacterization(t) })
}

func fillRefusals(t *testing.T) {
	const host = "cgnat.test"
	// Each case must refuse before any dial: the dial stub counts and fails.
	cases := []struct {
		name  string
		allow []string
		url   string
		dns   map[string][]net.IPAddr
		want  string
	}{
		{
			name: "cgnat-first-address-100.64.0.0",
			url:  "http://" + host + "/",
			dns:  map[string][]net.IPAddr{host: {net.IPAddr{IP: net.ParseIP("100.64.0.0")}}},
			want: "private range",
		},
		{
			name: "cgnat-last-address-100.127.255.255",
			url:  "http://" + host + "/",
			dns:  map[string][]net.IPAddr{host: {net.IPAddr{IP: net.ParseIP("100.127.255.255")}}},
			want: "private range",
		},
		{
			name: "cgnat-interior-shared-address",
			url:  "http://" + host + "/",
			dns:  map[string][]net.IPAddr{host: {net.IPAddr{IP: net.ParseIP("100.70.1.2")}}},
			want: "private range",
		},
		{
			name: "ipv4-mapped-cgnat-answer",
			url:  "http://" + host + "/",
			dns:  map[string][]net.IPAddr{host: {net.IPAddr{IP: net.ParseIP("::ffff:100.64.0.1")}}},
			want: "private range",
		},
		{
			name: "mixed-answer-public-first",
			url:  "http://" + host + "/",
			dns: map[string][]net.IPAddr{host: {
				{IP: net.ParseIP("203.0.113.50")}, {IP: net.ParseIP("100.64.0.1")},
			}},
			want: "private range",
		},
		{
			name: "mixed-answer-cgnat-first",
			url:  "http://" + host + "/",
			dns: map[string][]net.IPAddr{host: {
				{IP: net.ParseIP("100.64.0.1")}, {IP: net.ParseIP("203.0.113.50")},
			}},
			want: "private range",
		},
		{
			name:  "literal-cgnat-wildcard-allow-only",
			allow: []string{"*"},
			url:   "http://100.64.9.9/",
			dns:   map[string][]net.IPAddr{"100.64.9.9": {net.IPAddr{IP: net.ParseIP("100.64.9.9")}}},
			want:  "private range",
		},
		{
			name:  "literal-cgnat-exact-entry-wrong-port",
			allow: []string{"100.64.9.9:9980"},
			url:   "http://100.64.9.9:9981/",
			want:  "private range",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dials := 0
			resolveHost = func(_ context.Context, name string) ([]net.IPAddr, error) {
				if addrs, ok := tc.dns[name]; ok {
					return addrs, nil
				}
				return nil, &net.DNSError{Name: name, Err: "no such host", IsNotFound: true}
			}
			dialAddress = func(context.Context, string, string) (net.Conn, error) {
				dials++
				return nil, errors.New("unexpected dial")
			}
			allow := tc.allow
			if allow == nil {
				allow = []string{host}
			}
			p := types.EgressPolicy{Allow: allow, Schemes: []string{"http"}, PrivateRanges: types.DenyPrivate}
			_, err := Client(p).Get(tc.url)
			assertDenied(t, err, tc.want)
			if dials != 0 {
				t.Fatalf("dial count = %d, want 0", dials)
			}
		})
	}
}

func fillPeerReachability(t *testing.T) {
	t.Run("neighbor-lower-100.63.255.255-allowed", func(t *testing.T) {
		peer := startControlledPeerSerial(t, "neighbor-low-ok")
		peer.checkReady(t)
		installControlledResolutionSerial(t,
			map[string][]net.IPAddr{"neighbor.test": {net.IPAddr{IP: net.ParseIP("100.63.255.255")}}},
			dialSeamMapping{"100.63.255.255": peer.loopbackAddr()})
		resp, err := Client(policy("neighbor.test")).Get("http://neighbor.test/")
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != "neighbor-low-ok" {
			t.Fatalf("peer body = %q, want marker", body)
		}
		if peer.count() == 0 {
			t.Fatal("controlled peer request count = 0")
		}
	})

	t.Run("neighbor-upper-100.128.0.0-allowed", func(t *testing.T) {
		peer := startControlledPeerSerial(t, "neighbor-high-ok")
		peer.checkReady(t)
		installControlledResolutionSerial(t,
			map[string][]net.IPAddr{"neighbor.test": {net.IPAddr{IP: net.ParseIP("100.128.0.0")}}},
			dialSeamMapping{"100.128.0.0": peer.loopbackAddr()})
		resp, err := Client(policy("neighbor.test")).Get("http://neighbor.test/")
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != "neighbor-high-ok" {
			t.Fatalf("peer body = %q, want marker", body)
		}
		if peer.count() == 0 {
			t.Fatal("controlled peer request count = 0")
		}
	})

	t.Run("literal-cgnat-exact-allow-matching-port", func(t *testing.T) {
		peer := startControlledPeerSerial(t, "literal-cgnat-ok")
		peer.checkReady(t)
		installControlledResolutionSerial(t,
			map[string][]net.IPAddr{"100.64.9.9": {net.IPAddr{IP: net.ParseIP("100.64.9.9")}}},
			dialSeamMapping{"100.64.9.9": peer.loopbackAddr()})
		p := types.EgressPolicy{
			Allow:         []string{"100.64.9.9:9981"},
			Schemes:       []string{"http"},
			PrivateRanges: types.DenyPrivate,
		}
		resp, err := Client(p).Get("http://100.64.9.9:9981/")
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != "literal-cgnat-ok" {
			t.Fatalf("peer body = %q, want marker", body)
		}
		if peer.count() == 0 {
			t.Fatal("controlled peer request count = 0")
		}
	})
}

func fillCharacterization(t *testing.T) {
	t.Run("cgnat-hostname-under-AllowPrivate-passes", func(t *testing.T) {
		// No new refusal: the existing AllowPrivate branch lets the request
		// reach the dial; characterize the dial attempt, not a denial.
		dials := 0
		resolveHost = func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.ParseIP("100.70.3.4")}}, nil
		}
		dialAddress = func(context.Context, string, string) (net.Conn, error) {
			dials++
			return nil, errors.New("dial attempted")
		}
		p := types.EgressPolicy{Allow: []string{"cgnat.test"}, Schemes: []string{"http"}, PrivateRanges: types.AllowPrivate}
		_, err := Client(p).Get("http://cgnat.test/")
		var denied types.EgressDenied
		if errors.As(err, &denied) {
			t.Fatalf("AllowPrivate introduced EgressDenied: %v", denied)
		}
		if dials != 1 {
			t.Fatalf("dial count = %d, want 1", dials)
		}
	})

	for _, tc := range []struct {
		name string
		ip   string
		want string
	}{
		{"loopback-denied", "127.0.0.1", "loopback"},
		{"rfc1918-denied", "10.1.2.3", "private range"},
		{"ipv6-ula-denied", "fd00::1", "private range"},
		{"link-local-denied", "169.254.1.1", "link-local"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetNetworkFuncs(t)
			dials := 0
			resolveHost = func(context.Context, string) ([]net.IPAddr, error) {
				return []net.IPAddr{{IP: net.ParseIP(tc.ip)}}, nil
			}
			dialAddress = func(context.Context, string, string) (net.Conn, error) {
				dials++
				return nil, errors.New("unexpected dial")
			}
			_, err := Client(policy("priv.test")).Get("http://priv.test/")
			assertDenied(t, err, tc.want)
			if dials != 0 {
				t.Fatalf("dial count = %d, want 0", dials)
			}
		})
	}

	t.Run("link-local-under-AllowPrivate-still-denied", func(t *testing.T) {
		dials := 0
		resolveHost = func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.ParseIP("169.254.1.1")}}, nil
		}
		dialAddress = func(context.Context, string, string) (net.Conn, error) {
			dials++
			return nil, errors.New("unexpected dial")
		}
		p := types.EgressPolicy{Allow: []string{"priv.test"}, Schemes: []string{"http"}, PrivateRanges: types.AllowPrivate}
		_, err := Client(p).Get("http://priv.test/")
		assertDenied(t, err, "link-local")
		if dials != 0 {
			t.Fatalf("dial count = %d, want 0", dials)
		}
	})

	t.Run("empty-and-failed-resolution-host-not-allowed", func(t *testing.T) {
		dials := 0
		dialAddress = func(context.Context, string, string) (net.Conn, error) {
			dials++
			return nil, errors.New("unexpected dial")
		}
		resolveHost = func(context.Context, string) ([]net.IPAddr, error) {
			return nil, nil
		}
		_, err := Client(policy("empty.test")).Get("http://empty.test/")
		assertDenied(t, err, "host not allowed")

		resolveHost = func(context.Context, string) ([]net.IPAddr, error) {
			return nil, &net.DNSError{Name: "failed.test", Err: "resolver failure", IsTemporary: true}
		}
		_, err = Client(policy("failed.test")).Get("http://failed.test/")
		assertDenied(t, err, "host not allowed")
		if dials != 0 {
			t.Fatalf("dial count = %d, want 0", dials)
		}
	})
}

func resetNetworkFuncs(t *testing.T) {
	oldResolve, oldDial := resolveHost, dialAddress
	t.Cleanup(func() {
		resolveHost = oldResolve
		dialAddress = oldDial
	})
}

func policy(host string) types.EgressPolicy {
	return types.EgressPolicy{Allow: []string{host}, Schemes: []string{"http"}}
}

func assertDenied(t *testing.T, err error, reason string) {
	t.Helper()
	var denied types.EgressDenied
	if !errors.As(err, &denied) {
		t.Fatalf("error %T %v is not EgressDenied", err, err)
	}
	if denied.Reason != reason {
		t.Fatalf("reason = %q, want %q", denied.Reason, reason)
	}
}
