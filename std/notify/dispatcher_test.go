package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std/egress"
	"github.com/victorzhuk/gohan/std/notify/webhook"
)

type recSink struct {
	mu   sync.Mutex
	got  []types.RunNotice
	fail func(types.RunNotice) error
}

func (s *recSink) Notify(_ context.Context, n types.RunNotice) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail != nil {
		if err := s.fail(n); err != nil {
			return err
		}
	}
	s.got = append(s.got, n)
	return nil
}

func (s *recSink) notices() []types.RunNotice {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]types.RunNotice(nil), s.got...)
}

type countRec struct {
	name  string
	n     int64
	attrs map[string]string
}

type countingTel struct {
	mu     sync.Mutex
	counts []countRec
}

func (t *countingTel) StartSpan(ctx context.Context, _ string, _ ...types.Attr) (context.Context, func(...types.Attr)) {
	return ctx, func(...types.Attr) {}
}

func (t *countingTel) Count(_ context.Context, name string, n int64, attrs ...types.Attr) {
	t.mu.Lock()
	defer t.mu.Unlock()
	rec := countRec{name: name, n: n, attrs: map[string]string{}}
	for _, a := range attrs {
		rec.attrs[a.Key] = fmt.Sprint(a.Value)
	}
	t.counts = append(t.counts, rec)
}

func (t *countingTel) Record(context.Context, string, float64, ...types.Attr) {}

func (t *countingTel) total(name string) int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	var sum int64
	for _, c := range t.counts {
		if c.name == name {
			sum += c.n
		}
	}
	return sum
}

func (t *countingTel) last(name string) (countRec, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for i := len(t.counts) - 1; i >= 0; i-- {
		if t.counts[i].name == name {
			return t.counts[i], true
		}
	}
	return countRec{}, false
}

// fixture carries the two clocks the retry tests drive: the runs store
// judges claims on storeNow, the dispatcher judges backoff on dispNow.
type fixture struct {
	runs     *stores.MemoryRuns
	storeNow *time.Time
	dispNow  *time.Time
	sink     *recSink
	tel      *countingTel
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	base := time.Unix(1_760_000_000, 0)
	f := &fixture{
		storeNow: new(time.Time),
		dispNow:  new(time.Time),
		sink:     &recSink{},
		tel:      &countingTel{},
	}
	*f.storeNow = base
	*f.dispNow = base
	f.runs = stores.NewMemoryRuns(
		stores.WithMemoryRunClock(func() time.Time { return *f.storeNow }),
		stores.WithMemoryRunInfo(func(ctx context.Context) (types.RunInfo, bool) {
			p, _ := types.PrincipalFrom(ctx)
			return types.RunInfo{Principal: p}, true
		}),
	)
	return f
}

func tenantCtx(tenant string) context.Context {
	return types.WithPrincipal(context.Background(), types.Principal{Tenant: tenant, Subject: "u1"})
}

// seedFinish starts a run under tenant and finishes it, so the outbox
// carries one finished notice.
func seedFinish(t *testing.T, f *fixture, tenant string) types.RunNotice {
	t.Helper()
	lease, err := f.runs.Start(tenantCtx(tenant), stores.Run{SessionID: "s-" + tenant, RunID: "r-" + tenant}, time.Minute)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := f.runs.Finish(tenantCtx(tenant), lease, stores.Finished, nil, ""); err != nil {
		t.Fatalf("Finish() error = %v", err)
	}
	claimed, err := f.runs.Notices(context.Background(), 1)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("Notices() = %v, %d notices, want 1", err, len(claimed))
	}
	// The seed read claimed the notice; lapse the claim so the dispatcher
	// can claim it.
	*f.storeNow = f.storeNow.Add(2 * stores.LeaseTTL)
	return claimed[0]
}

// egressAllowAll is the notice-lane policy for loopback httptest servers.
func egressAllowAll() types.EgressPolicy {
	return types.EgressPolicy{
		Schemes:       []string{"http"},
		Allow:         []string{"127.0.0.1"},
		PrivateRanges: types.AllowPrivate,
	}
}

