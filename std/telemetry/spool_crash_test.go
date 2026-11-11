package telemetry_test

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std/telemetry"
)

// The self re-exec children are keyed off one env var so a mis-invocation
// (helper test running in the parent process) is a no-op.
const walCrashEnv = "GOHAN_WAL_CRASH_MODE"

const (
	walCrashModeAppend = "append"
	walCrashModeCursor = "cursor"
)

// blockingExportDest blocks every Export call forever, ignoring the context,
// so a child killed by SIGKILL is guaranteed to die inside the delivery pass.
type blockingExportDest struct {
	name string
}

func (d *blockingExportDest) Name() string { return d.name }

func (d blockingExportDest) Capabilities() types.DestinationCapabilities {
	return types.DestinationCapabilities{
		Signals:     []types.ExportSignalKind{types.SignalSpan},
		Replay:      types.ReplayDedupByID,
		Temporality: types.TemporalityCumulative,
	}
}

func (d *blockingExportDest) Export(ctx context.Context, recs []types.ExportRecord) ([]types.RecordOutcome, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(time.Hour):
		return nil, context.DeadlineExceeded
	}
}

// signalThenBlockDest returns accepted outcomes for every record it sees,
// writes one byte to the signal pipe after the outcomes are built, and then
// blocks forever. The worker therefore never returns from Export, so
// persistCursor never runs, which is exactly the pre-persist crash window.
type signalThenBlockDest struct {
	name   string
	signal *os.File
}

func (d *signalThenBlockDest) Name() string { return d.name }

func (d *signalThenBlockDest) Capabilities() types.DestinationCapabilities {
	return blockingExportDest{name: d.name}.Capabilities()
}

func (d *signalThenBlockDest) Export(ctx context.Context, recs []types.ExportRecord) ([]types.RecordOutcome, error) {
	out := make([]types.RecordOutcome, 0, len(recs))
	for _, rec := range recs {
		out = append(out, types.RecordOutcome{ID: rec.ID, Status: types.OutcomeAccepted})
	}
	if _, err := d.signal.Write([]byte{1}); err != nil {
		return nil, err
	}
	<-time.After(time.Hour)
	return out, nil
}

// countingAcceptDest accepts everything and records the IDs it saw per
// attempt, in order.
type countingAcceptDest struct {
	name string
	mu   chan struct{}
	sent [][]string
}

func newCountingAcceptDest(name string) *countingAcceptDest {
	return &countingAcceptDest{name: name, mu: make(chan struct{}, 1)}
}

func (d *countingAcceptDest) Name() string { return d.name }

func (d *countingAcceptDest) Capabilities() types.DestinationCapabilities {
	return blockingExportDest{name: d.name}.Capabilities()
}

func (d *countingAcceptDest) lock() {
	d.mu <- struct{}{}
}

func (d *countingAcceptDest) unlock() {
	<-d.mu
}

func (d *countingAcceptDest) Export(ctx context.Context, recs []types.ExportRecord) ([]types.RecordOutcome, error) {
	out := make([]types.RecordOutcome, 0, len(recs))
	ids := make([]string, 0, len(recs))
	for _, rec := range recs {
		ids = append(ids, string(rec.ID))
		out = append(out, types.RecordOutcome{ID: rec.ID, Status: types.OutcomeAccepted})
	}
	d.lock()
	d.sent = append(d.sent, ids)
	d.unlock()
	return out, nil
}

func (d *countingAcceptDest) attempts() [][]string {
	d.lock()
	defer d.unlock()
	out := make([][]string, len(d.sent))
	copy(out, d.sent)
	return out
}

// readLines reads exactly want newline-terminated lines from r, bounded by a
// hard deadline so a hung child fails the test instead of hanging it.
func readLines(t *testing.T, r *os.File, want int, stderr *bytes.Buffer) []string {
	t.Helper()
	lines := make(chan string, want)
	go func() {
		sc := bufio.NewScanner(r)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	got := make([]string, 0, want)
	deadline := time.After(60 * time.Second)
	for len(got) < want {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatalf("child stdout closed after %d/%d lines; stderr: %s", len(got), want, stderr.String())
			}
			got = append(got, line)
		case <-deadline:
			t.Fatalf("timeout waiting for child stdout line %d/%d; stderr: %s", len(got), want, stderr.String())
		}
	}
	return got
}

