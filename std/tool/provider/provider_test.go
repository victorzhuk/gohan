package provider

import (
	"context"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

func TestProviderToolDeclarations(t *testing.T) {
	t.Run("web-search-declaration", func(t *testing.T) {
		s := WebSearch().Spec()
		if s.Name != NameWebSearch || s.Effect != types.ReadOnly || s.Trust != types.Untrusted {
			t.Fatalf("spec = %+v, want ReadOnly Untrusted web_search", s)
		}
		if s.Executor != types.ByProvider || !s.Capabilities.Exfil {
			t.Fatalf("spec = %+v, want ByProvider with Exfil", s)
		}
		p := WebSearch().Policy()
		if p.Trust != types.Untrusted || p.MaxEffect != types.ReadOnly {
			t.Fatalf("policy = %+v, want Untrusted raised to ReadOnly", p)
		}
	})

	t.Run("web-search-domains", func(t *testing.T) {
		ws := WebSearch(WithDomains([]string{"example.com"}, []string{"internal.example"}))
		allow, deny := ws.Domains()
		if len(allow) != 1 || allow[0] != "example.com" || len(deny) != 1 || deny[0] != "internal.example" {
			t.Fatalf("domains = %v %v, want one allow and one deny entry", allow, deny)
		}
	})

	t.Run("code-exec-declaration", func(t *testing.T) {
		s := CodeExec().Spec()
		if s.Name != NameCodeExec || s.Effect != types.SideEffect || s.Trust != types.Untrusted {
			t.Fatalf("spec = %+v, want SideEffect Untrusted code_exec", s)
		}
		if s.Executor != types.ByProvider || s.Capabilities.Exfil {
			t.Fatalf("spec = %+v, want ByProvider without Exfil", s)
		}
		p := CodeExec().Policy()
		if p.Trust != types.Untrusted || p.MaxEffect != types.SideEffect {
			t.Fatalf("policy = %+v, want Untrusted raised to SideEffect", p)
		}
	})

	t.Run("hosted-mcp-declaration", func(t *testing.T) {
		m := HostedMCP("https://mcp.example/srv", "search_docs", "fetch_page")
		s := m.Spec()
		if s.Name != NameHostedMCP || s.Effect != types.SideEffect || s.Trust != types.Untrusted {
			t.Fatalf("spec = %+v, want SideEffect Untrusted hosted_mcp", s)
		}
		if s.Executor != types.ByProvider || !s.Capabilities.Exfil {
			t.Fatalf("spec = %+v, want ByProvider with Exfil", s)
		}
		url, allowed := m.Allowed()
		if url != "https://mcp.example/srv" || len(allowed) != 2 {
			t.Fatalf("allowed = %q %v, want server URL and two tool names", url, allowed)
		}
		p := m.Policy()
		if p.Trust != types.Untrusted || p.MaxEffect != types.SideEffect {
			t.Fatalf("policy = %+v, want Untrusted raised to SideEffect", p)
		}
	})

	t.Run("call-never-invoked", func(t *testing.T) {
		for _, tool := range []types.Tool{WebSearch(), CodeExec(), HostedMCP("https://mcp.example")} {
			if _, err := tool.Call(context.Background(), nil); err == nil {
				t.Fatalf("%s Call succeeded, want an error", tool.Spec().Name)
			}
		}
	})
}
