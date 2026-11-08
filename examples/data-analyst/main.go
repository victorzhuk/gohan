// Command data-analyst runs the offline data-analyst demo: a scripted
// model writes an extract into the session workspace, runs a warehouse
// query whose oversized output spills into the guarded output store, and
// suspends on the one call the demo policy asks about, the extract read.
// A second stack over the same durable surfaces approves and resumes the
// run, reads the extract back and pages the stored output. No network, no
// container runtime, no API keys.
package main

import (
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std"
	"github.com/victorzhuk/gohan/std/outputs"
	stdpermission "github.com/victorzhuk/gohan/std/permission"
	"github.com/victorzhuk/gohan/std/sandbox"
)

const (
	// flowName is the registered native flow and conversation name.
	flowName = "data-analyst"
	// sessionID names the demo session in the shared session log.
	sessionID = "analysis-1"
	// extractPath is where the model parks the extract in the workspace.
	extractPath = "extract.csv"
	// gatedTool is the one call the demo policy asks about; the approval
	// scope the shipped tier policy demands derives from its name.
	gatedTool = "read_file"
	// pageLimit bounds the demo page window over the stored output.
	pageLimit = 160
	// outputRefMarker is the spill marker text the sandbox tool set
	// appends once oversized output lands in the output store.
	outputRefMarker = "full output ref: "
)

// queryPrompt is the user turn that starts the analysis.
const queryPrompt = "Pull the top customers from the warehouse, keep the extract in the session workspace and summarize them."

// Report carries the observable evidence of one composed analysis run.
type Report struct {
	SessionID    string
	Turn1Events  []types.Event
	Turn2Events  []types.Event
	History      []types.Message
	Suspended    types.Suspended
	ResumeToken  types.ResumeToken
	WorkspaceRef types.WorkspaceRef
	WorkspaceGen int64
	OutputRef    string
	Page         string
	ToolSpecs    []types.ToolSpec
	Sandbox      *fakeSandbox
	Sessions     *stores.MemorySessionLog
	Checkpoints  *stores.MemoryCheckpoints
	Journal      stores.Journal
	Store        stores.OutputStore
	ReadOutput   types.Tool
	LeakCount    int64
}

// analysisEnv holds the durable surfaces of one composition.
type analysisEnv struct {
	Sandbox     *fakeSandbox
	Sessions    *stores.MemorySessionLog
	Checkpoints *stores.MemoryCheckpoints
	Journal     *stores.MemoryJournal
	Outputs     *stores.MemoryOutputs
	Redacting   *redactingOutputs
	Store       stores.OutputStore
	ReadOutput  types.Tool
	runs        *stores.MemoryRuns
}

// newAnalysisEnv builds the durable surfaces once: one offline sandbox
// scripted with the oversized query output, the shared in-memory stores,
// and the redacting output store every stack sees.
func newAnalysisEnv(root string) *analysisEnv {
	sb := newFakeSandbox(root)
	sb.scriptExec(types.ExecResult{ExitCode: 0, Stdout: queryStdout()})
	// Both conversations share one runs store: it backs the session
	// workspace owner guard, so the resumed run's lease stays visible to
	// the workspace publication the resumed turn finishes with.
	runs := stores.NewMemoryRuns()
	sessions := stores.NewMemorySessionLog(
		stores.WithSessionPrincipals(types.PrincipalFrom),
		stores.WithSessionWorkspaceOwnerGuard(runs),
	)
	blobStore := stores.NewMemoryOutputs()
	redacting := newRedactingOutputs(blobStore)
	return &analysisEnv{
		Sandbox:     sb,
		Sessions:    sessions,
		Checkpoints: stores.NewMemoryCheckpoints(),
		Journal:     stores.NewMemoryJournal(),
		Outputs:     blobStore,
		Redacting:   redacting,
		Store:       redacting,
		runs:        runs,
	}
}

// Analyze runs the whole composed demo against a temporary workspace root.
func Analyze(ctx context.Context) (Report, error) {
	root, err := os.MkdirTemp("", "data-analyst-example-")
	if err != nil {
		return Report{}, fmt.Errorf("create workspace root: %w", err)
	}
	defer func() { _ = os.RemoveAll(root) }()
	return newAnalysisEnv(root).Analyze(ctx)
}

