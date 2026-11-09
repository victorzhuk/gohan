package http_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std/egress"
	httpTool "github.com/victorzhuk/gohan/std/tool/http"
	"github.com/victorzhuk/gohan/testkit/gohantest"
)

type egressScenarioTelemetry struct {
	count int
	attrs map[string]any
}

func (m *egressScenarioTelemetry) Count(_ context.Context, name string, value int64, attrs ...types.Attr) {
	if name != "gohan.egress.requests" || value != 1 {
		return
	}
	m.count += int(value)
	m.attrs = make(map[string]any, len(attrs))
	for _, attr := range attrs {
		m.attrs[attr.Key] = attr.Value
	}
}

func (m *egressScenarioTelemetry) StartSpan(ctx context.Context, _ string, _ ...types.Attr) (context.Context, func(...types.Attr)) {
	return ctx, func(...types.Attr) {}
}

func (m *egressScenarioTelemetry) Record(context.Context, string, float64, ...types.Attr) {}

func TestEgressScenarios(t *testing.T) {
	policy := func(max int64) types.EgressPolicy {
		return types.EgressPolicy{Allow: []string{"127.0.0.1"}, Schemes: []string{"http"}, PrivateRanges: types.DenyPrivate, MaxBytes: max}
	}
	t.Run("tools.egress-metadata-blocked", func(t *testing.T) {
		var received int
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { received++ }))
		defer srv.Close()
		telemetry := &egressScenarioTelemetry{}
		text, _, _ := runEgressScenario(t, policy(1024), "http://169.254.169.254/latest/meta-data/", telemetry)
		if received != 0 {
			t.Fatalf("metadata listener received %d requests", received)
		}
		if !strings.Contains(text, "link-local") {
			t.Fatalf("response %q does not name link-local refusal", text)
		}
		if telemetry.count != 1 || telemetry.attrs["tool"] != "web_fetch" || telemetry.attrs["host"] != "169.254.169.254" || telemetry.attrs["decision"] != "denied" {
			t.Fatalf("denied metric = %#v", telemetry)
		}
	})
	t.Run("tools.egress-redirect-revalidated", func(t *testing.T) {
		var received int
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			received++
			if r.URL.Path == "/" {
				http.Redirect(w, r, "http://10.0.0.5/", http.StatusFound)
				return
			}
			t.Errorf("unexpected second-hop request: %s", r.URL)
		}))
		defer srv.Close()
		localPolicy := policy(1024)
		text, _, _ := runEgressScenario(t, localPolicy, srv.URL, nil)
		if received != 1 {
			t.Fatalf("redirect server received %d requests, want 1", received)
		}
		if !strings.Contains(text, "private range") {
			t.Fatalf("response does not show the private redirect refusal: %q", text)
		}
		if strings.Contains(text, "Found") {
			t.Fatalf("first-hop response body leaked into result: %q", text)
		}
	})
	t.Run("tools.egress-size-limit", func(t *testing.T) {
		const limit = 64
		body := strings.Repeat("x", limit+30)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(body))
		}))
		defer srv.Close()
		text, _, failureKind := runEgressScenario(t, policy(limit), srv.URL, nil)
		if failureKind != types.Permanent {
			t.Fatalf("failure kind = %v, want %v", failureKind, types.Permanent)
		}
		if !strings.Contains(text, "size limit 64") {
			t.Fatalf("failure message = %q, want it to name limit %d", text, limit)
		}
		if strings.Contains(text, body) {
			t.Fatalf("result contains response body beyond limit: %q", text)
		}
	})
	t.Run("tools.egress-audited", func(t *testing.T) {
		log := stores.NewMemoryAuditLog()
		text, sessionID, _ := runEgressScenario(t, policy(1024), "http://169.254.169.254/private-body", nil, log)
		if !strings.Contains(text, "link-local") {
			t.Fatalf("response %q does not name link-local refusal", text)
		}
		found := false
		for record, err := range log.Read(context.Background(), sessionID) {
			if err != nil {
				t.Fatalf("read audit log: %v", err)
			}
			if record.Kind != stores.AuditEgressDenied {
				continue
			}
			found = true
			if record.Tool != "web_fetch" || record.RunID == "" || record.Host != "169.254.169.254" || record.Reason != "link-local" {
				t.Fatalf("audit record = %#v", record)
			}
		}
		if !found {
			t.Fatal("egress denial audit record was not found")
		}
	})
}

