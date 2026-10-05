package exec

import (
	"context"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunProcess(t *testing.T) {
	sh, err := osexec.LookPath("/bin/sh")
	if err != nil {
		t.Skip("/bin/sh not available")
	}

	t.Run("captures stdout and stderr", func(t *testing.T) {
		res := runProcess(context.Background(), runConfig{
			Argv:    []string{sh, "-c", "echo o; echo e >&2"},
			Timeout: 10 * time.Second,
		})
		if res.Err != nil {
			t.Fatalf("Err = %v", res.Err)
		}
		if strings.TrimSpace(res.Stdout) != "o" {
			t.Errorf("Stdout = %q, want o", res.Stdout)
		}
		if strings.TrimSpace(res.Stderr) != "e" {
			t.Errorf("Stderr = %q, want e", res.Stderr)
		}
	})

	t.Run("empty environment stays empty", func(t *testing.T) {
		t.Setenv("GOHAN_EXEC_TEST_SECRET", "x")
		res := runProcess(context.Background(), runConfig{
			Argv:    []string{sh, "-c", `if [ -n "$GOHAN_EXEC_TEST_SECRET" ]; then echo leaked; else echo clean; fi`},
			Timeout: 10 * time.Second,
		})
		if res.Err != nil {
			t.Fatalf("Err = %v", res.Err)
		}
		if got := strings.TrimSpace(res.Stdout); got != "clean" {
			t.Errorf("GOHAN_EXEC_TEST_SECRET in child: %q, want clean empty environment", got)
		}
	})

	t.Run("start failure is reported", func(t *testing.T) {
		res := runProcess(context.Background(), runConfig{
			Argv:    []string{"/nonexistent-binary-gohan-test"},
			Timeout: 10 * time.Second,
		})
		if res.Err == nil {
			t.Fatal("Err = nil, want a start failure")
		}
	})

	t.Run("truncation appends the marker", func(t *testing.T) {
		b := &limitedBuffer{cap: 4}
		if n, err := b.Write([]byte("abcdefgh")); err != nil || n != 8 {
			t.Fatalf("Write = %d, %v; want 8, nil", n, err)
		}
		if got := b.truncatedOutput(); got != "abcd\n[truncated]" {
			t.Errorf("truncatedOutput = %q, want %q", got, "abcd\n[truncated]")
		}
		keep := &limitedBuffer{cap: 8}
		if _, err := keep.Write([]byte("abcdefgh")); err != nil {
			t.Fatalf("Write: %v", err)
		}
		if keep.truncated {
			t.Error("truncated = true, want false within the cap")
		}
	})

	t.Run("timeout kills the child too", func(t *testing.T) {
		pidFile := filepath.Join(t.TempDir(), "child.pid")
		res := runProcess(context.Background(), runConfig{
			Argv:    []string{sh, "-c", "sleep 60 & echo $! > " + pidFile + "; wait"},
			Timeout: 300 * time.Millisecond,
		})
		if !res.TimedOut {
			t.Fatalf("TimedOut = false, want true (Err=%v, ExitErr=%v)", res.Err, res.ExitErr)
		}
		data, err := os.ReadFile(pidFile)
		if err != nil {
			t.Fatalf("read child pid: %v", err)
		}
		var pid int
		if _, err := fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &pid); err != nil {
			t.Fatalf("parse child pid %q: %v", data, err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for groupAlive(pid) && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if groupAlive(pid) {
			t.Fatalf("child %d survived the group kill", pid)
		}
	})
}
