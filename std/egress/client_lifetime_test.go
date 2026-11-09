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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

type connTracker struct {
	closed int32
}

func (c *connTracker) state(_ net.Conn, s http.ConnState) {
	if s == http.StateClosed {
		atomic.AddInt32(&c.closed, 1)
	}
}

func (c *connTracker) waitClosed(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt32(&c.closed) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if atomic.LoadInt32(&c.closed) == 0 {
		t.Fatal("no server connection closed; transport still held after release point")
	}
}

func stallServer(t *testing.T, tracker *connTracker, body string) (*httptest.Server, func()) {
	t.Helper()
	hold := make(chan struct{})
	var holdOnce sync.Once
	closeHold := func() { holdOnce.Do(func() { close(hold) }) }
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, body)
		w.(http.Flusher).Flush()
		<-hold
	}))
	server.Config.ConnState = tracker.state
	server.Start()
	t.Cleanup(func() {
		closeHold()
		server.Close()
	})
	return server, closeHold
}

func localPolicy(host, port string, timeout time.Duration) types.EgressPolicy {
	return types.EgressPolicy{
		Allow:         []string{host + ":" + port},
		Schemes:       []string{"http"},
		PrivateRanges: types.AllowPrivate,
		Timeout:       timeout,
	}
}

func loopbackResolve(host string) func(context.Context, string) ([]net.IPAddr, error) {
	return func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP(host)}}, nil
	}
}

func redirectDial(tracked *string) func(ctx context.Context, network, address string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		if tracked != nil {
			*tracked = address
		}
		_, port, _ := net.SplitHostPort(address)
		return (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort("127.0.0.1", port))
	}
}

func splitServer(server *httptest.Server) (string, string) {
	host, port, _ := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	return host, port
}

func TestEgressBodyReadBounded(t *testing.T) {
	t.Run("tools.egress-body-read-bounded", func(t *testing.T) {
		resetNetworkFuncs(t)
		tracker := &connTracker{}
		server, closeHold := stallServer(t, tracker, "abc")
		host, port := splitServer(server)
		resolveHost = loopbackResolve("127.0.0.1")
		dialAddress = redirectDial(nil)
		client := Client(localPolicy(host, port, 60*time.Millisecond))
		resp, err := client.Get(server.URL)
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		var got []byte
		go func() {
			body, err := io.ReadAll(resp.Body)
			got = body
			done <- err
		}()
		select {
		case err := <-done:
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("read error = %v, want deadline exceeded", err)
			}
			if string(got) != "abc" {
				t.Fatalf("read bytes = %q, want %q", got, "abc")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("body read hung past the policy time limit")
		}
		closeHold()
		tracker.waitClosed(t)
		if err := resp.Body.Close(); err != nil {
			t.Fatalf("close after deadline: %v", err)
		}
	})
}

func TestEgressBodyDeadline(t *testing.T) {
	resetNetworkFuncs(t)
	tracker := &connTracker{}
	server, closeHold := stallServer(t, tracker, "abc")
	host, port := splitServer(server)
	resolveHost = loopbackResolve("127.0.0.1")
	dialAddress = redirectDial(nil)
	client := Client(localPolicy(host, port, 60*time.Millisecond))
	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	var got []byte
	var readErr error
	buf := make([]byte, 16)
	for readErr == nil {
		n, err := resp.Body.Read(buf)
		got = append(got, buf[:n]...)
		readErr = err
	}
	if !errors.Is(readErr, context.DeadlineExceeded) {
		t.Fatalf("read error = %v, want deadline exceeded", readErr)
	}
	if string(got) != "abc" {
		t.Fatalf("read bytes = %q, want %q", got, "abc")
	}
	closeHold()
	tracker.waitClosed(t)
	if err := resp.Body.Close(); err != nil {
		t.Fatalf("close after deadline: %v", err)
	}
	if _, err := resp.Body.Read(buf); !errors.Is(err, io.EOF) {
		t.Fatalf("read after close = %v, want io.EOF", err)
	}
}

