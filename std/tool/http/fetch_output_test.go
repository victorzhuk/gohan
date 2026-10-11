package http_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std/egress"
	"github.com/victorzhuk/gohan/std/outputs"
	httpTool "github.com/victorzhuk/gohan/std/tool/http"
	"github.com/victorzhuk/gohan/testkit/gohantest"
)

// countTelemetry records every count by metric name so a scenario can
// assert on gohan.output.stored and gohan.sandbox.secret_leak.
type countTelemetry struct {
	mu     sync.Mutex
	counts map[string]int64
	attrs  map[string][]types.Attr
}

func newCountTelemetry() *countTelemetry {
	return &countTelemetry{counts: map[string]int64{}, attrs: map[string][]types.Attr{}}
}

func (t *countTelemetry) Count(_ context.Context, name string, value int64, attrs ...types.Attr) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.counts[name] += value
	if len(attrs) > 0 {
		t.attrs[name] = append(t.attrs[name], attrs...)
	}
}

func (t *countTelemetry) StartSpan(ctx context.Context, _ string, _ ...types.Attr) (context.Context, func(...types.Attr)) {
	return ctx, func(...types.Attr) {}
}

func (t *countTelemetry) Record(context.Context, string, float64, ...types.Attr) {}

func (t *countTelemetry) total(name string) int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.counts[name]
}

func fetchPolicy(maxBytes int64, timeout time.Duration) types.EgressPolicy {
	return types.EgressPolicy{
		Allow:         []string{"127.0.0.1"},
		Schemes:       []string{"http"},
		PrivateRanges: types.DenyPrivate,
		MaxBytes:      maxBytes,
		Timeout:       timeout,
	}
}

type fetchRun struct {
	text        string
	ref         string
	failureKind types.ErrorKind
}

// runFetchTool drives web_fetch over the public path: gohan.Build, the
// scripted native agent, one tool call, one Send.
func runFetchTool(t *testing.T, tool gohan.Tool, rawURL string, outputsStore stores.OutputStore, tel types.Telemetry) fetchRun {
	t.Helper()
	args, err := json.Marshal(httpTool.FetchInput{URL: rawURL})
	if err != nil {
		t.Fatalf("encode tool input: %v", err)
	}
	model := gohantest.NewScriptedModel(types.ModelProfile{Name: "scripted", Caps: types.Caps{Tools: true}}, gohantest.ToolCall("web_fetch", json.RawMessage(args)), gohantest.Text("finished"))
	storesCfg := stores.Stores{SessionLog: stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))}
	if outputsStore != nil {
		storesCfg.OutputStore = outputsStore
	}
	options := []gohan.Option{
		gohan.WithStores(storesCfg),
		gohan.WithModels(model),
		gohan.WithNativeAgent(gohan.NativeSpec{
			Request: gohan.FlowRequest{Name: "fetch-output"}, Profile: "scripted", Tools: []types.Tool{tool},
			Assemble: func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
				return types.ModelRequest{Messages: in.History}, nil
			},
		}),
		gohan.WithEgressClient(func(p types.EgressPolicy) *http.Client { return egress.Client(p) }),
	}
	if tel != nil {
		options = append(options, gohan.WithTelemetry(tel))
	}
	stack, err := gohan.Build(options...)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	conv, err := gohan.NewNativeConversation(stack, "fetch-output", gohan.WithConversationRuns(stores.NewMemoryRuns()), gohan.WithConversationEventLog(stores.NewMemoryEventLog()))
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	sessionID := "fetch-session"
	ctx := types.WithPrincipal(context.Background(), types.Principal{Tenant: "tenant", Subject: "subject"})
	if tel != nil {
		ctx = types.WithTelemetry(ctx, tel)
	}
	run := fetchRun{}
	var b strings.Builder
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
			run.ref = e.Result.Ref
			if e.Result.Error != nil {
				run.failureKind = e.Result.Error.Kind
				b.WriteString(e.Result.Error.Message)
			}
		}
	}
	run.text = b.String()
	return run
}

func readStored(t *testing.T, store stores.OutputStore, ref string) string {
	t.Helper()
	reader, err := outputs.NewReadOutput(store)
	if err != nil {
		t.Fatalf("create read_output: %v", err)
	}
	result, err := reader.Call(context.Background(), []byte(`{"ref":"`+ref+`"}`))
	if err != nil {
		t.Fatalf("read_output: %v", err)
	}
	if result.Error != nil {
		t.Fatalf("read_output failed: %s", result.Error.Message)
	}
	var b strings.Builder
	for _, block := range result.Content {
		if text, ok := block.(types.Text); ok {
			b.WriteString(text.Text)
		}
	}
	return b.String()
}

