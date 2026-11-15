package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/victorzhuk/gohan/adapter/mcp/internal/schema"
	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

const defaultRequestBudget = 30 * time.Second

// taskLauncher launches a durable task row for a non-input suspension.
// tasksserver.go implements it; server.go calls it when wired.
type taskLauncher interface {
	launch(ctx context.Context, tool string, s *types.SuspendError) (TaskRef, error)
	start(ctx context.Context, tool string, fn func(ctx context.Context) error) (TaskRef, func(ctx context.Context) error, error)
}

type ServerOption func(*serverConfig)

type ExposeOption func(*exposedFlow)

type serverConfig struct {
	principal func(*http.Request) (types.Principal, error)
	runs      stores.Runs
	events    stores.EventLog
	budget    time.Duration
	flows     []exposedFlow
	launcher  taskLauncher
}

type exposedFlow struct {
	name         string
	inputSchema  json.RawMessage
	outputSchema json.RawMessage
	tasks        bool
	invoke       func(ctx context.Context, args json.RawMessage) (any, error)
	resume       func(ctx context.Context, token types.ResumeToken, in stores.ResumeInput) (any, error)
}

// Expose exposes a gohan flow as one MCP tool.
func Expose[In, Out any](name string, flow gohan.Flow[In, Out], opts ...ExposeOption) ServerOption {
	ef := exposedFlow{name: name}
	for _, opt := range opts {
		opt(&ef)
	}
	ef.invoke = func(ctx context.Context, args json.RawMessage) (any, error) {
		var in In
		if len(args) > 0 {
			if err := json.Unmarshal(args, &in); err != nil {
				return nil, fmt.Errorf("mcp: decode tool input: %w", err)
			}
		}
		out, err := flow.Invoke(ctx, in)
		if err != nil {
			return nil, err
		}
		return out, nil
	}
	ef.resume = func(ctx context.Context, token types.ResumeToken, in stores.ResumeInput) (any, error) {
		return flow.Resume(ctx, token, in)
	}
	return func(c *serverConfig) {
		c.flows = append(c.flows, ef)
	}
}

// InputSchema sets the JSON schema the tool input must satisfy.
func InputSchema(s json.RawMessage) ExposeOption {
	return func(ef *exposedFlow) { ef.inputSchema = s }
}

// OutputSchema sets the JSON schema the tool output satisfies.
func OutputSchema(s json.RawMessage) ExposeOption {
	return func(ef *exposedFlow) { ef.outputSchema = s }
}

// Tasks marks an exposed flow as task-capable for non-input suspensions.
func Tasks() ExposeOption {
	return func(ef *exposedFlow) { ef.tasks = true }
}

// WithPrincipalSource sets the transport resolver for the calling principal.
// Required.
func WithPrincipalSource(fn func(*http.Request) (types.Principal, error)) ServerOption {
	return func(c *serverConfig) { c.principal = fn }
}

// WithStores sets the run and event stores backing task rows. Required.
func WithStores(runs stores.Runs, events stores.EventLog) ServerOption {
	return func(c *serverConfig) { c.runs = runs; c.events = events }
}

// WithRequestBudget bounds each tool invocation. Default 30s.
func WithRequestBudget(d time.Duration) ServerOption {
	return func(c *serverConfig) { c.budget = d }
}

// NewHandler builds the stateless MCP streamable HTTP handler.
func NewHandler(opts ...ServerOption) (http.Handler, error) {
	var cfg serverConfig
	cfg.budget = defaultRequestBudget
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.principal == nil {
		return nil, errors.New("mcp: WithPrincipalSource is required")
	}
	if cfg.runs == nil || cfg.events == nil {
		return nil, errors.New("mcp: WithStores is required")
	}
	launcher := newTaskLauncher(cfg.runs, cfg.events)
	srv := mcp.NewServer(&mcp.Implementation{Name: "gohan", Version: "v0.0.0"}, nil)
	if err := registerTasksGet(srv, launcher); err != nil {
		return nil, err
	}
	h := &handler{cfg: cfg, server: srv, launcher: launcher}
	for _, ef := range cfg.flows {
		flow := ef
		inputSchema := any(json.RawMessage(`{"type":"object"}`))
		if len(flow.inputSchema) > 0 {
			inputSchema = json.RawMessage(flow.inputSchema)
		}
		var outputSchema any
		if len(flow.outputSchema) > 0 {
			outputSchema = json.RawMessage(flow.outputSchema)
		}
		srv.AddTool(&mcp.Tool{
			Name:         flow.name,
			InputSchema:  inputSchema,
			OutputSchema: outputSchema,
		}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return h.call(ctx, flow, req)
		})
	}
	inner := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, &mcp.StreamableHTTPOptions{Stateless: true})
	return &principalHandler{cfg: cfg, inner: inner, launcher: launcher}, nil
}

