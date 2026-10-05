package exec

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

func staticArgv(argv ...string) func(json.RawMessage) ([]string, error) {
	return func(json.RawMessage) ([]string, error) { return argv, nil }
}

func shArgv(script string) []string { return []string{"/bin/sh", "-c", script} }

func needSh(t *testing.T) {
	t.Helper()
	if _, err := osexec.LookPath("/bin/sh"); err != nil {
		t.Skip("/bin/sh not available")
	}
}

func call(t *testing.T, tool types.Tool) (types.ToolResult, error) {
	t.Helper()
	return tool.Call(context.Background(), json.RawMessage(`{}`))
}

// waitForGone polls until pid no longer exists, because the kernel reaps
// group members asynchronously after the kill.
func waitForGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !groupAlive(pid) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("process %d still alive after group kill", pid)
}

func TestExecHostRunner(t *testing.T) {
	t.Run("tools.env-allowlist", func(t *testing.T) {
		needSh(t)
		t.Setenv("SECRET", "x")
		tool := New(types.ToolSpec{Name: "run_script"}, Cmd{
			Argv: func(json.RawMessage) ([]string, error) {
				return shArgv("env"), nil
			},
		})
		res, err := call(t, tool)
		if err != nil {
			t.Fatalf("Call: %v", err)
		}
		if res.Outcome != types.Succeeded {
			t.Fatalf("Outcome = %v, want Succeeded", res.Outcome)
		}
		txt := res.Content[0].(types.Text).Text
		if strings.Contains(txt, "SECRET") {
			t.Errorf("child environment leaked SECRET:\n%s", txt)
		}
	})

	t.Run("tools.timeout-kills-group", func(t *testing.T) {
		needSh(t)
		pidFile := filepath.Join(t.TempDir(), "child.pid")
		script := "sleep 60 & echo $! > " + pidFile + "; wait"
		tool := New(types.ToolSpec{Name: "run_script", Effect: types.SideEffect}, Cmd{
			Argv: func(json.RawMessage) ([]string, error) {
				return shArgv(script), nil
			},
		}, WithTimeout(500*time.Millisecond))
		res, err := call(t, tool)
		if err != nil {
			t.Fatalf("Call: %v", err)
		}
		if res.Outcome != types.Unknown {
			t.Fatalf("Outcome = %v, want Unknown for a SideEffect timeout", res.Outcome)
		}
		if res.Error == nil {
			t.Fatal("Error = nil, want an error result")
		}
		data, err := os.ReadFile(pidFile)
		if err != nil {
			t.Fatalf("read child pid: %v", err)
		}
		var pid int
		if _, err := fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &pid); err != nil {
			t.Fatalf("parse child pid %q: %v", data, err)
		}
		if pid == 0 {
			t.Fatal("child pid not recorded")
		}
		waitForGone(t, pid)
	})

	t.Run("nonzero exit is a result not an error", func(t *testing.T) {
		needSh(t)
		tool := New(types.ToolSpec{Name: "run_script"}, Cmd{
			Argv: func(json.RawMessage) ([]string, error) {
				return shArgv("echo out; echo err >&2; exit 3"), nil
			},
		}, AllowHostExec())
		res, err := call(t, tool)
		if err != nil {
			t.Fatalf("Call returned Go error for a nonzero exit: %v", err)
		}
		if res.Outcome != types.Failed {
			t.Errorf("Outcome = %v, want Failed", res.Outcome)
		}
		if res.Error == nil || !strings.Contains(res.Error.Message, "exit status 3") {
			t.Errorf("Error = %+v, want exit status 3", res.Error)
		}
		if res.Error != nil && !strings.Contains(res.Error.Message, "err") {
			t.Errorf("Error = %+v, want capped stderr content", res.Error)
		}
	})

	t.Run("working directory is honoured", func(t *testing.T) {
		needSh(t)
		dir := t.TempDir()
		tool := New(types.ToolSpec{Name: "run_script"}, Cmd{
			Argv: func(json.RawMessage) ([]string, error) {
				return shArgv("pwd"), nil
			},
			Dir: dir,
		}, AllowHostExec())
		res, err := call(t, tool)
		if err != nil {
			t.Fatalf("Call: %v", err)
		}
		got := strings.TrimSpace(res.Content[0].(types.Text).Text)
		if got != dir {
			t.Errorf("pwd = %q, want %q", got, dir)
		}
	})

	t.Run("read-only timeout is retryable", func(t *testing.T) {
		needSh(t)
		tool := New(types.ToolSpec{Name: "probe", Effect: types.ReadOnly}, Cmd{
			Argv: func(json.RawMessage) ([]string, error) {
				return shArgv("sleep 60"), nil
			},
		}, WithTimeout(300*time.Millisecond))
		res, err := call(t, tool)
		if err != nil {
			t.Fatalf("Call: %v", err)
		}
		if res.Outcome != types.Failed {
			t.Errorf("Outcome = %v, want Failed", res.Outcome)
		}
		if res.Error == nil || res.Error.Kind != types.Retryable {
			t.Errorf("Error = %+v, want Kind Retryable", res.Error)
		}
	})

	t.Run("allow host exec guard warns", func(t *testing.T) {
		var buf lockedBuffer
		prev := slog.Default()
		slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
		New(types.ToolSpec{Name: "unacked"}, Cmd{Argv: staticArgv("true")})
		slog.SetDefault(prev)
		if !strings.Contains(buf.String(), "AllowHostExec") {
			t.Errorf("warning not emitted:\n%s", buf.String())
		}

		buf.Reset()
		prev = slog.Default()
		slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
		New(types.ToolSpec{Name: "acked"}, Cmd{Argv: staticArgv("true")}, AllowHostExec())
		slog.SetDefault(prev)
		if buf.Len() != 0 {
			t.Errorf("unexpected warning with AllowHostExec:\n%s", buf.String())
		}
	})
}

// lockedBuffer is a concurrency-safe buffer for slog handlers.
type lockedBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *lockedBuffer) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf.Reset()
}

func (b *lockedBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Len()
}
