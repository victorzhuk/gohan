package eino

import (
	"context"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"github.com/victorzhuk/gohan/core/types"
)

// The foreign-tool bridge presents an imported eino tool as a gohan
// types.Tool so the native runtime composes it exactly as it composes a
// native tool.
//
// Construction is NewForeignTool or NewForeignToolWithEnvelope:
//
//	func NewForeignTool(ctx context.Context, imported tool.InvokableTool, spec types.ToolSpec) (types.Tool, error)
//	func NewForeignToolWithEnvelope(ctx context.Context, imported tool.InvokableTool, spec types.ToolSpec) (types.Tool, error)
//
// spec is the full types.ToolSpec declaration; the bridge reports it from
// Spec() with one resolution applied: a zero Trust resolves to
// types.Untrusted, because imported tools are never trusted by default.
// The bridge reads no governance field (Effect, Risk, Capabilities,
// egress) out of the upstream schema.ToolInfo; every such field comes
// from the declared spec or not at all.
//
// The constructor calls Info(ctx) exactly once and fails on an error, a
// nil info, an empty name, or a name that disagrees with spec.Name, so a
// mis-declared bridge never reaches the governed batch.
//
// Result mapping is the construction choice. NewForeignTool is the
// conservative default: every upstream string becomes one Text block
// byte for byte and is never parsed. NewForeignToolWithEnvelope
// recognizes exactly one envelope: a JSON object whose single key is
// "gohan_tool_result" and whose value unmarshals into a types.ToolResult
// carrying at least one content block or an explicit outcome. Any other
// string stays one Text block and never fails the call. An error the
// imported tool returns surfaces unchanged, so errors.Is holds across
// the bridge. Every call runs through the governed tool chain the run
// composes.
const foreignToolResultKey = "gohan_tool_result"

type foreignToolMode int

const (
	foreignToolModeText foreignToolMode = iota
	foreignToolModeEnvelope
)

// NewForeignTool bridges an imported eino tool into the types.Tool port
// the governed native batch consumes. Every result string is carried
// verbatim as one Text block.
func NewForeignTool(ctx context.Context, imported tool.InvokableTool, spec types.ToolSpec) (types.Tool, error) {
	return newForeignTool(ctx, imported, spec, foreignToolModeText)
}

// NewForeignToolWithEnvelope bridges an imported eino tool and parses the
// declared gohan_tool_result envelope; every other string stays text.
func NewForeignToolWithEnvelope(ctx context.Context, imported tool.InvokableTool, spec types.ToolSpec) (types.Tool, error) {
	return newForeignTool(ctx, imported, spec, foreignToolModeEnvelope)
}

func newForeignTool(ctx context.Context, imported tool.InvokableTool, spec types.ToolSpec, mode foreignToolMode) (types.Tool, error) {
	if imported == nil {
		return nil, errors.New("bridge: foreign tool bridge without an imported tool")
	}
	info, err := imported.Info(ctx)
	if err != nil {
		return nil, fmt.Errorf("bridge: foreign tool %q: %w", spec.Name, err)
	}
	if info == nil {
		return nil, fmt.Errorf("bridge: foreign tool %q: nil tool info", spec.Name)
	}
	if info.Name == "" {
		return nil, fmt.Errorf("bridge: foreign tool %q: tool info declares no name", spec.Name)
	}
	if info.Name != spec.Name {
		return nil, fmt.Errorf("bridge: foreign tool declared as %q but the upstream tool info names %q", spec.Name, info.Name)
	}
	if spec.Trust == types.Trusted {
		spec.Trust = types.Untrusted
	}
	return &foreignTool{imported: imported, spec: spec, mode: mode}, nil
}

type foreignTool struct {
	imported tool.InvokableTool
	spec     types.ToolSpec
	mode     foreignToolMode
}

func (t *foreignTool) Spec() types.ToolSpec { return t.spec }

func (t *foreignTool) Call(ctx context.Context, args json.RawMessage) (types.ToolResult, error) {
	out, err := t.imported.InvokableRun(ctx, string(args))
	if err != nil {
		return types.ToolResult{}, err
	}
	if t.mode == foreignToolModeEnvelope {
		if res, ok := parseForeignEnvelope(out); ok {
			return res, nil
		}
	}
	return types.ToolResult{Content: []types.Block{types.Text{Text: out}}}, nil
}

// parseForeignEnvelope recognizes one envelope: a JSON object whose
// single key is gohan_tool_result and whose value unmarshals into a
// types.ToolResult with at least one content block or an explicit
// outcome. Any other string is not an envelope.
func parseForeignEnvelope(out string) (types.ToolResult, bool) {
	var obj map[string]json.RawMessage
	if err := jsonv2.Unmarshal([]byte(out), &obj); err != nil {
		return types.ToolResult{}, false
	}
	if len(obj) != 1 {
		return types.ToolResult{}, false
	}
	raw, ok := obj[foreignToolResultKey]
	if !ok || len(raw) == 0 {
		return types.ToolResult{}, false
	}
	var res types.ToolResult
	if err := jsonv2.Unmarshal(raw, &res); err != nil {
		return types.ToolResult{}, false
	}
	if len(res.Content) == 0 && res.Outcome == types.Succeeded {
		return types.ToolResult{}, false
	}
	return res, true
}