func TestEgressEarlyErrorCleanup(t *testing.T) {
	t.Run("tools.egress-early-error-cleanup", func(t *testing.T) {
		resetNetworkFuncs(t)
		dials := 0
		dialAddress = func(context.Context, string, string) (net.Conn, error) {
			dials++
			return nil, errors.New("unexpected dial")
		}
		resolveHost = loopbackResolve("169.254.169.254")
		_, err := Client(policy("metadata.test")).Get("http://metadata.test/")
		assertDenied(t, err, "link-local")
		if dials != 0 {
			t.Fatalf("dial count = %d", dials)
		}

		resolveHost = loopbackResolve("203.0.113.30")
		dialAddress = func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("dial refused")
		}
		_, err = Client(policy("unreachable.test")).Get("http://unreachable.test/")
		if err == nil {
			t.Fatal("expected transport error from refused dial")
		}

		proxy, _ := url.Parse("socks5://proxy.test:1080")
		_, err = Client(policy("target.test"), WithProxy(proxy)).Get("http://target.test/")
		assertDenied(t, err, "proxy scheme not supported")
		if !errors.Is(err, ErrEgressProxyUnsupported) {
			t.Fatalf("error %v does not name ErrEgressProxyUnsupported", err)
		}
	})
}

func TestEgressOversizeCleanup(t *testing.T) {
	t.Run("tools.egress-oversize-cleanup", func(t *testing.T) {
		resetNetworkFuncs(t)
		tracker := &connTracker{}
		server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Length", "10")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "1234567890")
		}))
		server.Config.ConnState = tracker.state
		server.Start()
		t.Cleanup(server.Close)
		host, port := splitServer(server)
		resolveHost = loopbackResolve("127.0.0.1")
		dialAddress = redirectDial(nil)
		p := localPolicy(host, port, 0)
		p.MaxBytes = 4
		_, err := Client(p).Get(server.URL)
		assertDenied(t, err, "size limit 4")
		tracker.waitClosed(t)
	})
}

func TestEgressBodyCloseIdempotent(t *testing.T) {
	t.Run("tools.egress-close-idempotent", func(t *testing.T) {
		var releases int
		var mu sync.Mutex
		body := &limitedBody{
			ReadCloser: io.NopCloser(strings.NewReader("hello")),
			limit:      64,
			release: func() {
				mu.Lock()
				releases++
				mu.Unlock()
			},
		}
		got, err := io.ReadAll(body)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "hello" {
			t.Fatalf("read %q", got)
		}
		for range 2 {
			if err := body.Close(); err != nil {
				t.Fatalf("close: %v", err)
			}
		}
		mu.Lock()
		defer mu.Unlock()
		if releases != 1 {
			t.Fatalf("release count = %d, want 1", releases)
		}

		resetNetworkFuncs(t)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "ok")
		}))
		defer server.Close()
		host, port := splitServer(server)
		resolveHost = loopbackResolve("127.0.0.1")
		dialAddress = redirectDial(nil)
		resp, err := Client(localPolicy(host, port, 0)).Get(server.URL)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.ReadAll(resp.Body); err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		_ = resp.Body.Close()
	})
}

