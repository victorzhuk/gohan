package mcp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std"
)

type countingTelemetry struct {
	mu     sync.Mutex
	counts map[string]int64
}

func (t *countingTelemetry) StartSpan(ctx context.Context, _ string, _ ...types.Attr) (context.Context, func(...types.Attr)) {
	return ctx, func(...types.Attr) {}
}

func (t *countingTelemetry) Count(_ context.Context, name string, n int64, _ ...types.Attr) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.counts == nil {
		t.counts = map[string]int64{}
	}
	t.counts[name] += n
}

func (t *countingTelemetry) Record(context.Context, string, float64, ...types.Attr) {}

func (t *countingTelemetry) total(name string) int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.counts[name]
}

type fixture struct {
	mu    sync.Mutex
	tools []*mcp.Tool
}

func (f *fixture) setTools(tools ...*mcp.Tool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tools = tools
}

func (f *fixture) snapshot() []*mcp.Tool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*mcp.Tool(nil), f.tools...)
}

func (f *fixture) serve(t *testing.T) string {
	t.Helper()
	ts := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		srv := mcp.NewServer(&mcp.Implementation{Name: "fixture", Version: "0"}, nil)
		for _, tool := range f.snapshot() {
			srv.AddTool(tool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil
			})
		}
		return srv
	}, &mcp.StreamableHTTPOptions{Stateless: true})
	httpSrv := httptest.NewServer(ts)
	t.Cleanup(httpSrv.Close)
	return httpSrv.URL
}

func readOnlyTool(name string) *mcp.Tool {
	return &mcp.Tool{
		Name:        name,
		Description: "desc " + name,
		InputSchema: map[string]any{"type": "object"},
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}
}

func connect(t *testing.T, url string, opts ...ClientOption) *Set {
	t.Helper()
	opts = append(opts, WithCheckpoints(stores.NewMemoryCheckpoints(stores.WithMemoryCheckpointRunInfo(types.RunInfoFrom))))
	set, err := Client(url, opts...)
	if err != nil {
		t.Fatalf("Client: %v", err)
	}
	t.Cleanup(func() { _ = set.Close() })
	return set
}

func findTool(t *testing.T, tools []types.Tool, name string) types.ToolSpec {
	t.Helper()
	for _, tool := range tools {
		spec := tool.Spec()
		if spec.Name == name {
			return spec
		}
	}
	t.Fatalf("tool %s not found among %d", name, len(tools))
	return types.ToolSpec{}
}

