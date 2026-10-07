package notify

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std/egress"
	"github.com/victorzhuk/gohan/std/notify/webhook"
)

func TestRunNotices(t *testing.T) {
	t.Run("streams.webhook-standard-signed", func(t *testing.T) {
		f := newFixture(t)
		var (
			mu   sync.Mutex
			hits []map[string]any
			ids  []string
			sts  []string
			sigs []string
		)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(r.Body)
			mu.Lock()
			hits = append(hits, map[string]any{
				"body":   string(raw),
				"secret": "tenant-secret",
			})
			ids = append(ids, r.Header.Get("webhook-id"))
			sts = append(sts, r.Header.Get("webhook-timestamp"))
			sigs = append(sigs, r.Header.Get("webhook-signature"))
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
		}))
		t.Cleanup(server.Close)

		client := egress.Client(egressAllowAll())
		sendClock := time.Unix(1_760_000_100, 0)
		sender := webhook.New(
			func(string) (string, string, bool) { return server.URL, "whsec_" + b64("tenant-secret"), true },
			client,
			webhook.WithClock(func() time.Time { return sendClock }),
		)
		notice := seedFinish(t, f, "acme")
		d := New(f.runs, sender, client, WithTelemetry(f.tel), WithClock(func() time.Time { return *f.dispNow }))
		if err := d.Deliver(context.Background()); err != nil {
			t.Fatalf("Deliver() error = %v", err)
		}

		mu.Lock()
		defer mu.Unlock()
		if len(hits) != 1 {
			t.Fatalf("deliveries = %d, want 1", len(hits))
		}
		if ids[0] != notice.ID {
			t.Fatalf("webhook-id = %q, want %q", ids[0], notice.ID)
		}
		if sts[0] != "1760000100" {
			t.Fatalf("webhook-timestamp = %q", sts[0])
		}
		var body map[string]any
		if err := json.Unmarshal([]byte(hits[0]["body"].(string)), &body); err != nil {
			t.Fatalf("body is not JSON: %v", err)
		}
		for _, field := range []string{"id", "kind", "tenant", "session_id", "run_id", "reason", "at"} {
			if _, ok := body[field]; !ok {
				t.Fatalf("body misses %q: %s", field, hits[0]["body"])
			}
		}
		ts, err := strconv.ParseInt(sts[0], 10, 64)
		if err != nil {
			t.Fatalf("webhook-timestamp: %v", err)
		}
		if sigs[0] != webhook.Sign("tenant-secret", notice.ID, ts, []byte(hits[0]["body"].(string))) {
			t.Fatal("receiver with the tenant secret rejected the signature")
		}
		if webhook.Sign("wrong-secret", notice.ID, ts, []byte(hits[0]["body"].(string))) == sigs[0] {
			t.Fatal("a wrong secret produced the accepted signature")
		}
		tampered := append([]byte(nil), []byte(hits[0]["body"].(string))...)
		tampered[len(tampered)-2] = '}'
		if webhook.Sign("tenant-secret", notice.ID, ts, tampered) == sigs[0] {
			t.Fatal("a tampered body verified against the delivered signature")
		}
		if f.tel.total(metricDelivered) != 1 {
			t.Fatalf("delivered count = %d, want 1", f.tel.total(metricDelivered))
		}
	})

	t.Run("streams.notice-retry-and-dead-letter", func(t *testing.T) {
		f := newFixture(t)
		var mu sync.Mutex
		attempts := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			mu.Lock()
			attempts++
			mu.Unlock()
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		t.Cleanup(server.Close)

		client := egress.Client(egressAllowAll())
		sender := webhook.New(func(string) (string, string, bool) { return server.URL, "whsec_" + b64("tenant-secret"), true }, client)
		notice := seedFinish(t, f, "acme")
		audit := stores.NewMemoryAuditLog()
		ctx := gohan.WithDecisionAudit(context.Background(), audit)
		d := New(f.runs, sender, client, WithTelemetry(f.tel), WithClock(func() time.Time { return *f.dispNow }))
		// The backoff doubles to the hourly cap, so the 72 hour budget
		// needs one pass per hour past the doubling ramp.
		for try := range 100 {
			*f.storeNow = f.storeNow.Add(2 * stores.LeaseTTL)
			*f.dispNow = f.dispNow.Add(min(RetryStart<<min(try, 12), RetryCap))
			if err := d.Deliver(ctx); err != nil {
				t.Fatalf("Deliver() error = %v", err)
			}
			if f.tel.total(metricDead) == 1 {
				break
			}
		}
		mu.Lock()
		gotAttempts := attempts
		mu.Unlock()
		if f.tel.total(metricDead) != 1 {
			t.Fatalf("notice never dead-lettered after %d attempts", gotAttempts)
		}
		if f.tel.total(metricDelivered) != 0 {
			t.Fatal("a 503 endpoint delivered")
		}
		if f.tel.total(metricRetried) != int64(gotAttempts-1) {
			t.Fatalf("retried = %d, attempts = %d", f.tel.total(metricRetried), gotAttempts)
		}
		if rest, _ := f.runs.Notices(context.Background(), 10); len(rest) != 0 {
			t.Fatal("dead notice stayed in the outbox")
		}
		var dead []stores.AuditRecord
		for rec, err := range audit.Read(ctx, "s-acme") {
			if err != nil {
				t.Fatalf("audit read: %v", err)
			}
			if rec.Kind == stores.AuditNoticeDead {
				dead = append(dead, rec)
			}
		}
		if len(dead) != 1 || dead[0].Reason != notice.ID {
			t.Fatalf("notice_dead records = %+v, want one carrying %q", dead, notice.ID)
		}
	})

	t.Run("suspension.approval-notice-delivered", func(t *testing.T) {
		suspensionApproval(t)
	})

	t.Run("identity.notice-endpoint-per-tenant", func(t *testing.T) {
		noticeEndpointPerTenant(t)
	})
}

