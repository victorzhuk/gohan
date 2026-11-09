package egress

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const fixtureBoundedSeconds = 5

// Serial-suffix helpers mutate package globals resolveHost and dialAddress and
// must run with packageWorkers 1 and parallelTests 1 per the chunk budgets.

type rawObservation struct {
	Target string
	Host   string
}

type rawObserver struct {
	mu   sync.Mutex
	seen []rawObservation
}

func (o *rawObserver) record(target, host string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.seen = append(o.seen, rawObservation{Target: target, Host: host})
}

func (o *rawObserver) snapshot() []rawObservation {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]rawObservation, len(o.seen))
	copy(out, o.seen)
	return out
}

func (o *rawObserver) count() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.seen)
}

// observingListener feeds every connection's first request head into the
// observer before net/http parses it, capturing the absolute request target
// and the raw Host header as seen on the wire.
type observingListener struct {
	net.Listener
	obs *rawObserver
}

func (l *observingListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &rawHeadConn{Conn: c, obs: l.obs}, nil
}

type rawHeadConn struct {
	net.Conn
	obs   *rawObserver
	once  sync.Once
	head  *bufio.Reader
	trail []byte
}

func (c *rawHeadConn) Read(p []byte) (int, error) {
	c.once.Do(func() { c.captureHead() })
	if len(c.trail) > 0 {
		n := copy(p, c.trail)
		c.trail = c.trail[n:]
		return n, nil
	}
	if c.head == nil {
		return c.Conn.Read(p)
	}
	return c.head.Read(p)
}

func (c *rawHeadConn) captureHead() {
	c.head = bufio.NewReaderSize(c.Conn, 16*1024)
	var head []byte
	buf := make([]byte, 1)
	for {
		if len(head) >= 32*1024 {
			return
		}
		n, err := c.head.Read(buf)
		if n == 0 {
			if err != nil {
				return
			}
			continue
		}
		head = append(head, buf[:n]...)
		if i := strings.Index(string(head), "\r\n\r\n"); i >= 0 {
			c.trail = append([]byte{}, head...)
			break
		}
	}
	target, host := parseRequestHead(string(head))
	c.obs.record(target, host)
}

func parseRequestHead(head string) (target, host string) {
	lines := strings.Split(head, "\r\n")
	if len(lines) == 0 {
		return "", ""
	}
	parts := strings.Split(lines[0], " ")
	if len(parts) >= 2 {
		target = parts[1]
	}
	for _, line := range lines[1:] {
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(name), "Host") {
			host = strings.TrimSpace(value)
			break
		}
	}
	return target, host
}

type controlledPeer struct {
	server  *httptest.Server
	marker  string
	requests atomic.Int32
	ready    atomic.Bool
	raw      rawObserver
}

// startControlledPeerSerial starts one independent origin with a distinct
// response marker and per-request counting.
func startControlledPeerSerial(t *testing.T, marker string) *controlledPeer {
	t.Helper()
	p := &controlledPeer{marker: marker}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.requests.Add(1)
		_, _ = io.WriteString(w, p.marker)
	})
	p.server = httptest.NewUnstartedServer(handler)
	p.server.Listener = &observingListener{Listener: p.server.Listener, obs: &p.raw}
	p.server.Start()
	t.Cleanup(func() { closeBounded(t, p.server, "controlled peer") })
	return p
}

func (p *controlledPeer) loopbackAddr() string {
	return p.server.Listener.Addr().String()
}

func (p *controlledPeer) count() int32 {
	return p.requests.Load()
}

func (p *controlledPeer) resetCount() {
	p.requests.Store(0)
}

