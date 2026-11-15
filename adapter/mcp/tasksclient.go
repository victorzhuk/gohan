package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/victorzhuk/gohan/core/types"
)

// defaultTaskPoll is the delay between server task polls.
const defaultTaskPoll = 5 * time.Second

// WithTaskPoll sets the delay between server task polls; the default is
// five seconds.
func WithTaskPoll(d time.Duration) ClientOption {
	return func(cfg *clientConfig) { cfg.taskPoll = d }
}

// TaskHandle identifies one server-side task run reported in the gohan
// task meta of a CallToolResult.
type TaskHandle struct {
	TaskID     string    `json:"taskId"`
	RunID      string    `json:"runId"`
	ServerTool string    `json:"serverTool"`
	WakeAt     time.Time `json:"wakeAt,omitempty"`
}

// taskPending is the checkpoint payload of an AwaitingTool suspension: it
// carries what the poll retry needs.
type taskPending struct {
	Handle TaskHandle      `json:"handle"`
	Args   json.RawMessage `json:"args"`
}

// taskPendings maps the child token minted for an AwaitingTool suspension
// to what the poll reissues. Process-local: a poll only ever runs in the
// process that minted the suspension.
var taskPendings sync.Map

type taskPollRecord struct {
	tool *importedTool
	args json.RawMessage
}

// suspendTask checkpoints the pending task and suspends with AwaitingTool.
func (t *importedTool) suspendTask(ctx context.Context, args json.RawMessage, h TaskHandle) (*types.SuspendError, error) {
	data, err := json.Marshal(taskPending{Handle: h, Args: args})
	if err != nil {
		return nil, fmt.Errorf("mcp: pending task: %w", err)
	}
	token, err := t.cps.Put(ctx, checkpointFor(ctx, types.AwaitingTool, data))
	if err != nil {
		return nil, fmt.Errorf("mcp: checkpoint: %w", err)
	}
	taskPendings.Store(token, taskPollRecord{tool: t, args: args})
	return &types.SuspendError{Token: token, Reason: types.AwaitingTool, Payload: h, WakeAt: h.WakeAt}, nil
}

// taskMetaHandle extracts the gohan task handle from a result's meta.
func taskMetaHandle(res *mcp.CallToolResult, serverTool string) (TaskHandle, bool) {
	g, ok := res.Meta[MetaKeyGohan].(map[string]any)
	if !ok {
		return TaskHandle{}, false
	}
	tv, ok := g[MetaKeyTask].(map[string]any)
	if !ok {
		return TaskHandle{}, false
	}
	raw, err := json.Marshal(tv)
	if err != nil {
		return TaskHandle{}, false
	}
	var ref TaskRef
	if err := json.Unmarshal(raw, &ref); err != nil || ref.TaskID == "" {
		return TaskHandle{}, false
	}
	h := TaskHandle{TaskID: ref.TaskID, RunID: ref.RunID, ServerTool: serverTool}
	for _, src := range []map[string]any{tv, g} {
		if w, ok := src[MetaKeyWakeAt].(string); ok {
			if at, perr := time.Parse(time.RFC3339, w); perr == nil {
				h.WakeAt = at
				break
			}
		}
	}
	return h, true
}

// wakeLedger records scheduled wakes per set.
type wakeLedger struct {
	mu      sync.Mutex
	wakes   map[types.ResumeToken]time.Time
	poll    time.Duration
	results map[types.ResumeToken]json.RawMessage
}

var setWakes sync.Map // *Set -> *wakeLedger

func (s *Set) wakes() *wakeLedger {
	if v, ok := setWakes.Load(s); ok {
		return v.(*wakeLedger)
	}
	l := &wakeLedger{wakes: map[types.ResumeToken]time.Time{}, poll: defaultTaskPoll}
	v, _ := setWakes.LoadOrStore(s, l)
	return v.(*wakeLedger)
}

// Schedule satisfies gohan.Waker: it records the wake. The SDK exposes no
// public subscription or task stream, so completion is delivered by poll:
// the caller reschedules and, after PollResult, resumes the child token
// with Deliver of the recorded result bytes.
func (s *Set) Schedule(ctx context.Context, t types.ResumeToken, at time.Time) error {
	l := s.wakes()
	l.mu.Lock()
	l.wakes[t] = at
	l.mu.Unlock()
	return nil
}

// PollResult reissues the original tools/call for the task suspension once
// per poll interval and records the result bytes under the child token.
func (s *Set) PollResult(ctx context.Context, child types.ResumeToken) (json.RawMessage, error) {
	l := s.wakes()
	v, ok := taskPendings.Load(child)
	if !ok {
		return nil, fmt.Errorf("%w: task child %s", ErrReplayStateLost, child)
	}
	rec := v.(taskPollRecord)
	res, err := rec.tool.session.CallTool(ctx, &mcp.CallToolParams{Name: rec.tool.serverName, Arguments: rec.args})
	if err != nil {
		return nil, fmt.Errorf("mcp: task poll %s: %w", rec.tool.serverName, err)
	}
	if res.NeedsInput() {
		susp, serr := rec.tool.suspendInput(ctx, rec.args, res)
		if serr != nil {
			return nil, serr
		}
		return nil, susp
	}
	if res.IsError {
		return nil, fmt.Errorf("mcp: task poll %s failed: %s", rec.tool.serverName, joinText(res))
	}
	text := joinText(res)
	l.mu.Lock()
	l.results[child] = json.RawMessage(text)
	l.mu.Unlock()
	return json.RawMessage(text), nil
}
