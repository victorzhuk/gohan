package adkgo

import (
	"context"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"

	"github.com/victorzhuk/gohan/core/types"
)

// The foreign-tool bridge presents an imported adk-go tool as a gohan
// types.Tool so the native runtime composes it exactly as it composes a
// native tool.
//
// Construction is NewForeignTool or NewForeignToolWithEnvelope:
//
//	func NewForeignTool(ctx context.Context, imported tool.Tool, spec types.ToolSpec) (types.Tool, error)
//	func NewForeignToolWithEnvelope(ctx context.Context, imported tool.Tool, spec types.ToolSpec) (types.Tool, error)
//
// adk's tool.Tool exposes only Name, Description and IsLongRunning, so the
// bridge calls the imported tool through the runnable shape the adk
// function tools implement and refuses anything that cannot run.
//
// spec is the full types.ToolSpec declaration; the bridge reports it from
// Spec() with one resolution applied: a zero Trust resolves to
// types.Untrusted, because imported tools are never trusted by default.
// The bridge reads no governance field (Effect, Risk, Capabilities,
// egress) out of the upstream tool; every such field comes from the
// declared spec or not at all.
//
// The constructor validates the imported value, the declared name and the
// runnable assertion, so a mis-declared bridge never reaches the governed
// batch.
//
// Result mapping is the construction choice. NewForeignTool is the
// conservative default: the upstream result map becomes one Text block
// byte for byte and is never parsed. NewForeignToolWithEnvelope
// recognizes exactly one envelope: a result map whose single key is
// "gohan_tool_result" and whose value unmarshals into a types.ToolResult
// carrying at least one content block or an explicit outcome. Any other
// map stays one Text block and never fails the call. An error the
// imported tool returns surfaces unchanged, so errors.Is holds across the
// bridge. Every call passes the caller's ctx unchanged.
const foreignToolResultKey = "gohan_tool_result"

// runnable is the exported method shape adk function tools implement.
type runnable interface {
	Run(ctx agent.Context, args any) (map[string]any, error)
}

type foreignToolMode int

const (
	foreignToolModeText foreignToolMode = iota
	foreignToolModeEnvelope
)

// NewForeignTool bridges an imported adk-go tool into the types.Tool port
// the governed native batch consumes. Every upstream result map is carried
// verbatim as one Text block.
func NewForeignTool(ctx context.Context, imported tool.Tool, spec types.ToolSpec) (types.Tool, error) {
	return newForeignTool(ctx, imported, spec, foreignToolModeText)
}

// NewForeignToolWithEnvelope bridges an imported adk-go tool and parses the
// declared gohan_tool_result envelope; every other result stays text.
func NewForeignToolWithEnvelope(ctx context.Context, imported tool.Tool, spec types.ToolSpec) (types.Tool, error) {
	return newForeignTool(ctx, imported, spec, foreignToolModeEnvelope)
}

func newForeignTool(ctx context.Context, imported tool.Tool, spec types.ToolSpec, mode foreignToolMode) (types.Tool, error) {
	if imported == nil {
		return nil, errors.New("bridge: foreign tool bridge without an imported tool")
	}
	if spec.Name == "" {
		return nil, errors.New("bridge: foreign tool bridge with an empty declared name")
	}
	if imported.Name() != spec.Name {
		return nil, fmt.Errorf("bridge: foreign tool declared as %q but the upstream tool names %q", spec.Name, imported.Name())
	}
	run, ok := imported.(runnable)
	if !ok {
		return nil, fmt.Errorf("bridge: foreign tool %q does not support Run", spec.Name)
	}
	if spec.Trust == types.Trusted {
		spec.Trust = types.Untrusted
	}
	return &foreignTool{imported: run, spec: spec, mode: mode}, nil
}

type foreignTool struct {
	imported runnable
	spec     types.ToolSpec
	mode     foreignToolMode
}