// checkReady performs the direct health check required before any product
// use of the peer.
func (p *controlledPeer) checkReady(t *testing.T) {
	t.Helper()
	client := &http.Client{Timeout: fixtureBoundedSeconds * time.Second}
	resp, err := client.Get(p.server.URL)
	if err != nil {
		t.Fatalf("peer readiness check failed: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("peer readiness body read failed: %v", err)
	}
	if string(body) != p.marker {
		t.Fatalf("peer marker = %q, want %q", body, p.marker)
	}
	p.ready.Store(true)
}

// refuseUnready rejects security assertions against an unready peer.
func (p *controlledPeer) refuseUnready(t *testing.T) {
	t.Helper()
	if !p.ready.Load() {
		t.Fatal("controlled peer refused: readiness check not established")
	}
}

func (p *controlledPeer) rawObservations() []rawObservation {
	return p.raw.snapshot()
}

type forwardingProxy struct {
	server  *httptest.Server
	routes  map[string]string
	counts  map[string]*atomic.Int32
	mu      sync.Mutex
	tracker *connTracker
	raw     *rawObserver
	client  *http.Client
}

// startForwardingProxySerial starts a real forwarding proxy. routes maps the
// hostname the proxy receives to the exact loopback endpoint it forwards to;
// the map is the proxy's own resolution and is independent of client
// resolution.
func startForwardingProxySerial(t *testing.T, routes map[string]string) *forwardingProxy {
	t.Helper()
	fp := &forwardingProxy{
		routes:  routes,
		counts:  make(map[string]*atomic.Int32),
		tracker: &connTracker{},
		raw:     &rawObserver{},
	}
	transport := &http.Transport{}
	transport.DisableKeepAlives = false
	fp.client = &http.Client{Transport: transport, Timeout: fixtureBoundedSeconds * time.Second}
	handler := http.HandlerFunc(fp.serveHTTP)
	fp.server = httptest.NewUnstartedServer(handler)
	fp.server.Config.ConnState = fp.tracker.state
	fp.server.Listener = &observingListener{Listener: fp.server.Listener, obs: fp.raw}
	fp.server.Start()
	t.Cleanup(func() {
		fp.client.CloseIdleConnections()
		closeBounded(t, fp.server, "forwarding proxy")
		fp.tracker.waitClosed(t)
	})
	return fp
}

func (fp *forwardingProxy) URL() string {
	return fp.server.URL
}

func (fp *forwardingProxy) originCount(origin string) int32 {
	c, ok := fp.counts[origin]
	if !ok {
		return 0
	}
	return c.Load()
}

func (fp *forwardingProxy) allOriginCounts() map[string]int32 {
	fp.mu.Lock()
	defer fp.mu.Unlock()
	out := make(map[string]int32, len(fp.counts))
	for k, c := range fp.counts {
		out[k] = c.Load()
	}
	return out
}

func (fp *forwardingProxy) rawObservations() []rawObservation {
	return fp.raw.snapshot()
}

func (fp *forwardingProxy) resetCounts() {
	fp.mu.Lock()
	defer fp.mu.Unlock()
	for _, c := range fp.counts {
		c.Store(0)
	}
}

func (fp *forwardingProxy) bumpOrigin(origin string) {
	fp.mu.Lock()
	c, ok := fp.counts[origin]
	if !ok {
		c = &atomic.Int32{}
		fp.counts[origin] = c
	}
	fp.mu.Unlock()
	c.Add(1)
}

func (fp *forwardingProxy) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		fp.serveConnect(w, r)
		return
	}
	target := r.RequestURI
	u, err := url.ParseRequestURI(target)
	if err != nil {
		http.Error(w, "fixture proxy cannot parse request target: "+err.Error(), http.StatusBadRequest)
		return
	}
	origin, ok := fp.routes[u.Hostname()]
	if !ok {
		http.Error(w, "fixture proxy has no route for "+u.Hostname(), http.StatusBadGateway)
		return
	}
	outURL := &url.URL{Scheme: "http", Host: origin}
	outURL.Path = u.Path
	outURL.RawQuery = u.RawQuery
	outReq, err := http.NewRequest(r.Method, outURL.String(), r.Body)
	if err != nil {
		http.Error(w, "fixture proxy forward build failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	outReq.Host = r.Host
	for k, vs := range r.Header {
		for _, v := range vs {
			outReq.Header.Add(k, v)
		}
	}
	fp.bumpOrigin(origin)
	resp, err := fp.client.Do(outReq)
	if err != nil {
		http.Error(w, "fixture proxy forward failed: "+err.Error(), http.StatusBadGateway)
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

// serveConnect tunnels to the endpoint the proxy's own map selects for the
// CONNECT authority, preserving the authority as the pinned destination.
func (fp *forwardingProxy) serveConnect(w http.ResponseWriter, r *http.Request) {
	authority := r.RequestURI
	host, _, err := net.SplitHostPort(authority)
	if err != nil {
		http.Error(w, "fixture proxy cannot parse CONNECT authority: "+err.Error(), http.StatusBadRequest)
		return
	}
	origin, ok := fp.routes[host]
	if !ok {
		http.Error(w, "fixture proxy has no route for "+host, http.StatusBadGateway)
		return
	}
	originHost, originPort, _ := net.SplitHostPort(origin)
	dest := net.JoinHostPort(loopbackHost(originHost), originPort)
	fp.bumpOrigin(origin)
	upstream, err := net.DialTimeout("tcp", dest, fixtureBoundedSeconds*time.Second)
	if err != nil {
		http.Error(w, "fixture proxy CONNECT dial failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		_ = upstream.Close()
		http.Error(w, "fixture proxy cannot hijack", http.StatusInternalServerError)
		return
	}
	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		_ = upstream.Close()
		return
	}
	_, _ = io.WriteString(clientConn, "HTTP/1.1 200 Connection Established\r\n\r\n")
	go relayBounded(clientConn, upstream)
	go relayBounded(upstream, clientConn)
}

func loopbackHost(host string) string {
	if ip := net.ParseIP(host); ip != nil && !ip.IsLoopback() {
		return "127.0.0.1"
	}
	return host
}

func relayBounded(dst io.WriteCloser, src io.ReadCloser) {
	defer func() { _ = dst.Close(); _ = src.Close() }()
	_, _ = io.Copy(dst, src)
}

// dialSeamMapping maps logical IP literals to exact loopback endpoints. The
// mapping applies only inside the test dial seam installed by
// installControlledResolutionSerial.
type dialSeamMapping map[string]string

// installControlledResolutionSerial wires resolveHost to return the logical
// addresses per hostname and dialAddress to translate logical IP:port to the
// mapped loopback endpoint. Requires serial use.
func installControlledResolutionSerial(t *testing.T, dns map[string][]net.IPAddr, seam dialSeamMapping) {
	t.Helper()
	resetNetworkFuncs(t)
	resolveHost = func(_ context.Context, host string) ([]net.IPAddr, error) {
		if addrs, ok := dns[host]; ok {
			return addrs, nil
		}
		return nil, fmt.Errorf("fixture resolver has no answer for %q", host)
	}
	dialAddress = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(address)
		if err == nil {
			if mapped, ok := seam[host]; ok {
				return (&net.Dialer{}).DialContext(ctx, network, mapped)
			}
		}
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
}

type boundedCloser interface {
	Close()
}

func closeBounded(t *testing.T, c boundedCloser, what string) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		c.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(fixtureBoundedSeconds * time.Second):
		t.Fatalf("%s close exceeded %ds bound", what, fixtureBoundedSeconds)
	}
}

// tlsFixtureBundle holds an ephemeral CA plus distinct proxy and origin
// certificate files for bounded subprocess trust installation.
type tlsFixtureBundle struct {
	dir        string
	caPEMPath  string
	proxyCert  string
	proxyKey   string
	originCert string
	originKey  string
}

func newTLSFixtureBundle(t *testing.T, proxyName, originName string) *tlsFixtureBundle {
	t.Helper()
	dir := t.TempDir()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("CA key generation failed: %v", err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "gohan fixture CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("CA certificate creation failed: %v", err)
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	caPEMPath := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(caPEMPath, caPEM, 0o600); err != nil {
		t.Fatalf("CA write failed: %v", err)
	}
	proxyCert, proxyKey := issueFixtureLeaf(t, dir, caTemplate, caDER, caKey, proxyName, "proxy")
	originCert, originKey := issueFixtureLeaf(t, dir, caTemplate, caDER, caKey, originName, "origin")
	return &tlsFixtureBundle{
		dir:        dir,
		caPEMPath:  caPEMPath,
		proxyCert:  proxyCert,
		proxyKey:   proxyKey,
		originCert: originCert,
		originKey:  originKey,
	}
}

func issueFixtureLeaf(t *testing.T, dir string, caTemplate *x509.Certificate, caDER []byte, caKey *ecdsa.PrivateKey, dnsName, kind string) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("%s key generation failed: %v", kind, err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatalf("CA parse failed: %v", err)
	}
	leaf := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: dnsName},
		DNSNames:     []string{dnsName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, caCert, &key.PublicKey, caKey)
	if err != nil {
		t.Fatalf("%s certificate creation failed: %v", kind, err)
	}
	certPath := filepath.Join(dir, kind+".crt")
	keyPath := filepath.Join(dir, kind+".key")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER}), 0o600); err != nil {
		t.Fatalf("%s certificate write failed: %v", kind, err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("%s key marshal failed: %v", kind, err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatalf("%s key write failed: %v", kind, err)
	}
	return certPath, keyPath
}

const tlsFixtureChildEnv = "GOHAN_TLS_FIXTURE_CHILD"

// launchTLSFixtureSubprocessSerial runs the named test in a bounded
// subprocess with the bundle CA installed through SSL_CERT_FILE and an empty
// SSL_CERT_DIR before system roots initialize. Verification stays enabled.
func launchTLSFixtureSubprocessSerial(t *testing.T, bundle *tlsFixtureBundle, run string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("subprocess executable lookup failed: %v", err)
	}
	cmd := exec.Command(exe, "-test.run="+run, "-test.timeout=25s")
	cmd.Env = append(
		os.Environ(),
		"SSL_CERT_FILE="+bundle.caPEMPath,
		"SSL_CERT_DIR=",
		tlsFixtureChildEnv+"=1",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("TLS fixture subprocess failed: %v\n%s", err, out)
	}
}

