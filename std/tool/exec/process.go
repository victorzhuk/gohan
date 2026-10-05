package exec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	osexec "os/exec"
	"syscall"
	"time"
)

// outputCap bounds stdout and stderr independently, per the tools spec
// default. Truncation is reported by the truncated flag so the caller can
// append a marker.
const outputCap = 64 << 10

// killGrace bounds how long Wait waits after the group kill for pipes held
// by grandchildren before giving up on them.
const killGrace = 2 * time.Second

type runConfig struct {
	Dir     string
	Env     []string
	Argv    []string
	Timeout time.Duration
}

type processResult struct {
	Stdout   string
	Stderr   string
	TimedOut bool
	ExitErr  error
	ExitCode int
	Err      error
}

// runProcess starts the command in its own process group and enforces the
// timeout by killing the whole group, so a child spawned by the command
// cannot outlive the call. Stdout and stderr are capped independently.
func runProcess(ctx context.Context, cfg runConfig) processResult {
	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()

	cmd := osexec.CommandContext(ctx, cfg.Argv[0], cfg.Argv[1:]...)
	cmd.Dir = cfg.Dir
	// A nil Env would inherit the parent environment; an explicit empty
	// slice runs the child with an empty environment, which is the
	// allowlist default.
	cmd.Env = cfg.Env
	if cmd.Env == nil {
		cmd.Env = []string{}
	}
	// A new process group lets the timeout kill descendants too; without
	// it a child that survives the direct kill would outlive the tool
	// call. WaitDelay stops a grandchild holding the output pipes from
	// blocking Wait after the kill.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = killGrace

	stdout := &limitedBuffer{cap: outputCap}
	stderr := &limitedBuffer{cap: outputCap}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	if err := cmd.Start(); err != nil {
		return processResult{Err: fmt.Errorf("start %s: %w", cfg.Argv[0], err)}
	}

	waitErr := cmd.Wait()
	res := processResult{
		Stdout: stdout.truncatedOutput(),
		Stderr: stderr.truncatedOutput(),
	}
	switch {
	case ctx.Err() != nil && errors.Is(ctx.Err(), context.DeadlineExceeded):
		res.TimedOut = true
	case ctx.Err() != nil:
		res.Err = ctx.Err()
	case waitErr == nil:
	default:
		if e, ok := errors.AsType[*osexec.ExitError](waitErr); ok {
			res.ExitErr = e
			res.ExitCode = e.ExitCode()
		} else {
			res.Err = fmt.Errorf("wait: %w", waitErr)
		}
	}
	return res
}

// limitedBuffer caps what it keeps at cap bytes and drops the rest, so a
// chatty command cannot exhaust memory.
type limitedBuffer struct {
	buf       bytes.Buffer
	cap       int
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if room := b.cap - b.buf.Len(); room > 0 {
		if len(p) > room {
			b.buf.Write(p[:room])
			b.truncated = true
			return len(p), nil
		}
		return b.buf.Write(p)
	}
	b.truncated = true
	return len(p), nil
}

// truncatedOutput returns the kept bytes plus the truncation marker when the
// cap cut anything off.
func (b *limitedBuffer) truncatedOutput() string {
	if !b.truncated {
		return b.buf.String()
	}
	return b.buf.String() + "\n[truncated]"
}

// groupAlive reports whether a process id still exists.
func groupAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}
