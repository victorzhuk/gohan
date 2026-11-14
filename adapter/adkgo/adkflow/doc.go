// Package adkflow presents google.golang.org/adk/v2 workflow graphs as
// governed gohan flows.
//
// The builders are the only supported entry into an adk-go workflow. They
// accept governed gohan components and nothing else:
//
//	func ModelNode(m gohan_types.Model) (workflow.Node, error)
//	func ToolsNode(spec gohan_types.ToolSpec, tools ...gohan_types.Tool) (workflow.Node, error)
//
// ModelNode builds a node whose work is one governed model call. The model
// value owns its request assembly and its canonical chain, so the guard the
// component already carries stays inside the node and the caller's ctx
// reaches the governed Generate unchanged. ToolsNode builds a node that
// runs the named governed tool through its governed Call, so a tool round
// trip inside a graph traverses exactly the chains the native runtime runs.
//
// FromWorkflow presents a constructed *workflow.Workflow as a gohan Flow:
//
//	func FromWorkflow[In, Out any](stack *gohan.Stack, wf *workflow.Workflow) gohan.Flow[In, Out]
//
// The flow declares runtime.GranularityTurn: one Step is one whole graph
// run, so gohan.Drive owns the limits, the events, the journal, checkpoint
// publication, preemption and Done. The stack parameter is kept for
// registration symmetry with agent.New; the flow itself drives through
// gohan.Drive and needs no stack state. A nil stack yields a standalone
// flow.
//
// Graphs run only through a real adk invocation. Every run wraps the
// workflow in agent.New(agent.Config{Name: "adkflow", Run: wf-shaped}) and
// drives it through runner.NewInMemory, so the runner builds the genuine
// InvocationContext the workflow engine requires. Nothing in this package
// fabricates an agent.InvocationContext, agent.Context or
// agent.ReadonlyContext: the governed nodes receive the runner's context as
// their agent.Context parameter and must pass it unchanged to the governed
// components, which keeps the run-scoped sink and the limits ledger live.
//
// Check reports the nodes a workflow runs outside the builders:
//
//	func Check(wf *workflow.Workflow) []string
//
// The adk workflow engine exposes no introspection over a constructed
// *workflow.Workflow, so Check audits by execution through a real adk
// invocation: it runs the graph once and reads the node path every event
// carries. Check audits executed nodes only. A node the package did not
// build is reported by name; the probe observes only nodes that actually
// run, so a routed branch the probe does not take stays unreported. The
// probe invokes governed components with empty requests, so point it at
// graphs whose components tolerate a probe run. Nothing is refused
// silently, and the builders remain the only way to place governed
// components.
package adkflow
