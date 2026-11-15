package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

func startTasksServer(t *testing.T, opts ...ServerOption) (*sdk.ClientSession, *taskLauncherImpl) {
	t.Helper()
	base := []ServerOption{
		WithPrincipalSource(testPrincipal),
		WithStores(stores.NewMemoryRuns(), stores.NewMemoryEventLog()),
	}
	handler, err := NewHandler(append(base, opts...)...)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "0"}, &sdk.ClientOptions{
		MultiRoundTrip: &sdk.MultiRoundTripOptions{Disabled: true},
	})
	_ = sdk.AddSendingCustomMethod[*tasksGetParams, *tasksGetResult](client, MethodTasksGet)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	cs, err := client.Connect(ctx, &sdk.StreamableClientTransport{Endpoint: server.URL}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	ph, ok := handler.(*principalHandler)
	if !ok {
		t.Fatalf("handler type %T", handler)
	}
	return cs, ph.tasksLauncher()
}

func tasksGet(t *testing.T, cs *sdk.ClientSession, taskID string) (*tasksGetResult, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return sdk.CallCustomMethod[*tasksGetParams, *tasksGetResult](ctx, cs, MethodTasksGet, &tasksGetParams{TaskID: taskID})
}

func TestTasksServerLifecycle(t *testing.T) {
	t.Run("interop.server-long-task", func(t *testing.T) {
		started := make(chan struct{})
		release := make(chan struct{})
		flow := &scriptedFlow{
			invokeFn: func(ctx context.Context, in map[string]any) (map[string]any, error) {
				close(started)
				<-release
				return map[string]any{"ok": true}, nil
			},
			resumeFn: func(ctx context.Context, token types.ResumeToken, in stores.ResumeInput) (map[string]any, error) {
				return map[string]any{"ok": true}, nil
			},
		}
		cs, launcher := startTasksServer(t, Expose("long_task", flow, Tasks()), WithRequestBudget(20*time.Millisecond))

		res := callTool(t, cs, &sdk.CallToolParams{Name: "long_task", Arguments: map[string]any{}})
		if res.IsError {
			t.Fatalf("tools/call returned error: %v", res.Content)
		}
		g := gohanMeta(t, res)
		raw, err := json.Marshal(g[MetaKeyTask])
		if err != nil {
			t.Fatalf("marshal task meta: %v", err)
		}
		var ref TaskRef
		if err := json.Unmarshal(raw, &ref); err != nil {
			t.Fatalf("decode task ref: %v", err)
		}
		if ref.TaskID == "" || ref.RunID != ref.TaskID {
			t.Fatalf("bad task ref %+v", ref)
		}
		if len(ref.TaskID) < 5 || ref.TaskID[:4] != "mcp_" {
			t.Fatalf("task id not mcp_ prefixed: %q", ref.TaskID)
		}

		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("flow did not start after tools/call returned")
		}

		got, err := tasksGet(t, cs, ref.TaskID)
		if err != nil {
			t.Fatalf("tasks/get: %v", err)
		}
		if got.Status != "working" {
			t.Fatalf("status = %q, want working while flow runs", got.Status)
		}

		close(release)
		deadline := time.Now().Add(5 * time.Second)
		for {
			got, err = tasksGet(t, cs, ref.TaskID)
			if err != nil {
				t.Fatalf("tasks/get: %v", err)
			}
			if got.Status == "completed" {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("task not completed in time, status %q", got.Status)
			}
			time.Sleep(10 * time.Millisecond)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var seqs []int64
		for ev, err := range launcher.Updates(ctx, ref.TaskID, 0) {
			if err != nil {
				t.Fatalf("Updates: %v", err)
			}
			seqs = append(seqs, ev.Meta.Seq)
		}
		if len(seqs) == 0 {
			t.Fatal("no updates")
		}
		for i := 1; i < len(seqs); i++ {
			if seqs[i] != seqs[i-1]+1 {
				t.Fatalf("seqs not gapless: %v", seqs)
			}
		}
	})
}

func TestTasksMarkedFlowStartsDetached(t *testing.T) {
	impl := newTaskLauncher(stores.NewMemoryRuns(), stores.NewMemoryEventLog())
	release := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ref, exec, err := impl.start(ctx, "flow.func", func(ctx context.Context) error {
		<-release
		return nil
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	done := make(chan struct{})
	go func() {
		_ = exec(context.Background())
		close(done)
	}()

	deadline := time.Now().Add(5 * time.Second)
	var run stores.Run
	for {
		run, err = impl.runs.ByOperation(ctx, "", ref.RunID)
		if err == nil && run.State == stores.Running {
			break
		}
		if err != nil {
			t.Fatalf("ByOperation: %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("state = %v, want Running before release", run.State)
		}
		time.Sleep(5 * time.Millisecond)
	}
	select {
	case <-done:
		t.Fatal("exec returned before the flow was released")
	default:
	}

	close(release)
	<-done
	for {
		run, err = impl.runs.ByOperation(ctx, "", ref.RunID)
		if err != nil {
			t.Fatalf("ByOperation: %v", err)
		}
		if run.State == stores.Finished {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("state = %v, want Finished after release", run.State)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestTaskRunRecordsTerminalEvent(t *testing.T) {
	impl := newTaskLauncher(stores.NewMemoryRuns(), stores.NewMemoryEventLog())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ref, err := impl.launch(ctx, "flow.func", &types.SuspendError{
		Token:  types.ResumeToken("tok-2"),
		Reason: types.AwaitingExternal,
	})
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	impl.run(func(ctx context.Context) error { return nil }, ref.RunID)

	deadline := time.Now().Add(5 * time.Second)
	var evs []stores.Event
	for {
		evs = nil
		for ev, err := range impl.Updates(ctx, ref.RunID, 0) {
			if err != nil {
				t.Fatalf("Updates: %v", err)
			}
			evs = append(evs, ev)
		}
		if len(evs) >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no terminal event, got %d", len(evs))
		}
		time.Sleep(10 * time.Millisecond)
	}
	done, ok := evs[1].Payload.(types.Done)
	if !ok {
		t.Fatalf("terminal payload %T, want types.Done", evs[1].Payload)
	}
	if done.Reason != types.StopCompleted {
		t.Fatalf("reason = %q, want completed", done.Reason)
	}
	run, err := impl.runs.ByOperation(ctx, "", ref.RunID)
	if err != nil {
		t.Fatalf("ByOperation: %v", err)
	}
	if run.State != stores.Finished {
		t.Fatalf("state = %v, want Finished", run.State)
	}
}

func TestTasksGetUnknownTask(t *testing.T) {
	cs, _ := startTasksServer(t, Expose("noop", &scriptedFlow{
		invokeFn: func(ctx context.Context, in map[string]any) (map[string]any, error) {
			return map[string]any{"ok": true}, nil
		},
		resumeFn: func(ctx context.Context, token types.ResumeToken, in stores.ResumeInput) (map[string]any, error) {
			return nil, nil
		},
	}))
	_, err := tasksGet(t, cs, "mcp_missing")
	if err == nil {
		t.Fatal("tasks/get unknown task: want error")
	}
	var re *jsonrpc.Error
	if !errors.As(err, &re) || re.Code != jsonrpc.CodeInvalidParams {
		t.Fatalf("err = %v, want code -32602", err)
	}
}