func walCrashChildCmd(t *testing.T, mode, dir string, extra *os.File) (*exec.Cmd, *os.File, *os.File, *bytes.Buffer) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Skipf("cannot re-exec the test binary: %v", err)
	}
	cmd := exec.Command(exe, "-test.run=TestWalCrashProcessHelper$", "-test.timeout=90s")
	cmd.Env = append(os.Environ(), walCrashEnv+"="+mode, "GOHAN_WAL_CRASH_DIR="+dir)
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatalf("child stdout pipe: %v", err)
	}
	cmd.Stdout = pw
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if extra != nil {
		cmd.ExtraFiles = []*os.File{extra}
	}
	t.Cleanup(func() { _ = pr.Close() })
	return cmd, pr, pw, &stderr
}

// walCrashWait reaps the killed child and fails the test if it exited on its
// own before the SIGKILL, which means the crash window was not entered.
func walCrashWait(t *testing.T, cmd *exec.Cmd, stderr *bytes.Buffer) {
	t.Helper()
	err := cmd.Wait()
	if err == nil {
		t.Fatalf("child exited cleanly before SIGKILL took effect; stderr: %s", stderr.String())
	}
	var ee *exec.ExitError
	if !errors.As(err, &ee) || !ee.ProcessState.Sys().(syscall.WaitStatus).Signaled() {
		t.Fatalf("child did not die by signal: %v; stderr: %s", err, stderr.String())
	}
}

func killCrashChild(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	if cmd.Process == nil {
		return
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Logf("kill child: %v", err)
	}
	_, _ = cmd.Process.Wait()
}

// walCrashChildOpen opens the child's spool with the mode-specific
// destination, shared with the parent through the spool directory.
func walCrashChildOpen(mode, dir string) (*telemetry.Spool, types.ExportDestination, error) {
	cfg := walConfig(1<<20, 1<<20)
	var dest types.ExportDestination
	switch mode {
	case walCrashModeAppend:
		dest = &blockingExportDest{name: "wal"}
	case walCrashModeCursor:
		f := os.NewFile(3, "crash-signal")
		if f == nil {
			return nil, nil, fmt.Errorf("signal pipe fd 3 missing")
		}
		dest = &signalThenBlockDest{name: "wal", signal: f}
	default:
		return nil, nil, fmt.Errorf("unknown crash mode %q", mode)
	}
	s, err := telemetry.OpenSpool(dir, []types.ExportDestination{dest}, cfg)
	if err != nil {
		return nil, nil, err
	}
	return s, dest, nil
}

func walCrashChildRun(mode, dir string) int {
	s, _, err := walCrashChildOpen(mode, dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "child %s: %v\n", mode, err)
		return 2
	}
	defer func() {
		// Never a graceful path: the parent SIGKILLs before this matters,
		// and Shutdown would defeat the crash window.
		_ = s
	}()
	ids := []string{"a1"}
	if mode == walCrashModeAppend {
		ids = []string{"a1", "a2", "a3"}
	}
	w := bufio.NewWriter(os.Stdout)
	for _, id := range ids {
		if err := s.Append(context.Background(), "wal", []types.ExportRecord{spanRecord(id)}); err != nil {
			fmt.Fprintf(os.Stderr, "child %s: Append %s: %v\n", mode, id, err)
			return 3
		}
		_, _ = fmt.Fprintf(w, "synced %s\n", id)
	}
	if err := w.Flush(); err != nil {
		fmt.Fprintf(os.Stderr, "child %s: stdout flush: %v\n", mode, err)
		return 3
	}
	if mode == walCrashModeCursor {
		// Start the delivery pass; it enters Export, signals the parent
		// through the pipe and then blocks, so the cursor is never persisted.
		go func() { _, _ = s.Flush(context.Background()) }()
	}
	<-time.After(time.Hour)
	return 0
}

func TestWalCrashProcessHelper(t *testing.T) {
	mode := os.Getenv(walCrashEnv)
	if mode == "" {
		return
	}
	dir := os.Getenv("GOHAN_WAL_CRASH_DIR")
	if dir == "" {
		os.Exit(3)
	}
	os.Exit(walCrashChildRun(mode, dir))
}

