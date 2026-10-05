package http_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
		gohan.WithEgressClient(func(p types.EgressPolicy) *http.Client { return egress.Client(p) }),
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