func TestDispatch(t *testing.T) {
	ctx := context.Background()

	t.Run("acknowledges a delivered notice", func(t *testing.T) {
		f := newFixture(t)
		seedFinish(t, f, "acme")
		d := New(f.runs, f.sink, nil, WithTelemetry(f.tel), WithClock(func() time.Time { return *f.dispNow }))
		if err := d.Deliver(ctx); err != nil {
			t.Fatalf("Deliver() error = %v", err)
		}
		if rest, _ := f.runs.Notices(ctx, 10); len(rest) != 0 {
			t.Fatalf("delivered notice stayed pending: %d", len(rest))
		}
		got := f.sink.notices()
		if len(got) != 1 || got[0].Tenant != "acme" || got[0].Kind != types.NoticeFinished {
			t.Fatalf("sink = %+v", got)
		}
		if f.tel.total(metricDelivered) != 1 {
			t.Fatalf("delivered count = %d, want 1", f.tel.total(metricDelivered))
		}
		if c, ok := f.tel.last(metricDelivered); !ok || c.attrs[types.KeyNoticeKind] != "finished" {
			t.Fatalf("delivered metric attrs = %#v", c)
		}
	})

	t.Run("redelivers an unacknowledged claim with the same id", func(t *testing.T) {
		f := newFixture(t)
		first := seedFinish(t, f, "acme")
		attempt := 0
		f.sink.fail = func(types.RunNotice) error {
			attempt++
			if attempt == 1 {
				return &types.ModelError{Class: types.ClassTransient, Err: errTransient}
			}
			return nil
		}
		d := New(f.runs, f.sink, nil, WithTelemetry(f.tel), WithClock(func() time.Time { return *f.dispNow }))
		if err := d.Deliver(ctx); err != nil {
			t.Fatalf("Deliver() error = %v", err)
		}
		if pending, _ := f.runs.Notices(ctx, 10); len(pending) != 0 {
			t.Fatalf("claimed notice should be held, got %d pending", len(pending))
		}
		*f.storeNow = f.storeNow.Add(2 * stores.LeaseTTL)
		*f.dispNow = f.dispNow.Add(RetryStart)
		if err := d.Deliver(ctx); err != nil {
			t.Fatalf("Deliver() error = %v", err)
		}
		// The sink records only successful deliveries: the first attempt
		// failed, so the redelivery is the one recorded notice, carrying
		// the same id the first claim carried.
		got := f.sink.notices()
		if len(got) != 1 || got[0].ID != first.ID {
			t.Fatalf("redelivered notices = %+v, want one carrying %q", got, first.ID)
		}
		if rest, _ := f.runs.Notices(ctx, 10); len(rest) != 0 {
			t.Fatalf("redelivered notice stayed pending")
		}
		if f.tel.total(metricRetried) != 1 {
			t.Fatalf("retried count = %d, want 1", f.tel.total(metricRetried))
		}
	})

	t.Run("only leaves unlisted kinds pending", func(t *testing.T) {
		f := newFixture(t)
		seedFinish(t, f, "acme")
		d := New(f.runs, f.sink, nil, Only(types.NoticeSuspended), WithClock(func() time.Time { return *f.dispNow }))
		if err := d.Deliver(ctx); err != nil {
			t.Fatalf("Deliver() error = %v", err)
		}
		// The pass claimed the filtered notice without acknowledging it;
		// lapse the claim so the pending notice is offered again.
		*f.storeNow = f.storeNow.Add(2 * stores.LeaseTTL)
		if pending, _ := f.runs.Notices(ctx, 10); len(pending) != 1 {
			t.Fatalf("filtered notice left the outbox")
		}
		if len(f.sink.notices()) != 0 {
			t.Fatal("filtered notice reached the sink")
		}
	})

	t.Run("unrouted acknowledges and counts", func(t *testing.T) {
		f := newFixture(t)
		seedFinish(t, f, "acme")
		f.sink.fail = func(types.RunNotice) error { return webhook.ErrUnrouted }
		d := New(f.runs, f.sink, nil, WithTelemetry(f.tel), WithClock(func() time.Time { return *f.dispNow }))
		if err := d.Deliver(ctx); err != nil {
			t.Fatalf("Deliver() error = %v", err)
		}
		if rest, _ := f.runs.Notices(ctx, 10); len(rest) != 0 {
			t.Fatal("unrouted notice stayed pending")
		}
		if f.tel.total(metricUnrouted) != 1 || f.tel.total(metricDelivered) != 0 {
			t.Fatalf("unrouted = %d, delivered = %d", f.tel.total(metricUnrouted), f.tel.total(metricDelivered))
		}
	})
}

var errTransient = &timeoutError{}

type timeoutError struct{}

func (*timeoutError) Error() string { return "transport timeout" }

func TestEndpointIsolation(t *testing.T) {
	isolation(t, false)
}