func TestWalCrashProcess(t *testing.T) {
	if testing.Short() {
		t.Skip("process crash windows re-exec the test binary; skipped under -short")
	}
	if os.Getenv(walCrashEnv) != "" {
		t.Skip("child mode reached without the helper gate")
	}
	t.Run("kill after synced append before delivery", func(t *testing.T) {
		dir := t.TempDir()
		cmd, stdout, pw, stderr := walCrashChildCmd(t, walCrashModeAppend, dir, nil)
		if err := cmd.Start(); err != nil {
			t.Fatalf("start child: %v", err)
		}
		_ = pw.Close()
		t.Cleanup(func() { killCrashChild(t, cmd) })

		lines := readLines(t, stdout, 3, stderr)
		for i, want := range []string{"synced a1", "synced a2", "synced a3"} {
			if lines[i] != want {
				t.Fatalf("child line %d: got %q, want %q", i, lines[i], want)
			}
		}
		if err := cmd.Process.Kill(); err != nil {
			t.Fatalf("SIGKILL child: %v", err)
		}
		walCrashWait(t, cmd, stderr)

		// The child died by SIGKILL, so OpenSpool succeeding proves the
		// flock was released and recovery can take over the directory.
		dest := newCountingAcceptDest("wal")
		s := walOpenDests(t, dir, nil, walConfig(1<<20, 1<<20), dest)
		res, err := s.Flush(t.Context())
		if err != nil {
			t.Fatalf("Flush after crash: %v", err)
		}
		if res.Delivered["wal"] != 3 {
			t.Fatalf("recovered pending records: got %d, want 3", res.Delivered["wal"])
		}
		atts := dest.attempts()
		if len(atts) != 1 {
			t.Fatalf("export attempts: got %d, want 1", len(atts))
		}
		want := []string{"a1", "a2", "a3"}
		if len(atts[0]) != len(want) {
			t.Fatalf("exported IDs: got %v, want %v", atts[0], want)
		}
		for i, id := range want {
			if atts[0][i] != id {
				t.Fatalf("exported ID %d: got %s, want %s", i, atts[0][i], id)
			}
		}
		if _, err := s.Shutdown(t.Context()); err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
	})

	t.Run("kill after remote acceptance before cursor persist", func(t *testing.T) {
		dir := t.TempDir()
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatalf("signal pipe: %v", err)
		}
		defer func() { _ = r.Close() }()
		cmd, stdout, pw, stderr := walCrashChildCmd(t, walCrashModeCursor, dir, w)
		if err := cmd.Start(); err != nil {
			t.Fatalf("start child: %v", err)
		}
		_ = pw.Close()
		t.Cleanup(func() { killCrashChild(t, cmd); _ = w.Close() })

		lines := readLines(t, stdout, 1, stderr)
		if lines[0] != "synced a1" {
			t.Fatalf("child line: got %q, want %q", lines[0], "synced a1")
		}
		// Bounded wait for the one-byte Export signal; a deadline here means
		// a hung child fails the test instead of hanging it.
		sig := make(chan error, 1)
		go func() {
			one := make([]byte, 1)
			_, err := r.Read(one)
			sig <- err
		}()
		select {
		case err := <-sig:
			if err != nil {
				t.Fatalf("read export signal: %v", err)
			}
		case <-time.After(60 * time.Second):
			t.Fatal("timeout waiting for child Export signal")
		}
		if err := cmd.Process.Kill(); err != nil {
			t.Fatalf("SIGKILL child: %v", err)
		}
		walCrashWait(t, cmd, stderr)

		// The child accepted remotely but never persisted the cursor, so the
		// record must replay on the next open (duplicate delivery permitted).
		dest := newCountingAcceptDest("wal")
		s := walOpenDests(t, dir, nil, walConfig(1<<20, 1<<20), dest)
		res, err := s.Flush(t.Context())
		if err != nil {
			t.Fatalf("Flush after crash: %v", err)
		}
		if res.Delivered["wal"] != 1 {
			t.Fatalf("replayed records: got %d, want 1", res.Delivered["wal"])
		}
		atts := dest.attempts()
		if len(atts) != 1 || len(atts[0]) != 1 || atts[0][0] != "a1" {
			t.Fatalf("replayed IDs: got %v, want [[a1]]", atts)
		}
		if _, err := s.Shutdown(t.Context()); err != nil {
			t.Fatalf("Shutdown: %v", err)
		}

		// Flush already persisted the cursor past the accepted record, so a
		// second open must not replay it.
		dest2 := newCountingAcceptDest("wal")
		s2 := walOpenDests(t, dir, nil, walConfig(1<<20, 1<<20), dest2)
		res2, err := s2.Flush(t.Context())
		if err != nil {
			t.Fatalf("second Flush: %v", err)
		}
		if res2.Delivered["wal"] != 0 {
			t.Fatalf("second replay: got %d, want 0", res2.Delivered["wal"])
		}
		if atts := dest2.attempts(); len(atts) != 0 {
			t.Fatalf("second open exports: got %v, want none", atts)
		}
		if _, err := s2.Shutdown(t.Context()); err != nil {
			t.Fatalf("second Shutdown: %v", err)
		}
	})
}