func TestEgressCleanupPreservesChecks(t *testing.T) {
	t.Run("tools.egress-cleanup-preserves-checks", func(t *testing.T) {
		resetNetworkFuncs(t)
		tracker := &connTracker{}
		server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "ok")
		}))
		server.Config.ConnState = tracker.state
		server.Start()
		defer server.Close()
		_, port := splitServer(server)
		var resolutions int
		resolveHost = func(context.Context, string) ([]net.IPAddr, error) {
			resolutions++
			return []net.IPAddr{{IP: net.ParseIP("203.0.113.9")}}, nil
		}
		dialed := ""
		dialAddress = redirectDial(&dialed)
		client := Client(types.EgressPolicy{
			Allow:         []string{"checked.test:" + port},
			Schemes:       []string{"http"},
			PrivateRanges: types.AllowPrivate,
		})
		reqURL := "http://checked.test:" + port + "/"
		resp, err := client.Get(reqURL)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.ReadAll(resp.Body); err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resolutions != 1 {
			t.Fatalf("resolution calls = %d, want 1 (no re-resolution within a request)", resolutions)
		}
		if !strings.HasPrefix(dialed, "203.0.113.9:") {
			t.Fatalf("dialed %q, want checked address 203.0.113.9", dialed)
		}
		tracker.waitClosed(t)

		resp, err = client.Get(reqURL)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.ReadAll(resp.Body); err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resolutions != 2 {
			t.Fatalf("resolution calls = %d, want 2 (no cached resolution after release)", resolutions)
		}
		if !strings.HasPrefix(dialed, "203.0.113.9:") {
			t.Fatalf("second request dialed %q, want checked address 203.0.113.9", dialed)
		}
	})
}

func TestEgressPerRequestTransportReleased(t *testing.T) {
	t.Run("tools.egress-per-request-transport-released", func(t *testing.T) {
		resetNetworkFuncs(t)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "ok")
		}))
		defer server.Close()
		_, port := splitServer(server)
		var resolutions int
		resolveHost = func(context.Context, string) ([]net.IPAddr, error) {
			resolutions++
			return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
		}
		dials := 0
		dialAddress = func(ctx context.Context, network, address string) (net.Conn, error) {
			dials++
			return (&net.Dialer{}).DialContext(ctx, network, address)
		}
		client := Client(types.EgressPolicy{
			Allow:         []string{"seq.test:" + port},
			Schemes:       []string{"http"},
			PrivateRanges: types.AllowPrivate,
		})
		for range 2 {
			resp, err := client.Get("http://seq.test:" + port + "/")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := io.ReadAll(resp.Body); err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
		}
		if resolutions != 2 {
			t.Fatalf("resolution calls = %d, want 2 per-request", resolutions)
		}
		if dials != 2 {
			t.Fatalf("dials = %d, want 2 (no pooled connection reuse)", dials)
		}
	})
}

func proxyLifetimePolicy(host string, timeout time.Duration) types.EgressPolicy {
	return types.EgressPolicy{
		Allow:         []string{host},
		Schemes:       []string{"http", "https"},
		PrivateRanges: types.AllowPrivate,
		Timeout:       timeout,
	}
}

type flushWriter struct {
	w io.Writer
	f http.Flusher
}

func (fw flushWriter) Write(p []byte) (int, error) {
	n, err := fw.w.Write(p)
	if n > 0 {
		fw.f.Flush()
	}
	return n, err
}

// proxyOriginStub forwards to the fixed origin URL and tracks the proxy hop's
// own product-facing connections through the given tracker. The forward
// request follows the incoming request context, so a product disconnect
// aborts the forwarded hop.
func proxyOriginStub(t *testing.T, tracker *connTracker, origin string) *httptest.Server {
	t.Helper()
	client := &http.Client{Timeout: fixtureBoundedSeconds * time.Second}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, origin+r.URL.RequestURI(), nil)
		if err != nil {
			http.Error(w, "proxy forward build failed: "+err.Error(), http.StatusBadGateway)
			return
		}
		resp, err := client.Do(req)
		if err != nil {
			http.Error(w, "proxy forward failed: "+err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		for k, vs := range resp.Header {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		w.(http.Flusher).Flush()
		_, _ = io.Copy(flushWriter{w: w, f: w.(http.Flusher)}, resp.Body)
	}))
	server.Config.ConnState = tracker.state
	server.Start()
	t.Cleanup(server.Close)
	return server
}