// Analyze drives the composed run: stack one executes the write and the
// query and suspends on the read, the checkpoint is peeked for the
// published workspace, and stack two over the same durable surfaces
// approves and resumes the run.
func (e *analysisEnv) Analyze(ctx context.Context) (Report, error) {
	readOutput, err := outputs.NewReadOutput(e.Store)
	if err != nil {
		return Report{}, fmt.Errorf("build read_output: %w", err)
	}
	e.ReadOutput = readOutput
	tel := newCountTelemetry()

	policy := types.SandboxPolicy{
		Lifecycle: types.PerSession,
		Secrets:   []types.SecretRef{{Name: secretNameWarehouse, Env: "PGPASSWORD"}},
	}
	model := newScriptedModel(analysisTurns())

	handles, err := sandbox.NewHandles(e.Sandbox, policy, e.Sessions)
	if err != nil {
		return Report{}, fmt.Errorf("build coordinator: %w", err)
	}
	set, err := sandbox.New(e.Sandbox, policy,
		sandbox.WithCoordinator(handles),
		sandbox.WithOutputStore(e.Redacting),
	)
	if err != nil {
		return Report{}, fmt.Errorf("build sandbox tool set: %w", err)
	}
	conv, err := e.conversation(model, set, handles, policy, tel)
	if err != nil {
		return Report{}, err
	}

	rep := Report{SessionID: sessionID}
	ctx = types.WithPrincipal(ctx, analysisPrincipal())
	ctx = types.WithTelemetry(ctx, tel)
	for ev, err := range conv.Send(ctx, sessionID, types.Message{
		Role:   types.RoleUser,
		Blocks: []types.Block{types.Text{Text: queryPrompt}},
	}) {
		if err != nil {
			return rep, err
		}
		rep.Turn1Events = append(rep.Turn1Events, ev)
	}
	for _, ev := range rep.Turn1Events {
		if s, ok := ev.(types.Suspended); ok {
			rep.Suspended = s
			rep.ResumeToken = s.Token
		}
	}
	if rep.ResumeToken == "" {
		return rep, fmt.Errorf("turn 1 ended without a suspension")
	}
	if rep.Suspended.Reason != types.HumanApproval {
		return rep, fmt.Errorf("turn 1 suspended with reason %s, want %s", rep.Suspended.Reason, types.HumanApproval)
	}
	rep.OutputRef = storedOutputRef(rep.Turn1Events)
	if rep.OutputRef == "" {
		return rep, fmt.Errorf("turn 1 stored no output ref")
	}
	cp, err := e.Checkpoints.Peek(ctx, rep.ResumeToken)
	if err != nil {
		return rep, fmt.Errorf("peek checkpoint: %w", err)
	}
	rep.WorkspaceRef = cp.Workspace
	rep.WorkspaceGen = cp.WorkspaceGeneration

	adapter := e.Sandbox.newAdapter()
	resumeHandles, err := sandbox.NewHandles(adapter, policy, e.Sessions)
	if err != nil {
		return rep, fmt.Errorf("build resume coordinator: %w", err)
	}
	resumeSet, err := sandbox.New(adapter, policy,
		sandbox.WithCoordinator(resumeHandles),
		sandbox.WithOutputStore(e.Redacting),
	)
	if err != nil {
		return rep, fmt.Errorf("build resume tool set: %w", err)
	}
	resumeConv, err := e.conversation(model, resumeSet, resumeHandles, policy, tel)
	if err != nil {
		return rep, err
	}
	for ev, err := range resumeConv.Resume(types.WithPrincipal(ctx, approverPrincipal()), rep.ResumeToken, gohan.Approve()) {
		if err != nil {
			return rep, err
		}
		rep.Turn2Events = append(rep.Turn2Events, ev)
	}

	loaded, err := e.Sessions.Load(ctx, sessionID)
	if err != nil {
		return rep, fmt.Errorf("load session %s: %w", sessionID, err)
	}
	rep.History = loaded.Messages
	rep.ToolSpecs = toolSpecs(set.Tools())
	rep.Sandbox = e.Sandbox
	rep.Sessions = e.Sessions
	rep.Checkpoints = e.Checkpoints
	rep.Journal = e.Journal
	rep.Store = e.Store
	rep.ReadOutput = e.ReadOutput
	page, err := pageOutput(ctx, e.Store, e.ReadOutput, rep.OutputRef, &outputs.Span{Offset: 0, Limit: pageLimit})
	if err != nil {
		return rep, err
	}
	rep.Page = page
	rep.LeakCount = tel.leakCount()
	return rep, nil
}