func inTLSFixtureSubprocess() bool {
	return os.Getenv(tlsFixtureChildEnv) == "1"
}

// TestNetworkFixtureControl proves the fixture family itself: both peers
// health-check directly, the proxy hostname map forwards to the denied peer
// independently of client resolution, raw wire observation captures the
// absolute target and Host header, per-origin counters track real forwarded
// bytes, counters reset after readiness controls, and cleanup stays bounded.
func TestNetworkFixtureControl(t *testing.T) {
	allowed := startControlledPeerSerial(t, "allowed-origin-marker-v1")
	denied := startControlledPeerSerial(t, "denied-origin-marker-v2")
	allowed.checkReady(t)
	denied.checkReady(t)
	allowed.refuseUnready(t)
	denied.refuseUnready(t)
	if allowed.count() != 1 || denied.count() != 1 {
		t.Fatalf("readiness counts allowed=%d denied=%d, want 1/1", allowed.count(), denied.count())
	}
	allowed.resetCount()
	denied.resetCount()

	proxy := startForwardingProxySerial(t, map[string]string{
		"allowed.test": allowed.loopbackAddr(),
		"denied.test":  denied.loopbackAddr(),
	})

	proxyClient := &http.Client{
		Transport: &http.Transport{Proxy: func(*http.Request) (*url.URL, error) {
			return url.Parse(proxy.URL())
		}},
		Timeout: fixtureBoundedSeconds * time.Second,
	}
	resp, err := proxyClient.Get("http://denied.test/control-path")
	if err != nil {
		t.Fatalf("hostname-forwarding control request failed: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("control body read failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("control status = %d, want 200", resp.StatusCode)
	}
	if string(body) != "denied-origin-marker-v2" {
		t.Fatalf("forwarded body = %q, want denied-origin-marker-v2", body)
	}

	if denied.count() != 1 {
		t.Fatalf("denied peer count = %d, want 1 real forwarded request", denied.count())
	}
	if allowed.count() != 0 {
		t.Fatalf("allowed peer count = %d, want 0", allowed.count())
	}
	if got := proxy.originCount(denied.loopbackAddr()); got != 1 {
		t.Fatalf("proxy denied-origin count = %d, want 1", got)
	}
	if got := proxy.originCount(allowed.loopbackAddr()); got != 0 {
		t.Fatalf("proxy allowed-origin count = %d, want 0", got)
	}

	obs := proxy.rawObservations()
	if len(obs) != 1 {
		t.Fatalf("raw observations = %d, want 1", len(obs))
	}
	if !strings.HasPrefix(obs[0].Target, "http://denied.test/control-path") {
		t.Fatalf("raw absolute target = %q, want absolute http://denied.test/control-path", obs[0].Target)
	}
	if obs[0].Host != "denied.test" {
		t.Fatalf("raw Host = %q, want denied.test", obs[0].Host)
	}

	allowed.resetCount()
	denied.resetCount()
	proxy.resetCounts()
	if allowed.count() != 0 || denied.count() != 0 {
		t.Fatalf("counter reset left allowed=%d denied=%d", allowed.count(), denied.count())
	}
	for origin, count := range proxy.allOriginCounts() {
		if count != 0 {
			t.Fatalf("proxy counter reset left %s=%d", origin, count)
		}
	}
}
