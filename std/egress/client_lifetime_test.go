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
