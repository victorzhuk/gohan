package types

import (
	"testing"
)

func TestStreamErrors(t *testing.T) {
	t.Run("streams.terminal-error-event", func(t *testing.T) {
		te := TerminalFailure(&AbortError{Reason: "user aborted"}, "run-1", 11)
		p := te.Problem
		if p.Code != CodeAborted || p.Kind != Permanent {
			t.Fatalf("terminal problem must carry code and kind, got %+v", p)
		}
		if p.Instance != "run-1" {
			t.Fatalf("Instance = %q, want the run id", p.Instance)
		}
		if te.NextSeq != 11 {
			t.Fatalf("NextSeq = %d, want 11 (the next EventMeta.Seq)", te.NextSeq)
		}
	})

	t.Run("streams.close-without-done-is-interrupted", func(t *testing.T) {
		te := InterruptedStream(11)
		if te.Problem.Code != CodeStreamInterrupted {
			t.Fatalf("Code = %q, want %q", te.Problem.Code, CodeStreamInterrupted)
		}
		var ev Event = te
		if _, clean := ev.(Done); clean {
			t.Fatal("close without done must be reported as interrupted, not as a clean end")
		}
		if te.Problem.Kind != Retryable {
			t.Fatal("interrupted problem must carry a kind")
		}
	})
}