// waitFor polls cond until it holds, so a detached drive's background
// steps are observed without sleeping a fixed duration.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// b64 encodes a raw secret the way the registry stores it behind whsec_.
func b64(raw string) string {
	return base64.StdEncoding.EncodeToString([]byte(raw))
}

func suspensionApproval(t *testing.T) {
	t.Helper()
	f := newFixture(t)
	f.sink = &recSink{}
	sink := f.sink
	book := &approvalTool{}
	model := &approvalModel{}
	runs := stores.NewMemoryRuns(
		stores.WithMemoryRunClock(time.Now),
		stores.WithMemoryRunInfo(func(ctx context.Context) (types.RunInfo, bool) {
			p, _ := types.PrincipalFrom(ctx)
			return types.RunInfo{Principal: p}, true
		}),
	)
	stack, err := gohan.Build(
		gohan.WithStores(stores.Stores{
			SessionLog:  stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)),
			Runs:        runs,
			Checkpoints: stores.NewMemoryCheckpoints(stores.WithMemoryCheckpointClock(time.Now), stores.WithMemoryCheckpointRunInfo(types.RunInfoFrom)),
		}),
		gohan.WithModels(model),
		gohan.WithApprovalPolicySource(approvalPolicySource{}),
		gohan.WithNotifier(sink),
		gohan.WithNativeAgent(gohan.NativeSpec{
			Request: gohan.FlowRequest{Name: "chat"},
			Profile: "native",
			Tools:   []types.Tool{book},
			Assemble: func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
				return types.ModelRequest{Messages: append(append([]types.Message{}, in.History...), in.Input...)}, nil
			},
		}),
	)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	conv, err := gohan.NewNativeConversation(stack, "chat",
		gohan.WithConversationRuns(runs),
		gohan.WithConversationEventLog(stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))),
		gohan.Detached(),
	)
	if err != nil {
		t.Fatalf("NewNativeConversation() error = %v", err)
	}
	ctx := types.WithPrincipal(context.Background(), types.Principal{Tenant: "acme", Subject: "u1"})
	msg := types.Message{ID: "u-1", Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "go"}}}
	done := make(chan error, 1)
	go func() {
		for _, err := range conv.Send(ctx, "s-acme", msg) {
			if err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Send() error = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Send() never returned")
	}
	// A detached run drives in the background; the suspension releases the
	// session lease, which is the signal the notice outbox now carries the
	// suspension notice.
	waitFor(t, "run suspended", func() bool {
		return !runs.SessionLeaseActive(ctx, "s-acme")
	})
	if book.ran() != 0 {
		t.Fatal("the unapproved side effect executed")
	}

	client := egress.Client(egressAllowAll())
	d := New(runs, sink, client, WithTelemetry(f.tel), WithClock(func() time.Time { return *f.dispNow }))
	if err := d.Deliver(context.Background()); err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}
	got := sink.notices()
	if len(got) != 1 {
		t.Fatalf("notices = %+v, want one suspension notice", got)
	}
	n := got[0]
	if n.Kind != types.NoticeSuspended || n.Tenant != "acme" || n.Reason != "HumanApproval" {
		t.Fatalf("notice = %+v, want suspended/HumanApproval for acme", n)
	}
	view, err := stack.Inspect(types.WithPrincipal(context.Background(), types.Principal{Tenant: "acme", Subject: "u1"}), n.RunID)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if view.Input == nil {
		t.Fatal("Inspect() returned no pending approval input")
	}
}