// conversation builds one governed stack over the shared durable surfaces
// and returns its native conversation. Both stacks register the same
// flow, policy and scripted model instance, so the resumed turn re-runs
// the identical configuration on the second coordinator.
func (e *analysisEnv) conversation(model types.Model, set sandbox.ToolSet, handles *sandbox.Handles, policy types.SandboxPolicy, tel types.Telemetry) (gohan.Conversation, error) {
	stack, err := gohan.Build(append(std.Defaults(),
		gohan.WithStores(stores.Stores{
			SessionLog:  e.Sessions,
			Checkpoints: e.Checkpoints,
			Journal:     e.Journal,
			OutputStore: e.Redacting,
		}),
		gohan.WithModels(model),
		gohan.WithTelemetry(tel),
		gohan.WithSecretResolver(newFakeSecretResolver(fixtureSecretValue)),
		gohan.WithEgressClient(func(types.EgressPolicy) *http.Client { return http.DefaultClient }),
		gohan.WithApprovalPolicySource(stdpermission.PolicySource{}),
		gohan.WithSandboxRegistration(gohan.SandboxRegistration{Handles: handles, Policy: policy}),
		gohan.WithNativeAgent(gohan.NativeSpec{
			Request: gohan.FlowRequest{Name: flowName},
			Profile: "scripted",
			Assemble: func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
				return types.ModelRequest{Messages: slices.Clone(in.History)}, nil
			},
			Tools:     set.Tools(),
			ToolChain: newGuardChain(newRedactor()),
			Decider:   analysisDecider{},
		}),
	)...)
	if err != nil {
		return nil, err
	}
	return gohan.NewNativeConversation(stack, flowName,
		gohan.WithConversationRuns(e.runs),
		gohan.WithConversationEventLog(stores.NewMemoryEventLog()),
	)
}

// analysisTurns scripts the three model turns: the first writes the
// extract and runs the query in one batch, the second asks to read the
// extract back, and the third delivers the summary text.
func analysisTurns() map[int][]types.ModelChunk {
	write := types.ToolUse{
		ID:   "call_write_extract",
		Name: "write_file",
		Args: jsontext.Value(`{"path":"extract.csv","content":"id,region,revenue\n1,emea,1200.50\n2,apac,840.00\n3,amer,2210.75\n"}`),
	}
	query := types.ToolUse{
		ID:   "call_run_query",
		Name: "run_code",
		Args: jsontext.Value(`{"code":"warehouse-report --top-customers"}`),
	}
	read := types.ToolUse{
		ID:   "call_read_extract",
		Name: gatedTool,
		Args: jsontext.Value(`{"path":"extract.csv"}`),
	}
	return map[int][]types.ModelChunk{
		0: {
			{ToolUse: &write},
			{ToolUse: &query},
			{Finish: types.FinishToolUse},
		},
		1: {
			{ToolUse: &read},
			{Finish: types.FinishToolUse},
		},
		2: {
			{Kind: types.DeltaText, Delta: "Top customers pulled; the extract sits in the session workspace and the full query output is stored under its ref."},
			{Finish: types.FinishStop},
		},
	}
}

// queryStdout builds the deterministic warehouse query output: larger
// than outputs.DefaultMaxOutput so the run spills it into the output
// store, and carrying the injected secret plus plain identifying rows so
// both output guards mask something.
func queryStdout() []byte {
	header := fmt.Sprintf("warehouse_password=%s\n", fixtureSecretValue)
	row := []byte("7,jordan.reed@example.com,4111 1111 1111 1111,182.40\n")
	out := make([]byte, 0, 80*1024)
	out = append(out, header...)
	out = append(out, "customer_id,customer_email,card_number,total\n"...)
	for len(out) < 70*1024 {
		out = append(out, row...)
	}
	return out
}

// analysisDecider is the demo policy: every call passes except the
// extract read, which asks, so the run suspends on a human approval.
type analysisDecider struct{}

func (analysisDecider) Decide(_ context.Context, inv *permission.ToolInvocation) (types.Decision[permission.Verdict], error) {
	if inv.Spec.Name == gatedTool {
		return types.Decision[permission.Verdict]{Value: permission.Ask, Confidence: 1}, nil
	}
	return types.Decision[permission.Verdict]{Value: permission.Allow, Confidence: 1}, nil
}