func runEgressScenario(t *testing.T, policy types.EgressPolicy, rawURL string, telemetry *egressScenarioTelemetry, logs ...*stores.MemoryAuditLog) (string, string, types.ErrorKind) {
	t.Helper()
	return runEgressScenarioFactory(t, policy, rawURL, telemetry, func(p types.EgressPolicy) *http.Client { return egress.Client(p) }, logs...)
}

func runEgressScenarioFactory(t *testing.T, policy types.EgressPolicy, rawURL string, telemetry *egressScenarioTelemetry, clientFactory func(types.EgressPolicy) *http.Client, logs ...*stores.MemoryAuditLog) (string, string, types.ErrorKind) {
	t.Helper()
	tool, err := httpTool.Fetch(policy)
	if err != nil {
		t.Fatalf("create fetch tool: %v", err)
	}
	args, err := json.Marshal(httpTool.FetchInput{URL: rawURL})
	if err != nil {
		t.Fatalf("encode tool input: %v", err)
	}
	model := gohantest.NewScriptedModel(types.ModelProfile{Name: "scripted", Caps: types.Caps{Tools: true}}, gohantest.ToolCall("web_fetch", json.RawMessage(args)), gohantest.Text("finished"))
	options := []gohan.Option{
		gohan.WithStores(stores.Stores{SessionLog: stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))}),
		gohan.WithModels(model),
		gohan.WithNativeAgent(gohan.NativeSpec{
			Request: gohan.FlowRequest{Name: "egress-scenarios"}, Profile: "scripted", Tools: []types.Tool{tool},
			Assemble: func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
				return types.ModelRequest{Messages: in.History}, nil
			},
		}),
		gohan.WithEgressClient(clientFactory),
	}
	if telemetry != nil {
		options = append(options, gohan.WithTelemetry(telemetry))
	}
	stack, err := gohan.Build(options...)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	conv, err := gohan.NewNativeConversation(stack, "egress-scenarios", gohan.WithConversationRuns(stores.NewMemoryRuns()), gohan.WithConversationEventLog(stores.NewMemoryEventLog()))
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	sessionID := "egress-session"
	ctx := types.WithPrincipal(context.Background(), types.Principal{Tenant: "tenant", Subject: "subject"})
	if telemetry != nil {
		ctx = types.WithTelemetry(ctx, telemetry)
	}
	if len(logs) > 0 {
		ctx = gohan.WithDecisionAudit(ctx, logs[0])
	}
	var b strings.Builder
	var failureKind types.ErrorKind
	for event, err := range conv.Send(ctx, sessionID, types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "Fetch this URL: " + rawURL}}}) {
		if err != nil {
			t.Fatalf("send: %v", err)
		}
		switch e := event.(type) {
		case types.TextDelta:
			b.WriteString(e.Delta)
		case types.ToolFinished:
			for _, block := range e.Result.Content {
				if text, ok := block.(types.Text); ok {
					b.WriteString(text.Text)
				}
			}
			if e.Result.Error != nil {
				failureKind = e.Result.Error.Kind
				b.WriteString(e.Result.Error.Message)
			}
		}
	}
	return b.String(), sessionID, failureKind
}

const toolFixtureBound = 5 * time.Second

type toolWireObservation struct {
	target string
	host   string
}

// toolWireRecorder captures the request target and Host authority exactly as
// the product HTTP server received them on the wire.
type toolWireRecorder struct {
	mu  sync.Mutex
	raw []toolWireObservation
}

func (r *toolWireRecorder) record(o toolWireObservation) {
	r.mu.Lock()
	r.raw = append(r.raw, o)
	r.mu.Unlock()
}

func (r *toolWireRecorder) observations() []toolWireObservation {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]toolWireObservation(nil), r.raw...)
}

func (r *toolWireRecorder) resetObservations() {
	r.mu.Lock()
	r.raw = nil
	r.mu.Unlock()
}

type toolOrigin struct {
	server   *httptest.Server
	marker   string
	ready    bool
	mu       sync.Mutex
	requests int
	raw      *toolWireRecorder
}

