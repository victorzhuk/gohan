package gohan

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std/tool/exec"
)

type ackTool struct {
	spec types.ToolSpec
	ack  bool
}

func (t *ackTool) Spec() types.ToolSpec { return t.spec }

func (t *ackTool) Call(ctx context.Context, args json.RawMessage) (types.ToolResult, error) {
	return types.ToolResult{}, nil
}

func (t *ackTool) HostExecAcknowledged() bool { return t.ack }

func buildLog(t *testing.T, tools []types.Tool) string {
	t.Helper()
	var buf bytes.Buffer
	spec := nativeSpec("agent", "primary")
	spec.Tools = tools
	_, err := Build(
		WithLogger(slog.New(slog.NewTextHandler(&buf, nil))),
		WithModels(fakeModel{profile: profile("primary", true)}),
		WithNativeAgent(spec),
	)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return buf.String()
}

func TestBuildHostExecWarning(t *testing.T) {
	t.Run("sandbox.host-exec-warning", func(t *testing.T) {
		unacknowledged := &ackTool{spec: types.ToolSpec{Name: "run_cmd", Effect: types.SideEffect}}
		if log := buildLog(t, []types.Tool{unacknowledged}); !strings.Contains(log, "run_cmd") {
			t.Errorf("build log = %q, want a warning naming run_cmd", log)
		}
		acknowledged := &ackTool{spec: types.ToolSpec{Name: "run_cmd", Effect: types.SideEffect}, ack: true}
		if log := buildLog(t, []types.Tool{acknowledged}); strings.Contains(log, "without isolation") {
			t.Errorf("build log = %q, want no host-exec warning", log)
		}
		plain := &fakeNativeTool{spec: types.ToolSpec{Name: "search"}}
		if log := buildLog(t, []types.Tool{plain}); strings.Contains(log, "without isolation") {
			t.Errorf("build log = %q, want no host-exec warning for a non-runner tool", log)
		}
	})

	t.Run("constructor-warns-on-its-own", func(t *testing.T) {
		var buf bytes.Buffer
		prev := slog.Default()
		slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
		defer slog.SetDefault(prev)
		exec.New(types.ToolSpec{Name: "run_cmd", Effect: types.SideEffect}, exec.Cmd{})
		if !strings.Contains(buf.String(), "run_cmd") {
			t.Errorf("constructor log = %q, want a warning naming run_cmd", buf.String())
		}
	})
}