// analysisPrincipal is the demo operator: session read and write, no
// approve scope, so the approval on the resumed turn cannot come from the
// originator.
func analysisPrincipal() types.Principal {
	return types.Principal{
		Tenant:  "local",
		Subject: "analyst",
		Scopes:  []string{types.ScopeSessionRead, types.ScopeSessionWrite},
	}
}

// approverPrincipal is the second human the resumed turn rides on: a
// subject distinct from the originator, holding the session access the
// resume authority check requires plus the approve scope the shipped tier
// policy demands for the gated tool.
func approverPrincipal() types.Principal {
	return types.Principal{
		Tenant:  "local",
		Subject: "supervisor",
		Scopes:  []string{types.ScopeSessionRead, types.ScopeSessionWrite, "approve:" + gatedTool},
	}
}

// storedOutputRef walks the streamed events for the spill marker the
// sandbox tool set appends to oversized output and returns the last ref
// it named.
func storedOutputRef(events []types.Event) string {
	ref := ""
	for _, ev := range events {
		fin, ok := ev.(types.ToolFinished)
		if !ok {
			continue
		}
		for _, b := range fin.Result.Content {
			txt, ok := b.(types.Text)
			if !ok {
				continue
			}
			i := strings.Index(txt.Text, outputRefMarker)
			if i < 0 {
				continue
			}
			rest := txt.Text[i+len(outputRefMarker):]
			if end := strings.IndexByte(rest, ']'); end >= 0 {
				ref = rest[:end]
			}
		}
	}
	return ref
}

// toolSpecs lists the specs a tool set registered with the flow.
func toolSpecs(tools []types.Tool) []types.ToolSpec {
	specs := make([]types.ToolSpec, 0, len(tools))
	for _, t := range tools {
		specs = append(specs, t.Spec())
	}
	return specs
}

// pageArgs is the read_output argument shape the paging helper sends.
type pageArgs struct {
	Ref   string        `json:"ref"`
	Range *outputs.Span `json:"range,omitempty"`
}

// pageOutput refuses an out-of-range window against the stored body and
// delegates the in-range read to the shipped read_output tool value, so
// the demo pages a stored ref without registering the reserved harness
// tool name as a flow tool.
func pageOutput(ctx context.Context, store stores.OutputStore, tool types.Tool, ref string, span *outputs.Span) (string, error) {
	data, err := store.Get(ctx, ref)
	if err != nil {
		return "", fmt.Errorf("page output %s: %w", ref, err)
	}
	var msg types.Message
	if err := json.Unmarshal(data, &msg); err != nil {
		return "", fmt.Errorf("page output %s: decode stored blocks: %w", ref, err)
	}
	var body strings.Builder
	for _, b := range msg.Blocks {
		if txt, ok := b.(types.Text); ok {
			body.WriteString(txt.Text)
		}
	}
	total := int64(body.Len())
	if span != nil && (span.Offset < 0 || span.Offset > total || (span.Limit > 0 && span.Offset+span.Limit > total)) {
		return "", fmt.Errorf("page output %s: window offset %d limit %d outside stored body of %d bytes", ref, span.Offset, span.Limit, total)
	}
	args, err := json.Marshal(pageArgs{Ref: ref, Range: span})
	if err != nil {
		return "", fmt.Errorf("page output %s: encode arguments: %w", ref, err)
	}
	res, err := tool.Call(ctx, args)
	if err != nil {
		return "", fmt.Errorf("page output %s: %w", ref, err)
	}
	var page strings.Builder
	for _, b := range res.Content {
		if txt, ok := b.(types.Text); ok {
			page.WriteString(txt.Text)
		}
	}
	return page.String(), nil
}

func main() {
	rep, err := Analyze(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, "data-analyst:", err)
		os.Exit(1)
	}
	fmt.Printf("query: %s\n", queryPrompt)
	fmt.Printf("extract: %s (workspace %s, generation %d)\n", extractPath, rep.WorkspaceRef, rep.WorkspaceGen)
	fmt.Printf("stored output ref: %s\n", rep.OutputRef)
	fmt.Printf("paged output bytes [0, %d):\n%s\n", pageLimit, rep.Page)
}
