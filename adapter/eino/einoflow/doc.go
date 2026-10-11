// Package einoflow presents cloudwego/eino graphs as governed gohan flows.
//
// The builders are the only supported entry into an eino graph. They accept
// governed gohan components and nothing else:
//
//	func ModelNode(m gohan_types.Model) model.ToolCallingChatModel
//	func ToolsNode(ctx context.Context, tools ...gohan_types.Tool) (*compose.ToolsNode, error)
//
// ModelNode bridges a governed gohan model into the eino chat model port.
// The bridge is non-mutating: WithTools records nothing, because the
// governed model owns its request assembly and the tool inventory binds at
// the tools node, not at the model. ToolsNode composes every tool through
// its governed Call — the canonical tool chain and the guards the component
// already carries — so a tool round trip inside a graph traverses exactly
// the chains the native runtime runs.
//
// FromRunnable presents a compiled eino compose.Runnable[In, Out] as a
// gohan Flow:
//
//	func FromRunnable[In, Out any](stack *gohan.Stack, r compose.Runnable[In, Out], opts ...Option) gohan.Flow[In, Out]
//
// The flow declares runtime.GranularityTurn: one Step is one whole graph
// run, so gohan.Drive owns the limits, the events, the journal, checkpoint
// publication, preemption and Done. The graph runtime declares the graph
// flow kind (stores.FlowGraph) and its checkpoint envelope version, so the
// harness resume plan can refuse a foreign BackendVersion instead of
// replaying a compiled shape the running adapter can no longer decode. The
// stack parameter is kept for registration symmetry with agent.New; the
// flow itself drives through gohan.Drive and needs no stack state. A nil
// stack yields a standalone flow.
//
// Check reports a component a graph carries outside the builders:
//
//	func Check(nodes ...any) []Issue
//
// Pass the node values the graph was built with. A chat model that did not
// come from ModelNode is reported. eino v0.9 exposes no introspection over
// a *compose.ToolsNode instance, so tools nodes are skipped where
// introspection is unavailable; nothing is refused silently, and the
// builders remain the only way to place governed components.
package einoflow