func isolation(t *testing.T, _ bool) ([]*serverRec, *countingTel, string) {
	t.Helper()
	var (
		mu      sync.Mutex
		tenants []string
	)
	tenantsSeen := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), tenants...)
	}
	secrets := map[string]string{"t1": "secret-one", "t2": "secret-two"}
	recs := map[string]*serverRec{}
	urls := map[string]string{}
	for tenant, secret := range secrets {
		sr := &serverRec{secret: secret, hits: map[string][]string{}}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(r.Body)
			if !verifyNotice(t, secret, r, raw) {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			var body map[string]any
			_ = json.Unmarshal(raw, &body)
			sr.add(fmt.Sprint(body["tenant"]), string(raw))
			w.WriteHeader(http.StatusOK)
		}))
		t.Cleanup(server.Close)
		sr.server = server
		recs[tenant] = sr
		urls[tenant] = server.URL
	}
	t3 := &serverRec{secret: "secret-three", hits: map[string][]string{}}
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t3.add("t3", "hit")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(empty.Close)

	f := newFixture(t)
	endpoint := func(tenant string) (string, string, bool) {
		mu.Lock()
		tenants = append(tenants, tenant)
		mu.Unlock()
		url, ok := urls[tenant]
		return url, secrets[tenant], ok
	}
	client := egress.Client(egressAllowAll())
	sender := webhook.New(endpoint, client)
	seedFinish(t, f, "t1")
	seedFinish(t, f, "t2")
	seedFinish(t, f, "t3")
	d := New(f.runs, sender, client, WithTelemetry(f.tel), WithClock(func() time.Time { return *f.dispNow }))
	if err := d.Deliver(context.Background()); err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}

	for tenant, sr := range recs {
		hits := sr.snapshot(tenant)
		if len(hits) != 1 || !strings.Contains(hits[0], `"tenant":"`+tenant+`"`) {
			t.Fatalf("tenant %s received %v", tenant, hits)
		}
	}
	if hits := t3.snapshot("t3"); len(hits) != 0 {
		t.Fatalf("tenant without an endpoint received %v", hits)
	}
	seen := tenantsSeen()
	for _, tenant := range seen {
		if tenant != "t1" && tenant != "t2" && tenant != "t3" {
			t.Fatalf("registry consulted with %q", tenant)
		}
	}
	if f.tel.total(metricUnrouted) != 1 {
		t.Fatalf("unrouted count = %d, want 1", f.tel.total(metricUnrouted))
	}
	return nil, f.tel, ""
}

type serverRec struct {
	server *httptest.Server
	secret string
	mu     sync.Mutex
	hits   map[string][]string
}

func (s *serverRec) add(tenant, body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hits[tenant] = append(s.hits[tenant], body)
}

func (s *serverRec) snapshot(tenant string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.hits[tenant]...)
}

// verifyNotice recomputes the Standard Webhooks signature the way the
// tenant's receiver does.
func verifyNotice(t *testing.T, secret string, r *http.Request, body []byte) bool {
	t.Helper()
	ts, err := strconv.ParseInt(r.Header.Get("webhook-timestamp"), 10, 64)
	if err != nil {
		return false
	}
	return r.Header.Get("webhook-signature") == webhook.Sign(secret, r.Header.Get("webhook-id"), ts, body)
}

func TestGovernedEgress(t *testing.T) {
	f := newFixture(t)
	audit := stores.NewMemoryAuditLog()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := egress.Client(types.EgressPolicy{
		Schemes: []string{"http"},
		Allow:   []string{"example.com"},
	})
	sender := webhook.New(func(string) (string, string, bool) { return server.URL, "secret", true }, client)
	seedFinish(t, f, "acme")
	d := New(f.runs, sender, client, WithTelemetry(f.tel), WithClock(func() time.Time { return *f.dispNow }))
	ctx := gohan.WithDecisionAudit(context.Background(), audit)
	if err := d.Deliver(ctx); err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}

	var denied []stores.AuditRecord
	for rec, err := range audit.Read(ctx, "s-acme") {
		if err != nil {
			t.Fatalf("audit read: %v", err)
		}
		if rec.Kind == stores.AuditEgressDenied {
			denied = append(denied, rec)
		}
	}
	if len(denied) != 1 {
		t.Fatalf("egress_denied records = %d, want 1", len(denied))
	}
	rec := denied[0]
	if rec.Host != "127.0.0.1" || rec.Reason == "" {
		t.Fatalf("audit record = %+v, want host and reason only", rec)
	}
	for _, forbidden := range []string{server.URL, "http://", "secret"} {
		if strings.Contains(rec.Host, forbidden) || strings.Contains(rec.Reason, forbidden) {
			t.Fatalf("audit record leaks %q: %+v", forbidden, rec)
		}
	}
	if c, ok := f.tel.last(metricEgress); !ok ||
		c.attrs["tool"] != "notice" || c.attrs["host"] != "127.0.0.1" || c.attrs["decision"] != "denied" {
		t.Fatalf("egress metric = %#v", c)
	}
	if rest, _ := f.runs.Notices(ctx, 10); len(rest) != 0 {
		t.Fatal("refused notice stayed pending")
	}
	if f.tel.total(metricRetried) != 0 || f.tel.total(metricDead) != 0 {
		t.Fatalf("refusal retried %d times, dead %d; want none", f.tel.total(metricRetried), f.tel.total(metricDead))
	}
}
