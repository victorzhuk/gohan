package gohan

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std/tool/exec"
)

func buildLog(t *testing.T, tools []types.Tool, foreign []ForeignSpec) string {
	t.Helper()
	var buf bytes.Buffer
	spec := nativeSpec("agent", "primary")
	spec.Tools = tools
	opts := []Option{
		WithLogger(slog.New(slog.NewTextHandler(&buf, nil))),
		WithModels(fakeModel{profile: profile("primary", true)}),
		WithNativeAgent(spec),
	}
	for _, def := range foreign {
		opts = append(opts, WithForeignAgent(def))
	}
	if _, err := Build(opts...); err != nil {
		t.Fatalf("build: %v", err)
	}
	return buf.String()
}

func foreignSpec(name string, tools ...types.Tool) ForeignSpec {
	return ForeignSpec{
		Request: FlowRequest{Name: name},
		Profile: "primary",
		Tools:   tools,
		NewRuntime: func() runtime.Runtime {
			return nil
		},
	}
}

func TestBuildHostExecWarning(t *testing.T) {
	t.Run("sandbox.host-exec-warning", func(t *testing.T) {
		unacknowledged := exec.New(types.ToolSpec{Name: "run_cmd", Effect: types.SideEffect}, exec.Cmd{})
		if log := buildLog(t, []types.Tool{unacknowledged}, nil); !strings.Contains(log, "run_cmd") {
			t.Errorf("build log = %q, want a warning naming run_cmd", log)
		}
		acknowledged := exec.New(types.ToolSpec{Name: "run_cmd", Effect: types.SideEffect}, exec.Cmd{}, exec.AllowHostExec())
		if log := buildLog(t, []types.Tool{acknowledged}, nil); strings.Contains(log, "without isolation") {
			t.Errorf("build log = %q, want no host-exec warning", log)
		}
		plain := &fakeNativeTool{spec: types.ToolSpec{Name: "search"}}
		if log := buildLog(t, []types.Tool{plain}, nil); strings.Contains(log, "without isolation") {
			t.Errorf("build log = %q, want no host-exec warning for a non-runner tool", log)
		}
	})

	t.Run("hostExecWarningForeign", func(t *testing.T) {
		unacknowledged := exec.New(types.ToolSpec{Name: "foreign_cmd", Effect: types.SideEffect}, exec.Cmd{})
		log := buildLog(t, nil, []ForeignSpec{foreignSpec("foreign-agent", unacknowledged)})
		if !strings.Contains(log, "foreign_cmd") {
			t.Errorf("build log = %q, want a warning naming foreign_cmd", log)
		}
		acknowledged := exec.New(types.ToolSpec{Name: "foreign_cmd", Effect: types.SideEffect}, exec.Cmd{}, exec.AllowHostExec())
		log = buildLog(t, nil, []ForeignSpec{foreignSpec("foreign-agent", acknowledged)})
		if strings.Contains(log, "without isolation") {
			t.Errorf("build log = %q, want no host-exec warning", log)
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