func TestClient(t *testing.T) {
	t.Run("interop.client-deferred-registration", func(t *testing.T) {
		f := &fixture{tools: []*mcp.Tool{readOnlyTool("search"), readOnlyTool("get")}}
		set := connect(t, f.serve(t), Namespace("crm"))
		tools, err := set.Tools(context.Background())
		if err != nil {
			t.Fatalf("Tools: %v", err)
		}
		if len(tools) != 2 {
			t.Fatalf("got %d tools, want 2", len(tools))
		}
		for _, name := range []string{"crm__search", "crm__get"} {
			spec := findTool(t, tools, name)
			if !spec.Deferred {
				t.Errorf("%s Deferred = false", name)
			}
			if spec.Trust != types.Untrusted {
				t.Errorf("%s Trust = %v, want Untrusted", name, spec.Trust)
			}
		}
	})

	t.Run("interop.namespace-required-for-second-set", func(t *testing.T) {
		named := &fixture{tools: []*mcp.Tool{readOnlyTool("search")}}
		bare := &fixture{tools: []*mcp.Tool{readOnlyTool("search")}}
		setNamed := connect(t, named.serve(t), Namespace("crm"))
		setBare := connect(t, bare.serve(t))
		_, err := Assemble(setNamed, setBare)
		if err == nil {
			t.Fatal("Assemble: want error, got nil")
		}
		if !strings.Contains(err.Error(), setBare.url) {
			t.Errorf("error %q does not name url %s", err.Error(), setBare.url)
		}
		if !errors.Is(err, ErrNamespaceRequired) {
			t.Errorf("error %q is not ErrNamespaceRequired", err.Error())
		}
	})

	t.Run("interop.client-guarding-intact", func(t *testing.T) {
		f := &fixture{tools: []*mcp.Tool{readOnlyTool("search")}}
		set := connect(t, f.serve(t), Namespace("crm"),
			WithPolicy(gohan.ToolPolicy{Trust: types.Untrusted, MaxEffect: types.SideEffect}),
			WithRefreshInterval(50*time.Millisecond))
		tools, err := set.Tools(context.Background())
		if err != nil {
			t.Fatalf("Tools: %v", err)
		}
		if spec := findTool(t, tools, "crm__search"); spec.Effect != types.ReadOnly {
			t.Fatalf("Effect = %v, want ReadOnly", spec.Effect)
		}
		f.setTools(&mcp.Tool{
			Name:        "search",
			Description: "desc search",
			InputSchema: map[string]any{"type": "object"},
			Annotations: &mcp.ToolAnnotations{},
		})
		time.Sleep(150 * time.Millisecond)
		tools, err = set.Tools(context.Background())
		if err != nil {
			t.Fatalf("Tools after refresh: %v", err)
		}
		for _, tool := range tools {
			if spec := tool.Spec(); spec.Name == "crm__search" && spec.Effect > types.ReadOnly {
				t.Fatalf("refresh raised Effect to %v", spec.Effect)
			}
		}
	})

	t.Run("interop.annotations-never-raise", func(t *testing.T) {
		f := &fixture{tools: []*mcp.Tool{{
			Name:        "search",
			Description: "desc search",
			InputSchema: map[string]any{"type": "object"},
			Annotations: &mcp.ToolAnnotations{DestructiveHint: new(false), IdempotentHint: true},
		}}}
		set := connect(t, f.serve(t), Namespace("crm"))
		tools, err := set.Tools(context.Background())
		if err != nil {
			t.Fatalf("Tools: %v", err)
		}
		if spec := findTool(t, tools, "crm__search"); spec.Effect != types.ReadOnly {
			t.Fatalf("Effect = %v, want ReadOnly", spec.Effect)
		}
	})

	t.Run("interop.namespace-in-manifest", func(t *testing.T) {
		f := &fixture{tools: []*mcp.Tool{readOnlyTool("search"), readOnlyTool("get")}}
		url := f.serve(t)
		seed := connect(t, url, Namespace("crm"))
		tools, err := seed.Tools(context.Background())
		if err != nil {
			t.Fatalf("seed Tools: %v", err)
		}
		specs := make([]types.ToolSpec, 0, len(tools))
		for _, tool := range tools {
			specs = append(specs, tool.Spec())
		}
		manifest, err := std.MarshalManifest(specs)
		if err != nil {
			t.Fatalf("MarshalManifest: %v", err)
		}
		_ = seed.Close()

		f.setTools(readOnlyTool("find"), readOnlyTool("get"))
		tel := &countingTelemetry{}
		set := connect(t, url, Namespace("crm"), WithPinnedManifest(manifest), WithTelemetry(tel))
		tools, err = set.Tools(context.Background())
		if err != nil {
			t.Fatalf("Tools: %v", err)
		}
		for _, tool := range tools {
			if tool.Spec().Name == "crm__search" {
				t.Fatal("pinned crm__search still registered after rename")
			}
		}
		findTool(t, tools, "crm__find")
	})

	t.Run("interop.client-description-drift", func(t *testing.T) {
		f := &fixture{tools: []*mcp.Tool{readOnlyTool("search")}}
		tel := &countingTelemetry{}
		set := connect(t, f.serve(t), Namespace("crm"), WithTelemetry(tel), WithRefreshInterval(50*time.Millisecond))
		if _, err := set.Tools(context.Background()); err != nil {
			t.Fatalf("Tools: %v", err)
		}
		changed := readOnlyTool("search")
		changed.Description = "changed description"
		f.setTools(changed)
		time.Sleep(150 * time.Millisecond)
		tools, err := set.Tools(context.Background())
		if err != nil {
			t.Fatalf("Tools after refresh: %v", err)
		}
		for _, tool := range tools {
			if tool.Spec().Name == "crm__search" {
				t.Fatal("drifted crm__search still registered")
			}
		}
		if got := tel.total("gohan.tool.manifest_drift"); got != 1 {
			t.Fatalf("manifest_drift count = %d, want 1", got)
		}
	})

	t.Run("interop.catalog-ttl", func(t *testing.T) {
		f := &fixture{tools: []*mcp.Tool{readOnlyTool("search")}}
		set := connect(t, f.serve(t), Namespace("crm"), WithRefreshInterval(50*time.Millisecond))
		tools, err := set.Tools(context.Background())
		if err != nil {
			t.Fatalf("Tools: %v", err)
		}
		if len(tools) != 1 {
			t.Fatalf("got %d tools, want 1", len(tools))
		}
		f.setTools(readOnlyTool("search"), readOnlyTool("get"))
		time.Sleep(150 * time.Millisecond)
		tools, err = set.Tools(context.Background())
		if err != nil {
			t.Fatalf("Tools after interval: %v", err)
		}
		if len(tools) != 2 {
			t.Fatalf("got %d tools, want 2", len(tools))
		}
	})
}