func TestFetchOutput(t *testing.T) {
	const cap = outputs.DefaultMaxOutput

	t.Run("tools.fetch-output-stored", func(t *testing.T) {
		body := strings.Repeat("x", cap)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(body))
		}))
		defer srv.Close()
		store := stores.NewMemoryOutputs()
		tel := newCountTelemetry()
		tool, err := httpTool.Fetch(fetchPolicy(1<<20, 0), httpTool.WithOutputStore(store, tel))
		if err != nil {
			t.Fatalf("create fetch tool: %v", err)
		}
		run := runFetchTool(t, tool, srv.URL, store, tel)
		if run.ref == "" {
			t.Fatalf("expected a stored ref, result text tail: %q", tail(run.text, 200))
		}
		if !strings.Contains(run.text, "HTTP 200 OK") {
			t.Fatalf("unexpected failure: %s", run.text)
		}
		if !strings.Contains(run.text, "truncated at") || !strings.Contains(run.text, "full output ref") {
			t.Fatalf("inline text lacks truncation marker: %q", tail(run.text, 200))
		}
		if len(run.text) > cap+200 {
			t.Fatalf("inline text %d bytes exceeds cap plus marker", len(run.text))
		}
		full := "HTTP 200 OK\n" + body
		if got := readStored(t, store, run.ref); got != full {
			t.Fatalf("stored document mismatch: got %d bytes, want %d", len(got), len(full))
		}
		if tel.total("gohan.output.stored") != 1 {
			t.Fatalf("gohan.output.stored = %d, want 1", tel.total("gohan.output.stored"))
		}
	})

	t.Run("tools.fetch-output-no-store", func(t *testing.T) {
		body := strings.Repeat("y", cap)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(body))
		}))
		defer srv.Close()
		tool, err := httpTool.Fetch(fetchPolicy(1<<20, 0))
		if err != nil {
			t.Fatalf("create fetch tool: %v", err)
		}
		run := runFetchTool(t, tool, srv.URL, nil, nil)
		if run.ref != "" {
			t.Fatalf("unexpected ref %q without a store", run.ref)
		}
		if !strings.Contains(run.text, "no output store configured") {
			t.Fatalf("inline text lacks no-store marker: %q", tail(run.text, 200))
		}
		if !strings.Contains(run.text, "12 bytes dropped") {
			t.Fatalf("marker does not name the dropped bytes: %q", tail(run.text, 200))
		}
		if len(run.text) > cap+200 {
			t.Fatalf("inline text %d bytes exceeds cap plus marker", len(run.text))
		}
	})

	t.Run("tools.fetch-options-compatible", func(t *testing.T) {
		body := "small body"
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(body))
		}))
		defer srv.Close()
		tool, err := httpTool.Fetch(fetchPolicy(1<<20, 0))
		if err != nil {
			t.Fatalf("create fetch tool with one argument: %v", err)
		}
		run := runFetchTool(t, tool, srv.URL, nil, nil)
		if want := "HTTP 200 OK\n" + body; !strings.HasPrefix(run.text, want) {
			t.Fatalf("rendered text = %q, want prefix %q", run.text, want)
		}
		if run.ref != "" {
			t.Fatalf("fitting response must stay inline without a ref, got %q", run.ref)
		}
	})
}

func TestFetchEgressLifetime(t *testing.T) {
	t.Run("tools.egress-body-read-bounded", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			<-r.Context().Done()
		}))
		defer srv.Close()
		tool, err := httpTool.Fetch(fetchPolicy(1<<20, 300*time.Millisecond))
		if err != nil {
			t.Fatalf("create fetch tool: %v", err)
		}
		start := time.Now()
		run := runFetchTool(t, tool, srv.URL, nil, nil)
		if elapsed := time.Since(start); elapsed > 5*time.Second {
			t.Fatalf("body read took %v, not bounded by the policy limit", elapsed)
		}
		if run.failureKind != types.Permanent {
			t.Fatalf("failure kind = %v, want %v (text: %q)", run.failureKind, types.Permanent, tail(run.text, 200))
		}
		if !strings.Contains(run.text, "timeout") {
			t.Fatalf("failure text does not name the timeout: %q", tail(run.text, 200))
		}
	})

	t.Run("tools.egress-per-request-transport-released", func(t *testing.T) {
		var mu sync.Mutex
		requests, conns, closedConns := 0, 0, 0
		srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			mu.Lock()
			requests++
			mu.Unlock()
			_, _ = w.Write([]byte("body"))
		}))
		srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
			mu.Lock()
			defer mu.Unlock()
			if s == http.StateNew {
				conns++
			}
			if s == http.StateClosed {
				closedConns++
			}
		}
		srv.Start()
		defer srv.Close()
		tool, err := httpTool.Fetch(fetchPolicy(1<<20, 0))
		if err != nil {
			t.Fatalf("create fetch tool: %v", err)
		}
		// Two requests in sequence over the public path; each owns its
		// transport, so no idle connection survives either response.
		for _, path := range []string{"/one", "/two"} {
			run := runFetchTool(t, tool, srv.URL+path, nil, nil)
			if !strings.Contains(run.text, "HTTP 200 OK") {
				t.Fatalf("request %s failed: %s", path, run.text)
			}
		}
		deadline := time.Now().Add(2 * time.Second)
		for {
			mu.Lock()
			requestsDone, connsDone, closedDone := requests, conns, closedConns
			mu.Unlock()
			if requestsDone == 2 && connsDone >= 2 && closedDone == connsDone {
				break
			}
			if time.Now().After(deadline) {
				mu.Lock()
				defer mu.Unlock()
				t.Fatalf("idle connection survived: %d of %d connections closed after %d requests", closedDone, connsDone, requestsDone)
			}
			time.Sleep(10 * time.Millisecond)
		}
	})
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
