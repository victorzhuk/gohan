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
	_, err := Client(p, WithProxy(proxyURL)).Get("http://target.test/")
	if err != nil {
		t.Fatal(err)
	}
	if dialed != net.JoinHostPort(host, port) {
		t.Fatalf("dialed %q, expected proxy %s", dialed, net.JoinHostPort(host, port))
	}
}

func TestEgressUnsupportedProxyRefused(t *testing.T) {
	resetNetworkFuncs(t)
	proxy, _ := url.Parse("socks5://proxy.test:1080")
	resolveHost = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("203.0.113.4")}}, nil
	}
	_, err := Client(policy("target.test"), WithProxy(proxy)).Get("http://target.test/")
	assertDenied(t, err, "proxy scheme not supported")
	if !errors.Is(err, ErrEgressProxyUnsupported) {
		t.Fatalf("error %v does not name ErrEgressProxyUnsupported", err)
	}
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
