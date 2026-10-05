package gohan

import (
	"context"

	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/types"
)

// NativeSpec is the driver-supplied definition of one native flow: what it
// requests at build time, which profile it runs on, and the chains it
// executes. The flow name is Request.Name. The tool list has one source:
// Request.Tools is derived from Tools during resolution, never supplied
// independently.
type NativeSpec struct {
	Request     FlowRequest
	Profile     string
	Instruction []types.Block
	Tools       []types.Tool
	Assemble    func(ctx context.Context, in types.AssembleInput) (types.ModelRequest, error)
	ModelChain  chains.ModelChain
	ToolChain   chains.ToolChain
}

// WithNativeAgent registers a native flow definition before Build runs, so
// Build resolves it with everything else. Registering two definitions with
// the same flow name fails the build.
func WithNativeAgent(spec NativeSpec) Option {
	return func(c *config) error {
		c.native = append(c.native, spec)
		return nil
	}
}