type approvalPolicySource struct{}

func (approvalPolicySource) ApprovalPolicy(context.Context, types.RiskTier, string, bool) (permission.ApprovalPolicy, error) {
	return permission.ApprovalPolicy{}, nil
}

// approvalTool is a SideEffect tool: the default Ask gate suspends the run
// before its first execution.
type approvalTool struct {
	mu   sync.Mutex
	runs int
}

func (a *approvalTool) Spec() types.ToolSpec {
	return types.ToolSpec{Name: "book", Effect: types.SideEffect}
}

func (a *approvalTool) Call(context.Context, json.RawMessage) (types.ToolResult, error) {
	a.mu.Lock()
	a.runs++
	a.mu.Unlock()
	return types.ToolResult{Content: []types.Block{types.Text{Text: "booked"}}, Outcome: types.Succeeded}, nil
}

func (a *approvalTool) ran() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.runs
}

// approvalModel scripts the turn: one SideEffect call on the first step,
// then the run closes once the call is answered.
type approvalModel struct{}

func (approvalModel) Profile() types.ModelProfile {
	return types.ModelProfile{Name: "native", Caps: types.Caps{Tools: true}}
}

func (approvalModel) Generate(_ context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	return func(yield func(types.ModelChunk, error) bool) {
		answered := 0
		for _, msg := range req.Messages {
			for _, b := range msg.Blocks {
				if _, ok := b.(types.ToolResult); ok {
					answered++
				}
			}
		}
		if answered == 0 {
			yield(types.ModelChunk{Kind: types.DeltaToolArgs, ToolUse: &types.ToolUse{ID: "c1", Name: "book", Args: json.RawMessage(`{"id":"b1"}`)}}, nil)
			yield(types.ModelChunk{Finish: types.FinishToolUse}, nil)
			return
		}
		yield(types.ModelChunk{Kind: types.DeltaText, Delta: "done"}, nil)
		yield(types.ModelChunk{Finish: types.FinishStop}, nil)
	}
}

func noticeEndpointPerTenant(t *testing.T) {
	t.Helper()
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
	stray := &serverRec{secret: "secret-three", hits: map[string][]string{}}
	unrouted := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stray.add("t3", "hit")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(unrouted.Close)

	f := newFixture(t)
	client := egress.Client(egressAllowAll())
	sender := webhook.New(func(tenant string) (string, string, bool) {
		url, ok := urls[tenant]
		return url, secrets[tenant], ok
	}, client)
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
	if hits := stray.snapshot("t3"); len(hits) != 0 {
		t.Fatalf("tenant without an endpoint received %v", hits)
	}
	if f.tel.total(metricUnrouted) != 1 {
		t.Fatalf("unrouted count = %d, want 1", f.tel.total(metricUnrouted))
	}
}
