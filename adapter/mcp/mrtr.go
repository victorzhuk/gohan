package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/victorzhuk/gohan/adapter/mcp/internal/schema"
	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

var (
	// ErrCheckpointsRequired is returned by Client when WithCheckpoints is
	// absent: every imported call can suspend, so no store means no client.
	ErrCheckpointsRequired = errors.New("mcp: checkpoints store required")

	// ErrReplayStateLost reports a resume whose pending adapter state is
	// gone or already consumed: the pending call is never replayed twice.
	ErrReplayStateLost = errors.New("mcp: replay state lost")

	// ErrUnsupportedInputRequests reports a result that carries anything
	// but exactly one elicitation request.
	ErrUnsupportedInputRequests = errors.New("mcp: unsupported input requests")
)

// WithCheckpoints is REQUIRED: the store that mints and consumes the
// child resume tokens for input_required suspensions.
func WithCheckpoints(c stores.Checkpoints) ClientOption {
	return func(cfg *clientConfig) { cfg.cps = c }
}

// pendingCall is the adapter state a suspended imported call needs for the
// chain resume: core consumes only the parent token, so everything the
// replay uses travels in the checkpoint data.
type pendingCall struct {
	ServerTool   string          `json:"serverTool"`
	Args         json.RawMessage `json:"args"`
	RequestID    string          `json:"requestId"`
	RequestState string          `json:"requestState"`
	Schema       json.RawMessage `json:"schema"`
}

// suspendInput maps one input_required result onto a minted child token
// and the matching core suspension payload.
func (t *importedTool) suspendInput(ctx context.Context, args json.RawMessage, res *mcp.CallToolResult) (*types.SuspendError, error) {
	if len(res.InputRequests) != 1 {
		return nil, fmt.Errorf("%w: got %d requests", ErrUnsupportedInputRequests, len(res.InputRequests))
	}
	var reqID string
	var params *mcp.ElicitParams
	for id, req := range res.InputRequests {
		ep, ok := req.(*mcp.ElicitParams)
		if !ok {
			return nil, fmt.Errorf("%w: %T", ErrUnsupportedInputRequests, req)
		}
		reqID, params = id, ep
	}
	schemaBytes, err := json.Marshal(params.RequestedSchema)
	if err != nil {
		return nil, fmt.Errorf("mcp: request schema: %w", err)
	}
	data, err := json.Marshal(pendingCall{
		ServerTool:   t.serverName,
		Args:         args,
		RequestID:    reqID,
		RequestState: res.RequestState,
		Schema:       schemaBytes,
	})
	if err != nil {
		return nil, fmt.Errorf("mcp: pending call: %w", err)
	}
	reason := types.AwaitingInput
	if isConfirmation(schemaBytes) {
		reason = types.HumanApproval
	}
	token, err := t.cps.Put(ctx, checkpointFor(ctx, reason, data))
	if err != nil {
		return nil, fmt.Errorf("mcp: checkpoint: %w", err)
	}
	susp := &types.SuspendError{Token: token, Reason: reason}
	if reason == types.HumanApproval {
		callID := ""
		if call, ok := types.ToolUseFrom(ctx); ok {
			callID = call.ID
		}
		susp.Payload = permission.ApprovalRequest{
			Tool: t.spec,
			Call: types.ToolUse{ID: callID, Name: t.spec.Name, Args: args},
			Risk: t.spec.Risk,
		}
		return susp, nil
	}
	susp.Payload = types.InputRequest{Prompt: params.Message, Schema: schemaBytes}
	return susp, nil
}

// isConfirmation reports whether the requested schema asks for a yes/no
// confirmation rather than structured input.
func isConfirmation(schema json.RawMessage) bool {
	var s struct {
		Type string `json:"type"`
	}
	return json.Unmarshal(schema, &s) == nil && s.Type == "boolean"
}

func checkpointFor(ctx context.Context, reason types.SuspendReason, data []byte) stores.Checkpoint {
	cp := stores.Checkpoint{
		SchemaVersion: stores.CurrentSchemaVersion,
		Backend:       "mcp",
		Reason:        reason,
		Data:          data,
	}
	if ri, ok := types.RunInfoFrom(ctx); ok {
		cp.SessionID = ri.SessionID
		cp.RunID = ri.RunID
	}
	return cp
}

