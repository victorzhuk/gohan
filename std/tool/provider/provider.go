// Package provider ships the provider-executed tools: the model provider
// runs them inside the model call, so Call is never invoked by the
// harness. Each tool registers through the ordinary registry with
// Executor ByProvider and carries its own trust policy that raises
// MaxEffect to the declared effect, so the import cap does not apply.
package provider

import (
	"context"
	"encoding/json"

	core "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/types"
)

// Tool names the provider-executed tools register under.
const (
	NameWebSearch = "web_search"
	NameCodeExec  = "code_exec"
	NameHostedMCP = "hosted_mcp"
)

type tool struct {
	spec   types.ToolSpec
	policy core.ToolPolicy
	allow  []string
	deny   []string
}

func (t *tool) Spec() types.ToolSpec { return t.spec }

// Call is never invoked for a provider-executed tool: the provider runs
// the call inside the model request. The error exists so the port is
// satisfied and a stray invocation is loud.
func (t *tool) Call(context.Context, json.RawMessage) (types.ToolResult, error) {
	return types.ToolResult{}, errProviderCall
}

var errProviderCall = &providerCallError{}

type providerCallError struct{}

func (*providerCallError) Error() string {
	return "gohan: provider-executed tool cannot be called by the harness"
}

// Policy returns the tool's own trust policy: Untrusted with MaxEffect
// raised to the declared effect.
func (t *tool) Policy() core.ToolPolicy { return t.policy }

// Option adjusts a provider tool at construction.
type Option func(*tool)

// WithDomains narrows the search tool to the given domain allow-list and
// deny-list. The provider enforces them; the harness carries them only
// into the declaration.
func WithDomains(allow []string, deny []string) Option {
	return func(t *tool) {
		t.allow = append([]string(nil), allow...)
		t.deny = append([]string(nil), deny...)
	}
}

// searchTool carries the search-specific accessors over the base tool.
type searchTool struct {
	tool
}

// Domains returns the domain allow-list and deny-list the adapter
// declares to the provider.
func (t *searchTool) Domains() (allow, deny []string) { return t.allow, t.deny }

// WebSearch registers the provider-run web search tool: ReadOnly,
// Untrusted, exfiltration-capable, with its own policy raising MaxEffect
// to ReadOnly.
func WebSearch(opts ...Option) *searchTool {
	t := &searchTool{tool: tool{
		spec: types.ToolSpec{
			Name:        NameWebSearch,
			Description: "Search the web. Runs at the model provider inside the model call.",
			Effect:      types.ReadOnly,
			Trust:       types.Untrusted,
			Executor:    types.ByProvider,
			Capabilities: types.Capabilities{
				Exfil: true,
			},
			MaxOutput: 32 << 10,
		},
		policy: core.ToolPolicy{Trust: types.Untrusted, MaxEffect: types.ReadOnly},
	}}
	for _, opt := range opts {
		opt(&t.tool)
	}
	return t
}

// CodeExec registers the provider-run code execution tool: SideEffect,
// Untrusted, with its own policy raising MaxEffect to SideEffect.
func CodeExec() *tool {
	return &tool{
		spec: types.ToolSpec{
			Name:        NameCodeExec,
			Description: "Run code in the provider's sandbox. Runs at the model provider inside the model call.",
			Effect:      types.SideEffect,
			Trust:       types.Untrusted,
			Executor:    types.ByProvider,
			MaxOutput:   32 << 10,
		},
		policy: core.ToolPolicy{Trust: types.Untrusted, MaxEffect: types.SideEffect},
	}
}

// mcpTool carries the allowed-tools list the adapter translates into the
// hosted MCP declaration.
type mcpTool struct {
	tool
	url     string
	allowed []string
}

// Allowed returns the hosted MCP server URL and the allowed tool names
// the adapter declares to the provider.
func (t *mcpTool) Allowed() (url string, allowed []string) { return t.url, t.allowed }

// HostedMCP registers a hosted MCP server tool: SideEffect, Untrusted,
// exfiltration-capable, with its own policy raising MaxEffect to
// SideEffect. The allowed list, when empty, admits every tool the
// server exposes.
func HostedMCP(url string, allowed ...string) *mcpTool {
	return &mcpTool{
		tool: tool{
			spec: types.ToolSpec{
				Name:        NameHostedMCP,
				Description: "Call tools on the hosted MCP server " + url + ". Runs at the model provider inside the model call.",
				Effect:      types.SideEffect,
				Trust:       types.Untrusted,
				Executor:    types.ByProvider,
				Capabilities: types.Capabilities{
					Exfil: true,
				},
				MaxOutput: 32 << 10,
			},
			policy: core.ToolPolicy{Trust: types.Untrusted, MaxEffect: types.SideEffect},
		},
		url:     url,
		allowed: append([]string(nil), allowed...),
	}
}