func startToolOrigin(t *testing.T, marker string) *toolOrigin {
	t.Helper()
	o := &toolOrigin{marker: marker, raw: &toolWireRecorder{}}
	o.server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		o.raw.record(toolWireObservation{target: r.RequestURI, host: r.Host})
		o.mu.Lock()
		o.requests++
		o.mu.Unlock()
		_, _ = io.WriteString(w, marker)
	}))
	o.server.Start()
	t.Cleanup(o.server.Close)
	return o
}

func (o *toolOrigin) loopbackAddr() string {
	return o.server.Listener.Addr().String()
}

func (o *toolOrigin) count() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.requests
}

func (o *toolOrigin) reset() {
	o.mu.Lock()
	o.requests = 0
	o.mu.Unlock()
	o.raw.resetObservations()
}

func (o *toolOrigin) observations() []toolWireObservation {
	return o.raw.observations()
}

// checkReady proves the origin serves its marker through a plain direct fetch
// before any product request, then resets the counters the assertions use.
func (o *toolOrigin) checkReady(t *testing.T) {
	t.Helper()
	client := &http.Client{Timeout: toolFixtureBound}
	resp, err := client.Get("http://" + o.loopbackAddr() + "/")
	if err != nil {
		t.Fatalf("origin readiness check: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("origin readiness read: %v", err)
	}
	if resp.StatusCode != http.StatusOK || string(body) != o.marker {
		t.Fatalf("origin readiness = %d %q, want 200 %q", resp.StatusCode, body, o.marker)
	}
	o.mu.Lock()
	o.ready = true
	o.mu.Unlock()
	o.reset()
}

// refuseUnready rejects security assertions when the readiness control never
// proved the origin serves traffic on its own.
func (o *toolOrigin) refuseUnready(t *testing.T) {
	t.Helper()
	o.mu.Lock()
	ready := o.ready
	o.mu.Unlock()
	if !ready {
		t.Fatal("origin readiness control did not run; refusing zero-count assertions")
	}
}

type toolProxy struct {
	server *httptest.Server
	routes map[string]string
	mu     sync.Mutex
	counts map[string]int
	raw    *toolWireRecorder
}

// startToolProxy starts a real forwarding proxy whose routes map is the
// proxy's own resolution, independent of the client's.
func startToolProxy(t *testing.T, routes map[string]string) *toolProxy {
	t.Helper()
	p := &toolProxy{routes: routes, counts: make(map[string]int, len(routes)), raw: &toolWireRecorder{}}
	p.server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.raw.record(toolWireObservation{target: r.RequestURI, host: r.Host})
		u, err := url.Parse(r.RequestURI)
		if err != nil {
			http.Error(w, "fixture proxy cannot parse request target: "+err.Error(), http.StatusBadRequest)
			return
		}
		origin, ok := p.routes[u.Hostname()]
		if !ok {
			http.Error(w, "fixture proxy has no route for "+u.Hostname(), http.StatusBadGateway)
			return
		}
		outReq := r.Clone(r.Context())
		outURL := *r.URL
		outURL.Scheme = "http"
		outURL.Host = origin
		outReq.URL = &outURL
		outReq.Host = u.Host
		outReq.RequestURI = ""
		resp, err := (&http.Client{Timeout: toolFixtureBound}).Do(outReq)
		if err != nil {
			http.Error(w, "fixture proxy forward failed: "+err.Error(), http.StatusBadGateway)
			return
		}
		defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()
		p.mu.Lock()
		p.counts[origin]++
		p.mu.Unlock()
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}))
	p.server.Start()
	t.Cleanup(p.server.Close)
	return p
}

func (p *toolProxy) loopbackAddr() string {
	return p.server.Listener.Addr().String()
}

func (p *toolProxy) originCount(origin string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.counts[origin]
}

func (p *toolProxy) resetCounts() {
	p.mu.Lock()
	p.counts = make(map[string]int, len(p.routes))
	p.mu.Unlock()
}

func (p *toolProxy) observations() []toolWireObservation {
	return p.raw.observations()
}