// ResumeChild resumes one suspended imported call. Core resolves the
// pending tool by name and hands over only the parent token; the child
// token minted at suspension time is consumed here.
func (t *importedTool) ResumeChild(ctx context.Context, token types.ResumeToken, in stores.ResumeInput) (json.RawMessage, error) {
	pc, tp, err := t.loadPending(ctx, token, in)
	if err != nil {
		return nil, err
	}
	if tp != nil {
		if len(in.Data) > 0 {
			return in.Data, nil
		}
		return t.mapResume(ctx, tp.Args, func() (*mcp.CallToolResult, error) {
			return t.session.CallTool(ctx, &mcp.CallToolParams{Name: tp.Handle.ServerTool, Arguments: tp.Args})
		})
	}
	args := pc.Args
	if in.Verdict == stores.VerdictEdit && len(in.Args) > 0 {
		args = in.Args
	}
	var resp mcp.InputResponseMap
	if pc.RequestID != "" {
		resp = mcp.InputResponseMap{pc.RequestID: inputResponse(in)}
	}
	return t.mapResume(ctx, args, func() (*mcp.CallToolResult, error) {
		return t.session.CallTool(ctx, &mcp.CallToolParams{
			Name:           pc.ServerTool,
			Arguments:      args,
			InputResponses: resp,
			RequestState:   pc.RequestState,
		})
	})
}

// loadPending recovers the suspension state. Deliver validates the payload
// against the requested schema before the token is consumed, so a rejected
// delivery leaves the pending call resumable.
func (t *importedTool) loadPending(ctx context.Context, token types.ResumeToken, in stores.ResumeInput) (pendingCall, *taskPending, error) {
	var (
		pc pendingCall
		tp *taskPending
	)
	decode := func(data []byte) error {
		if err := json.Unmarshal(data, &pc); err == nil && pc.ServerTool != "" {
			return nil
		}
		var r taskPending
		if err := json.Unmarshal(data, &r); err == nil && r.Handle.TaskID != "" {
			tp = &r
			return nil
		}
		return fmt.Errorf("mcp: undecodable pending state")
	}
	if len(in.Data) > 0 {
		data, err := peekCheckpoint(ctx, t.cps, token)
		if err != nil {
			return pc, tp, fmt.Errorf("%w: %v", ErrReplayStateLost, err)
		}
		if err := decode(data); err != nil {
			return pc, tp, fmt.Errorf("%w: %v", ErrReplayStateLost, err)
		}
		if tp == nil {
			if verr := schema.Validate(pc.Schema, in.Data); verr != nil {
				return pc, tp, fmt.Errorf("%w: %v", types.ErrInputInvalid, verr)
			}
		}
	}
	cp, err := t.cps.Consume(ctx, token, in)
	if err != nil {
		return pc, tp, fmt.Errorf("%w: %v", ErrReplayStateLost, err)
	}
	if len(in.Data) == 0 {
		if err := decode(cp.Data); err != nil {
			return pc, tp, fmt.Errorf("%w: %v", ErrReplayStateLost, err)
		}
	}
	return pc, tp, nil
}

// peekCheckpoint reads the pending state without consuming the token.
func peekCheckpoint(ctx context.Context, cps stores.Checkpoints, token types.ResumeToken) ([]byte, error) {
	if pr, ok := cps.(stores.CheckpointResumer); ok {
		cp, err := pr.Peek(ctx, token)
		if err != nil {
			return nil, err
		}
		return cp.Data, nil
	}
	ri, _ := types.RunInfoFrom(ctx)
	cp, _, err := cps.PendingInput(ctx, ri.RunID)
	if err != nil {
		return nil, err
	}
	return cp.Data, nil
}

// inputResponse maps the resume decision onto one elicitation response.
func inputResponse(in stores.ResumeInput) *mcp.ElicitResult {
	switch in.Verdict {
	case stores.VerdictReject:
		return &mcp.ElicitResult{Action: "decline", Content: map[string]any{"reason": in.Reason}}
	default:
		if len(in.Data) > 0 {
			var content map[string]any
			_ = json.Unmarshal(in.Data, &content)
			return &mcp.ElicitResult{Action: "accept", Content: content}
		}
		return &mcp.ElicitResult{Action: "accept"}
	}
}

// mapResume replays the mapped result: another input_required round mints
// a fresh child token (multi-round), a server task re-suspends, an error
// result settles the call as rejected bytes, and success returns the
// result bytes core appends to the parent history.
func (t *importedTool) mapResume(ctx context.Context, args json.RawMessage, call func() (*mcp.CallToolResult, error)) (json.RawMessage, error) {
	res, err := call()
	if err != nil {
		return nil, fmt.Errorf("mcp: replay %s: %w", t.serverName, err)
	}
	if h, ok := taskMetaHandle(res, t.serverName); ok {
		susp, serr := t.suspendTask(ctx, args, h)
		if serr != nil {
			return nil, serr
		}
		return nil, susp
	}
	if res.NeedsInput() {
		susp, serr := t.suspendInput(ctx, args, res)
		if serr != nil {
			return nil, serr
		}
		return nil, susp
	}
	text := joinText(res)
	if res.IsError {
		out, merr := json.Marshal(map[string]string{"error": text})
		if merr != nil {
			return nil, merr
		}
		return out, nil
	}
	if res.StructuredContent != nil {
		return json.Marshal(res.StructuredContent)
	}
	return json.Marshal(text)
}