// abortingProxyOriginStub forwards like proxyOriginStub but aborts the
// product connection mid-body when the origin stream breaks, so the product
// observes a real read error instead of a clean chunked terminator.
func abortingProxyOriginStub(t *testing.T, tracker *connTracker, origin string) *httptest.Server {
	t.Helper()
	client := &http.Client{Timeout: fixtureBoundedSeconds * time.Second}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, origin+r.URL.RequestURI(), nil)
		if err != nil {
			http.Error(w, "proxy forward build failed: "+err.Error(), http.StatusBadGateway)
			return
		}
		resp, err := client.Do(req)
		if err != nil {
			http.Error(w, "proxy forward failed: "+err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		w.WriteHeader(resp.StatusCode)
		w.(http.Flusher).Flush()
		if _, err := io.Copy(w, resp.Body); err != nil {
			panic(http.ErrAbortHandler)
		}
	}))
	server.Config.ConnState = tracker.state
	server.Start()
	t.Cleanup(server.Close)
	return server
}

func TestEgressProxyLifetime(t *testing.T) {
	t.Run("proxy-body-stall-bounded", func(t *testing.T) {
		resetNetworkFuncs(t)
		originTracker := &connTracker{}
		origin, closeHold := stallServer(t, originTracker, "abc")
		proxyTracker := &connTracker{}
		proxy := proxyOriginStub(t, proxyTracker, origin.URL)
		resolveHost = loopbackResolve("127.0.0.1")
		dialAddress = redirectDial(nil)
		client := Client(proxyLifetimePolicy("dest.test", 60*time.Millisecond), WithProxy(parseProxyURL(t, proxy.URL)))
		resp, err := client.Get("http://dest.test/stall")
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		var got []byte
		go func() {
			body, err := io.ReadAll(resp.Body)
			got = body
			done <- err
		}()
		select {
		case err := <-done:
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("proxied read error = %v, want deadline exceeded", err)
			}
			if string(got) != "abc" {
				t.Fatalf("proxied read bytes = %q, want %q", got, "abc")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("proxied body read hung past the policy time limit")
		}
		closeHold()
		proxyTracker.waitClosed(t)
		originTracker.waitClosed(t)
		if err := resp.Body.Close(); err != nil {
			t.Fatalf("close after deadline: %v", err)
		}
	})

	t.Run("proxy-header-stall-denied", func(t *testing.T) {
		resetNetworkFuncs(t)
		proxyTracker := &connTracker{}
		hold := make(chan struct{})
		origin := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			<-hold
		}))
		origin.Start()
		t.Cleanup(func() { close(hold); origin.Close() })
		proxy := proxyOriginStub(t, proxyTracker, origin.URL)
		resolveHost = loopbackResolve("127.0.0.1")
		dialAddress = redirectDial(nil)
		_, err := Client(proxyLifetimePolicy("dest.test", 60*time.Millisecond), WithProxy(parseProxyURL(t, proxy.URL))).Get("http://dest.test/header-stall")
		assertDenied(t, err, "timeout")
		proxyTracker.waitClosed(t)
	})

	t.Run("proxy-connect-refused", func(t *testing.T) {
		resetNetworkFuncs(t)
		proxyTracker := &connTracker{}
		server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodConnect {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			_, _ = io.WriteString(w, "unexpected")
		}))
		server.Config.ConnState = proxyTracker.state
		server.Start()
		t.Cleanup(server.Close)
		resolveHost = loopbackResolve("127.0.0.1")
		dialAddress = redirectDial(nil)
		client := Client(proxyLifetimePolicy("dest.test", 0), WithProxy(parseProxyURL(t, server.URL)))
		_, err := client.Get("https://dest.test/connect-refused")
		if err == nil {
			t.Fatal("CONNECT refusal produced a response, want transport error")
		}
		proxyTracker.waitClosed(t)
	})

	t.Run("proxy-tls-failure", func(t *testing.T) {
		resetNetworkFuncs(t)
		proxyTracker := &connTracker{}
		plain := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		plain.Config.ConnState = proxyTracker.state
		plain.Start()
		t.Cleanup(plain.Close)
		proxyURL := parseProxyURL(t, plain.URL)
		proxyURL.Scheme = "https"
		resolveHost = loopbackResolve("127.0.0.1")
		dialAddress = redirectDial(nil)
		_, err := Client(proxyLifetimePolicy("dest.test", 0), WithProxy(proxyURL)).Get("http://dest.test/via-broken-tls-proxy")
		if err == nil {
			t.Fatal("TLS handshake against a plain proxy endpoint succeeded, want handshake failure")
		}
		proxyTracker.waitClosed(t)
	})

	t.Run("proxy-size-refusal", func(t *testing.T) {
		resetNetworkFuncs(t)
		proxyTracker := &connTracker{}
		origin := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Length", "10")
			_, _ = io.WriteString(w, "1234567890")
		}))
		origin.Start()
		t.Cleanup(origin.Close)
		proxy := proxyOriginStub(t, proxyTracker, origin.URL)
		resolveHost = loopbackResolve("127.0.0.1")
		dialAddress = redirectDial(nil)
		p := proxyLifetimePolicy("dest.test", 0)
		p.MaxBytes = 4
		_, err := Client(p, WithProxy(parseProxyURL(t, proxy.URL))).Get("http://dest.test/oversize")
		assertDenied(t, err, "size limit 4")
		proxyTracker.waitClosed(t)
	})

	t.Run("proxy-body-cap-read", func(t *testing.T) {
		resetNetworkFuncs(t)
		origin := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "12345")
			w.(http.Flusher).Flush()
		}))
		origin.Start()
		t.Cleanup(origin.Close)
		proxy := proxyOriginStub(t, &connTracker{}, origin.URL)
		resolveHost = loopbackResolve("127.0.0.1")
		dialAddress = redirectDial(nil)
		p := proxyLifetimePolicy("dest.test", 0)
		p.MaxBytes = 4
		resp, err := Client(p, WithProxy(parseProxyURL(t, proxy.URL))).Get("http://dest.test/cap")
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		var denied types.EgressDenied
		if !errors.As(err, &denied) || denied.Reason != "size limit 4" {
			t.Fatalf("read error = %v, want size limit 4", err)
		}
		if string(got) != "1234" {
			t.Fatalf("read bytes = %q, want capped %q", got, "1234")
		}
	})

	t.Run("proxy-eof-release", func(t *testing.T) {
		resetNetworkFuncs(t)
		proxyTracker := &connTracker{}
		origin := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "abc")
			w.(http.Flusher).Flush()
			panic(http.ErrAbortHandler)
		}))
		origin.Start()
		t.Cleanup(origin.Close)
		proxy := abortingProxyOriginStub(t, proxyTracker, origin.URL)
		resolveHost = loopbackResolve("127.0.0.1")
		dialAddress = redirectDial(nil)
		resp, err := Client(proxyLifetimePolicy("dest.test", 0), WithProxy(parseProxyURL(t, proxy.URL))).Get("http://dest.test/eof")
		if err != nil {
			t.Fatal(err)
		}
		_, err = io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err == nil {
			t.Fatal("aborted origin body ended with io.EOF, want a read error")
		}
		proxyTracker.waitClosed(t)
	})

	t.Run("proxy-repeated-close", func(t *testing.T) {
		resetNetworkFuncs(t)
		origin := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "ok")
		}))
		origin.Start()
		t.Cleanup(origin.Close)
		proxy := proxyOriginStub(t, &connTracker{}, origin.URL)
		resolveHost = loopbackResolve("127.0.0.1")
		dialAddress = redirectDial(nil)
		resp, err := Client(proxyLifetimePolicy("dest.test", 0), WithProxy(parseProxyURL(t, proxy.URL))).Get("http://dest.test/twice")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.ReadAll(resp.Body); err != nil {
			t.Fatal(err)
		}
		if err := resp.Body.Close(); err != nil {
			t.Fatalf("first close: %v", err)
		}
		if err := resp.Body.Close(); err != nil {
			t.Fatalf("repeated close: %v", err)
		}
	})
}