func TestEgressProxyToolScenarios(t *testing.T) {
	proxyPolicy := func(max int64) types.EgressPolicy {
		return types.EgressPolicy{Allow: []string{"127.0.0.1"}, Schemes: []string{"http"}, PrivateRanges: types.AllowPrivate, MaxBytes: max}
	}
	t.Run("forwarding-proxy-allowed-journey", func(t *testing.T) {
		const marker = "allowed-origin-marker-tool-journey"
		origin := startToolOrigin(t, marker)
		origin.checkReady(t)
		proxy := startToolProxy(t, map[string]string{"127.0.0.1": origin.loopbackAddr()})
		proxyURL, err := url.Parse("http://" + proxy.loopbackAddr())
		if err != nil {
			t.Fatalf("parse proxy url: %v", err)
		}
		dest := "http://" + origin.loopbackAddr() + "/"
		text, _, _ := runEgressScenarioFactory(t, proxyPolicy(1024), dest, nil, func(p types.EgressPolicy) *http.Client {
			return egress.Client(p, egress.WithProxy(proxyURL), egress.WithTimeout(10*time.Second))
		})
		origin.refuseUnready(t)
		if !strings.Contains(text, marker) {
			t.Fatalf("tool result %q does not carry the forwarded origin marker", text)
		}
		if got := origin.count(); got != 1 {
			t.Fatalf("origin forwarded requests = %d, want 1", got)
		}
		var proxyObs *toolWireObservation
		for _, o := range proxy.observations() {
			if o.target == dest {
				obs := o
				proxyObs = &obs
				break
			}
		}
		if proxyObs == nil {
			t.Fatalf("proxy wire observations %v do not contain the absolute target %q the client requested", proxy.observations(), dest)
		}
		if proxyObs.host != origin.loopbackAddr() {
			t.Fatalf("proxy wire Host = %q, want original authority %q", proxyObs.host, origin.loopbackAddr())
		}
		var originObs *toolWireObservation
		if obs := origin.observations(); len(obs) > 0 {
			originObs = &obs[len(obs)-1]
		}
		if originObs == nil || originObs.host != origin.loopbackAddr() {
			t.Fatalf("origin wire observations %v do not retain the original authority %q", origin.observations(), origin.loopbackAddr())
		}
		if got := proxy.originCount(origin.loopbackAddr()); got != 1 {
			t.Fatalf("proxy forwarded to origin %d times, want 1", got)
		}
	})
	t.Run("denied-destination-no-origin-requests", func(t *testing.T) {
		policy := types.EgressPolicy{Allow: []string{"127.0.0.1"}, Schemes: []string{"http"}, PrivateRanges: types.DenyPrivate, MaxBytes: 1024}
		allowed := startToolOrigin(t, "allowed-substitute-marker")
		allowed.checkReady(t)
		denied := startToolOrigin(t, "denied-peer-marker")
		denied.checkReady(t)
		text, _, kind := runEgressScenarioFactory(t, policy, "http://10.1.2.3:9/private", nil, func(p types.EgressPolicy) *http.Client {
			return egress.Client(p, egress.WithTimeout(10*time.Second))
		})
		allowed.refuseUnready(t)
		denied.refuseUnready(t)
		if kind != types.Permanent {
			t.Fatalf("failure kind = %v, want %v", kind, types.Permanent)
		}
		if !strings.Contains(text, "private range") {
			t.Fatalf("tool result %q does not carry the egress denial reason", text)
		}
		if got := allowed.count(); got != 0 {
			t.Fatalf("substitute origin received %d requests, want 0 after denial", got)
		}
		if got := denied.count(); got != 0 {
			t.Fatalf("denied origin received %d requests, want 0", got)
		}
	})
	t.Run("unsupported-proxy-scheme-no-dispatch", func(t *testing.T) {
		origin := startToolOrigin(t, "scheme-test-origin-marker")
		origin.checkReady(t)
		proxyURL, err := url.Parse("socks5://" + origin.loopbackAddr())
		if err != nil {
			t.Fatalf("parse proxy url: %v", err)
		}
		text, _, kind := runEgressScenarioFactory(t, proxyPolicy(1024), "http://"+origin.loopbackAddr()+"/", nil, func(p types.EgressPolicy) *http.Client {
			return egress.Client(p, egress.WithProxy(proxyURL), egress.WithTimeout(10*time.Second))
		})
		origin.refuseUnready(t)
		if kind != types.Permanent {
			t.Fatalf("failure kind = %v, want %v", kind, types.Permanent)
		}
		if !strings.Contains(text, "proxy scheme not supported") {
			t.Fatalf("tool result %q does not map the unsupported proxy scheme refusal", text)
		}
		if got := origin.count(); got != 0 {
			t.Fatalf("origin received %d requests, want 0 without dispatch", got)
		}
	})
}