// TaskStatus maps a run state to the tasks/get status vocabulary.
func TaskStatus(state stores.RunState) string {
	switch state {
	case stores.Finished:
		return "completed"
	case stores.Failed:
		return "failed"
	default:
		return "working"
	}
}

type handler struct {
	cfg      serverConfig
	server   *mcp.Server
	launcher *taskLauncherImpl
}

type principalHandler struct {
	cfg      serverConfig
	inner    http.Handler
	launcher *taskLauncherImpl
}

// tasksLauncher returns the launcher wired into this handler.
func (ph *principalHandler) tasksLauncher() *taskLauncherImpl {
	return ph.launcher
}

func (ph *principalHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p, err := ph.cfg.principal(r)
	if err != nil {
		writeJSONRPCError(w, r, err)
		return
	}
	ph.inner.ServeHTTP(w, r.WithContext(types.WithPrincipal(r.Context(), p)))
}

func writeJSONRPCError(w http.ResponseWriter, r *http.Request, err error) {
	var id json.RawMessage
	var body struct {
		ID json.RawMessage `json:"id"`
	}
	if r.Method == http.MethodPost && r.Body != nil {
		if raw, rerr := readLimited(r); rerr == nil {
			_ = json.Unmarshal(raw, &body)
		}
	}
	if len(body.ID) > 0 {
		id = body.ID
	} else {
		id = json.RawMessage("null")
	}
	resp, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error":   map[string]any{"code": -32000, "message": err.Error()},
	})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write(resp)
}

func readLimited(r *http.Request) ([]byte, error) {
	const limit = 1 << 20
	buf := make([]byte, 0, 512)
	chunk := make([]byte, 4096)
	for len(buf) < limit {
		n, err := r.Body.Read(chunk)
		buf = append(buf, chunk[:n]...)
		if err != nil {
			return buf, nil
		}
	}
	return buf, nil
}

func (h *handler) call(ctx context.Context, ef exposedFlow, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx, cancel := context.WithTimeout(ctx, h.cfg.budget)
	defer cancel()

	if state := resumeState(req); state != "" {
		return h.resume(ctx, ef, req, state)
	}
	return h.invoke(ctx, ef, req)
}

func resumeState(req *mcp.CallToolRequest) string {
	if req.Params.RequestState != "" {
		return req.Params.RequestState
	}
	if raw, err := json.Marshal(req.Params.Meta); err == nil && len(raw) > 0 {
		var meta map[string]any
		if json.Unmarshal(raw, &meta) == nil {
			if g, ok := meta[MetaKeyGohan].(map[string]any); ok {
				if tok, _ := g[MetaKeyToken].(string); tok != "" {
					return tok
				}
			}
		}
	}
	return ""
}

func (h *handler) invoke(ctx context.Context, ef exposedFlow, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := schema.Validate(ef.inputSchema, req.Params.Arguments); err != nil {
		return errorResult(err), nil
	}
	if ef.tasks {
		return h.startTask(ctx, ef, req)
	}
	out, err := ef.invoke(ctx, req.Params.Arguments)
	if err != nil {
		return h.mapError(ctx, ef, err), nil
	}
	return resultFromOut(out)
}

func (h *handler) resume(ctx context.Context, ef exposedFlow, req *mcp.CallToolRequest, state string) (*mcp.CallToolResult, error) {
	payload, err := decodeOrToken(state)
	if err != nil {
		return errorResult(err), nil
	}
	er := elicitationResult(req)
	if er == nil {
		return errorResult(errors.New("mcp: the retry carries no input response")), nil
	}
	var in stores.ResumeInput
	switch er.Action {
	case "accept":
		if len(er.Content) == 0 {
			in = gohan.Approve()
		} else {
			data, merr := json.Marshal(er.Content)
			if merr != nil {
				return errorResult(merr), nil
			}
			if verr := schema.Validate(ef.inputSchema, data); verr != nil {
				return errorResult(verr), nil
			}
			in = gohan.Deliver(data)
		}
	case "decline", "cancel":
		in = gohan.Reject(rejectionReason(er))
	default:
		return errorResult(fmt.Errorf("mcp: unknown elicitation action %q", er.Action)), nil
	}
	// The transport-verified principal travels in ctx and on the input; core
	// re-derives the approver from ctx.
	if p, ok := types.PrincipalFrom(ctx); ok {
		in.Approver = &p
	}
	out, err := ef.resume(ctx, types.ResumeToken(payload.Token), in)
	if err != nil {
		return h.mapError(ctx, ef, err), nil
	}
	return resultFromOut(out)
}

func elicitationResult(req *mcp.CallToolRequest) *mcp.ElicitResult {
	resp, ok := req.Params.InputResponses["0"]
	if !ok || resp == nil {
		return nil
	}
	er, ok := resp.(*mcp.ElicitResult)
	if !ok {
		return nil
	}
	return er
}

func decodeOrToken(state string) (RequestStatePayload, error) {
	if payload, err := decodeRequestState(state); err == nil {
		return payload, nil
	}
	return RequestStatePayload{Token: state}, nil
}

