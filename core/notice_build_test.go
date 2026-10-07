package gohan

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

type noticeSink struct {
	got []types.RunNotice
}

func (s *noticeSink) Notify(_ context.Context, n types.RunNotice) error {
	s.got = append(s.got, n)
	return nil
}

func noticeStack(t *testing.T, sink types.Notifier, log *bytes.Buffer) *Stack {
	t.Helper()
	opts := []Option{
		WithStores(stores.Stores{
			SessionLog:  stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)),
			Runs:        stores.NewMemoryRuns(stores.WithMemoryRunClock(time.Now), stores.WithMemoryRunInfo(types.RunInfoFrom)),
			Checkpoints: stores.NewMemoryCheckpoints(stores.WithMemoryCheckpointClock(time.Now), stores.WithMemoryCheckpointRunInfo(types.RunInfoFrom)),
		}),
		WithModels(&nrrModel{}),
		WithNativeAgent(NativeSpec{
			Request: FlowRequest{Name: "chat"},
			Profile: "native",
			Tools:   []types.Tool{&bookTool{}, &nrrNoteTool{}},
			Assemble: func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
				return types.ModelRequest{Messages: append(append([]Message{}, in.History...), in.Input...)}, nil
			},
		}),
	}
	if sink != nil {
		opts = append(opts, WithNotifier(sink))
	}
	if log != nil {
		opts = append(opts, WithLogger(slog.New(slog.NewTextHandler(log, nil))))
	}
	stack, err := Build(opts...)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return stack
}

func TestNotifierBuild(t *testing.T) {
	t.Run("stores the notifier", func(t *testing.T) {
		sink := &noticeSink{}
		stack := noticeStack(t, sink, nil)
		if stack.Notifier() != types.Notifier(sink) {
			t.Fatal("stack.Notifier() did not return the WithNotifier sink")
		}
	})

	t.Run("warns for a detached flow without a notifier", func(t *testing.T) {
		var log bytes.Buffer
		stack := noticeStack(t, nil, &log)
		runs := stores.NewMemoryRuns(stores.WithMemoryRunClock(time.Now), stores.WithMemoryRunInfo(types.RunInfoFrom))
		_, err := NewNativeConversation(stack, "chat",
			WithConversationRuns(runs),
			WithConversationEventLog(stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))),
			Detached(),
		)
		if err != nil {
			t.Fatalf("new native conversation: %v", err)
		}
		if !bytes.Contains(log.Bytes(), []byte("no notifier")) {
			t.Fatalf("build log = %q, want the detached-without-notifier warning", log.String())
		}
	})

	t.Run("stays silent for a detached flow with a notifier", func(t *testing.T) {
		var log bytes.Buffer
		stack := noticeStack(t, &noticeSink{}, &log)
		runs := stores.NewMemoryRuns(stores.WithMemoryRunClock(time.Now), stores.WithMemoryRunInfo(types.RunInfoFrom))
		if _, err := NewNativeConversation(stack, "chat",
			WithConversationRuns(runs),
			WithConversationEventLog(stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))),
			Detached(),
		); err != nil {
			t.Fatalf("new native conversation: %v", err)
		}
		if bytes.Contains(log.Bytes(), []byte("no notifier")) {
			t.Fatalf("build log = %q, want no notifier warning", log.String())
		}
	})
}