func (t *foreignTool) Spec() types.ToolSpec { return t.spec }

// ErrAdkContextRequired reports a foreign adk tool called outside a live
// adk invocation, where no genuine agent.Context exists.
var ErrAdkContextRequired = errors.New("gohan: adk tool requires a runner-supplied agent context")

// NewAdkTool exposes a governed gohan tool as an adk tool.Tool an
// llmagent can call. Identity and declaration come from gov.Spec(); every
// call runs the governed Call, so gate and decorators stay in the path.
func NewAdkTool(gov types.Tool) (tool.Tool, error) {
	if gov == nil {
		return nil, errors.New("bridge: adk tool without a governed tool")
	}
	spec := gov.Spec()
	if spec.Name == "" {
		return nil, errors.New("bridge: adk tool with an empty declared name")
	}
	if len(spec.Schema) == 0 {
		return nil, fmt.Errorf("bridge: adk tool %q without a declared schema", spec.Name)
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(spec.Schema, &schema); err != nil {
		return nil, fmt.Errorf("bridge: adk tool %q: decode declared schema: %w", spec.Name, err)
	}
	cfg := functiontool.Config{
		Name:        spec.Name,
		Description: spec.Description,
		InputSchema: &schema,
	}
	return functiontool.New[map[string]any, map[string]any](cfg, func(ac agent.Context, args map[string]any) (map[string]any, error) {
		raw, err := json.Marshal(args)
		if err != nil {
			return nil, fmt.Errorf("bridge: adk tool %q: encode arguments: %w", spec.Name, err)
		}
		res, err := gov.Call(ac, raw)
		if err != nil {
			return nil, err
		}
		enc, err := json.Marshal(res)
		if err != nil {
			return nil, fmt.Errorf("bridge: adk tool %q: encode result: %w", spec.Name, err)
		}
		return map[string]any{foreignToolResultKey: json.RawMessage(enc)}, nil
	})
}

func (t *foreignTool) Call(ctx context.Context, args json.RawMessage) (types.ToolResult, error) {
	actx, ok := ctx.(agent.Context)
	if !ok {
		return types.ToolResult{}, fmt.Errorf("bridge: foreign tool %q: %w", t.spec.Name, ErrAdkContextRequired)
	}
	var in any
	if len(args) > 0 {
		if err := json.Unmarshal(args, &in); err != nil {
			return types.ToolResult{}, fmt.Errorf("bridge: foreign tool %q: decode arguments: %w", t.spec.Name, err)
		}
	}
	out, err := t.imported.Run(actx, in)
	if err != nil {
		return types.ToolResult{}, err
	}
	if t.mode == foreignToolModeEnvelope {
		if res, ok := parseForeignEnvelope(out); ok {
			return res, nil
		}
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return types.ToolResult{}, fmt.Errorf("bridge: foreign tool %q: encode result: %w", t.spec.Name, err)
	}
	return types.ToolResult{Content: []types.Block{types.Text{Text: string(raw)}}}, nil
}

// parseForeignEnvelope recognizes one envelope: a result map whose single
// key is gohan_tool_result and whose value unmarshals into a
// types.ToolResult with at least one content block or an explicit outcome.
// Any other map is not an envelope.
func parseForeignEnvelope(out map[string]any) (types.ToolResult, bool) {
	if out == nil || len(out) != 1 {
		return types.ToolResult{}, false
	}
	raw, ok := out[foreignToolResultKey]
	if !ok || raw == nil {
		return types.ToolResult{}, false
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return types.ToolResult{}, false
	}
	if string(encoded) == "null" {
		return types.ToolResult{}, false
	}
	var res types.ToolResult
	if err := jsonv2.Unmarshal(encoded, &res); err != nil {
		return types.ToolResult{}, false
	}
	if len(res.Content) == 0 && res.Outcome == types.Succeeded {
		return types.ToolResult{}, false
	}
	return res, true
}