func rejectionReason(er *mcp.ElicitResult) string {
	if s, ok := er.Content["reason"].(string); ok {
		return s
	}
	return ""
}

func (h *handler) mapError(ctx context.Context, ef exposedFlow, err error) *mcp.CallToolResult {
	var se *types.SuspendError
	if !errors.As(err, &se) {
		return errorResult(err)
	}
	switch se.Reason {
	case types.HumanApproval, types.AwaitingInput:
		return inputRequiredResult(ef, se)
	default:
		return h.taskResult(ctx, ef, se)
	}
}

// startTask detaches a Tasks-marked flow: the run row already exists, so
// the flow runs in a goroutine. The reply carries the task ref at once,
// or the first outcome when the flow suspends or finishes within the
// request budget, so an immediate WakeAt reaches the caller.
func (h *handler) startTask(ctx context.Context, ef exposedFlow, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.Params.Arguments
	type firstOutcome struct {
		out any
		err error
	}
	outcome := make(chan firstOutcome, 1)
	ref, exec, err := h.launcher.start(context.WithoutCancel(ctx), ef.name, func(ctx context.Context) error {
		out, ferr := ef.invoke(ctx, args)
		outcome <- firstOutcome{out, ferr}
		return ferr
	})
	if err != nil {
		return errorResult(err), nil
	}
	go exec(context.Background())
	select {
	case oc := <-outcome:
		var se *types.SuspendError
		if errors.As(oc.err, &se) && se.Reason != types.HumanApproval && se.Reason != types.AwaitingInput {
			gohanMeta := map[string]any{MetaKeyTask: ref}
			if !se.WakeAt.IsZero() {
				gohanMeta[MetaKeyWakeAt] = se.WakeAt.UTC().Format(time.RFC3339)
			}
			res := &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "run suspended as a task"}},
			}
			res.Meta = mcp.Meta{MetaKeyGohan: gohanMeta}
			return res, nil
		}
		if oc.err != nil {
			return h.mapError(ctx, ef, oc.err), nil
		}
		return resultFromOut(oc.out)
	case <-time.After(h.cfg.budget):
		res := &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "run started as a task: " + ref.TaskID}},
		}
		res.Meta = mcp.Meta{MetaKeyGohan: map[string]any{MetaKeyTask: ref}}
		return res, nil
	}
}

func (h *handler) taskResult(ctx context.Context, ef exposedFlow, se *types.SuspendError) *mcp.CallToolResult {
	ref := TaskRef{TaskID: string(se.Token)}
	if h.cfg.launcher != nil {
		ref2, err := h.cfg.launcher.launch(context.WithoutCancel(ctx), ef.name, se)
		if err == nil {
			ref = ref2
		}
	}
	gohanMeta := map[string]any{MetaKeyTask: ref}
	if !se.WakeAt.IsZero() {
		gohanMeta[MetaKeyWakeAt] = se.WakeAt.UTC().Format(time.RFC3339)
	}
	res := &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: "run suspended as a task"}},
	}
	res.Meta = mcp.Meta{MetaKeyGohan: gohanMeta}
	return res
}

func inputRequiredResult(ef exposedFlow, se *types.SuspendError) *mcp.CallToolResult {
	state, _ := encodeRequestState(RequestStatePayload{Token: string(se.Token), Tool: ef.name})
	var ep *mcp.ElicitParams
	message := "approval required"
	if se.Reason == types.HumanApproval {
		if req, ok := se.Payload.(permission.ApprovalRequest); ok && req.Consequence != "" {
			message = req.Consequence
		}
		ep = &mcp.ElicitParams{
			Mode:            "form",
			Message:         message,
			RequestedSchema: map[string]any{"type": "boolean"},
		}
	} else {
		var requested any
		if ir, ok := se.Payload.(types.InputRequest); ok {
			if ir.Prompt != "" {
				message = ir.Prompt
			}
			if len(ir.Schema) > 0 {
				requested = json.RawMessage(ir.Schema)
			}
		}
		ep = &mcp.ElicitParams{Message: message, RequestedSchema: requested}
	}
	res := &mcp.CallToolResult{
		InputRequests: mcp.InputRequestMap{"0": ep},
		RequestState:  state,
	}
	res.Meta = mcp.Meta{MetaKeyGohan: map[string]any{MetaKeyToken: string(se.Token)}}
	return res
}

func resultFromOut(out any) (*mcp.CallToolResult, error) {
	raw, err := json.Marshal(out)
	if err != nil {
		return errorResult(err), nil
	}
	var structured any
	if err := json.Unmarshal(raw, &structured); err != nil {
		return errorResult(err), nil
	}
	return &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: string(raw)}},
		StructuredContent: structured,
	}, nil
}

func errorResult(err error) *mcp.CallToolResult {
	res := &mcp.CallToolResult{}
	res.SetError(err)
	return res
}
