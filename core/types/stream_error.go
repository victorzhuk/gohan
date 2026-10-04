package types

// TerminalError is the event that closes a stream after a permanent failure.
// The harness appends it to the EventLog and delivers it as the last stream
// event; a client that receives it must not expect a Done. NextSeq is the
// next EventMeta.Seq after the failure, so Attach(runID, NextSeq) resumes
// without re-reading any event the client already saw.
type TerminalError struct {
	Problem Problem
	NextSeq int64
}

func (TerminalError) isEvent() {}

// TerminalFailure converts a Go error into the terminal stream event,
// carrying the client-facing Problem and the resume sequence. Instance is
// the run id so the client can correlate the failure with the run.
func TerminalFailure(err error, runID string, nextSeq int64) TerminalError {
	p := ProblemOf(err)
	p.Instance = runID
	return TerminalError{Problem: p, NextSeq: nextSeq}
}

// InterruptedStream is the terminal event reported when a stream closes
// without a Done or TerminalError, for example because the connection
// dropped. It carries CodeStreamInterrupted, which clients treat as
// retryable, and the sequence to resume from.
func InterruptedStream(nextSeq int64) TerminalError {
	return TerminalError{
		Problem: buildProblem(catalogByCode[CodeStreamInterrupted], nil),
		NextSeq: nextSeq,
	}
}
