package mcp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"iter"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// taskLauncherImpl backs durable task rows on the run and event stores.
// One instance per handler; no process-global state.
type taskLauncherImpl struct {
	runs   stores.Runs
	events stores.EventLog

	mu     sync.Mutex
	leases map[string]stores.Lease
}

func newTaskLauncher(runs stores.Runs, events stores.EventLog) *taskLauncherImpl {
	return &taskLauncherImpl{
		runs:   runs,
		events: events,
		leases: make(map[string]stores.Lease),
	}
}

func newTaskID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("mcp: mint task id: %w", err)
	}
	return "mcp_" + hex.EncodeToString(b[:]), nil
}

func taskEventMeta(runID, flow string) types.EventMeta {
	return types.EventMeta{SessionID: runID, RunID: runID, RootRunID: runID, Flow: flow, Time: time.Now()}
}

func (l *taskLauncherImpl) launch(ctx context.Context, tool string, se *types.SuspendError) (TaskRef, error) {
	id, err := newTaskID()
	if err != nil {
		return TaskRef{}, err
	}
	lease, err := l.runs.Start(ctx, stores.Run{
		RunID:       id,
		SessionID:   id,
		RootRunID:   id,
		OperationID: id,
		Flow:        tool,
		Backend:     "mcp",
		Mode:        types.Primary,
	}, stores.LeaseTTL)
	if err != nil {
		return TaskRef{}, fmt.Errorf("mcp: start task run: %w", err)
	}
	if err := l.events.Append(ctx, id, stores.Event{
		Payload: types.Suspended{Token: se.Token, Reason: se.Reason, Payload: se.Payload, WakeAt: se.WakeAt},
		Meta:    taskEventMeta(id, tool),
	}); err != nil {
		return TaskRef{}, fmt.Errorf("mcp: record task suspension: %w", err)
	}
	l.mu.Lock()
	l.leases[id] = lease
	l.mu.Unlock()
	return TaskRef{TaskID: id, RunID: id}, nil
}

// run executes fn in a goroutine for a Tasks-marked flow and closes the
// run row out: a new suspension keeps the row working, anything else
// records the terminal event and finishes the run.
func (l *taskLauncherImpl) run(fn func(ctx context.Context) error, runID string) {
	if fn == nil {
		return
	}
	go func() {
		l.closeOut(fn(context.Background()), runID, "")
	}()
}

// start mints a durable run row for a Tasks-marked tool and returns the
// execution function that closes the row out when the caller runs the
// flow detached from the request.
func (l *taskLauncherImpl) start(ctx context.Context, tool string, fn func(context.Context) error) (TaskRef, func(context.Context) error, error) {
	id, err := newTaskID()
	if err != nil {
		return TaskRef{}, nil, err
	}
	lease, err := l.runs.Start(ctx, stores.Run{
		RunID:       id,
		SessionID:   id,
		RootRunID:   id,
		OperationID: id,
		Flow:        tool,
		Backend:     "mcp",
		Mode:        types.Primary,
	}, stores.LeaseTTL)
	if err != nil {
		return TaskRef{}, nil, fmt.Errorf("mcp: start task run: %w", err)
	}
	l.mu.Lock()
	l.leases[id] = lease
	l.mu.Unlock()
	exec := func(ctx context.Context) error {
		err := fn(ctx)
		l.closeOut(err, id, tool)
		return err
	}
	return TaskRef{TaskID: id, RunID: id}, exec, nil
}

// closeOut records the run outcome: a suspension keeps the row working
// with a Suspended event, anything else appends the terminal event and
// finishes the run.
func (l *taskLauncherImpl) closeOut(err error, runID, flow string) {
	l.mu.Lock()
	lease, ok := l.leases[runID]
	l.mu.Unlock()
	if !ok {
		return
	}
	var se *types.SuspendError
	if errors.As(err, &se) {
		_ = l.events.Append(context.Background(), runID, stores.Event{
			Payload: types.Suspended{Token: se.Token, Reason: se.Reason, Payload: se.Payload, WakeAt: se.WakeAt},
			Meta:    taskEventMeta(runID, flow),
		})
		return
	}
	state, reason := stores.Finished, types.StopCompleted
	if err != nil {
		state, reason = stores.Failed, types.StopFailed
	}
	_ = l.events.Append(context.Background(), runID, stores.Event{
		Payload: types.Done{Reason: reason},
		Meta:    taskEventMeta(runID, flow),
	})
	_ = l.runs.Finish(context.Background(), lease, state, nil, "")
}

// Updates streams a task's events ordered by the store-assigned Seq.
// The stateless streamable transport rejects server-initiated GETs, so
// this pull surface is the only ordered-update channel.
func (l *taskLauncherImpl) Updates(ctx context.Context, taskID string, afterSeq int64) iter.Seq2[stores.Event, error] {
	return l.events.Read(ctx, taskID, afterSeq)
}

type tasksGetParams struct {
	sdk.ParamsBase
	TaskID string `json:"taskId"`
}

type tasksGetResult struct {
	sdk.ResultBase
	TaskID string `json:"taskId"`
	Status string `json:"status"`
	WakeAt string `json:"wakeAt,omitempty"`
	Seq    int64  `json:"seq"`
}

func registerTasksGet(srv *sdk.Server, l *taskLauncherImpl) error {
	return sdk.AddReceivingCustomMethod(srv, MethodTasksGet,
		func(ctx context.Context, ss *sdk.ServerSession, params *tasksGetParams) (*tasksGetResult, error) {
			if params == nil || params.TaskID == "" {
				return nil, &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: "tasks/get requires taskId"}
			}
			run, err := l.runs.ByOperation(ctx, "", params.TaskID)
			if err != nil {
				return nil, &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: fmt.Sprintf("unknown task %q", params.TaskID)}
			}
			res := &tasksGetResult{TaskID: params.TaskID, Status: TaskStatus(run.State)}
			for ev, err := range l.events.Read(ctx, params.TaskID, 0) {
				if err != nil {
					break
				}
				if ev.Meta.Seq > res.Seq {
					res.Seq = ev.Meta.Seq
				}
				if s, ok := ev.Payload.(types.Suspended); ok && !s.WakeAt.IsZero() {
					res.WakeAt = s.WakeAt.UTC().Format(time.RFC3339)
				}
			}
			return res, nil
		})
}
