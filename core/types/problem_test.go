package types

import (
	"errors"
	"testing"
)

func TestProblemCatalog(t *testing.T) {
	t.Run("errors.every-sentinel-has-code", func(t *testing.T) {
		covered := []error{
			ErrNoPrincipal, ErrSessionForbidden, ErrApproverNotEligible, ErrRunActive,
			ErrRunNotActive, ErrSessionHeld, ErrMailboxFull, ErrResumeInsideRun,
			ErrOperationExists, ErrTokenConsumed, ErrTokenExpired, ErrTokenMismatch,
			ErrInputInvalid, ErrNotSuspendable, ErrEmptyHistory, ErrSessionHandedOff,
			ErrBlobTooLarge, ErrShuttingDown, ErrCheckpointIncompatible, ErrVersionConflict,
			ErrStructuredOutput, ErrToolName, ErrToolDescription, ErrMemoryStoreRequired,
			ErrNoTokenEstimator, ErrSessionIndexRequired, ErrSchemaTooOld, ErrPartitionMissing,
			ErrShutdownIncomplete, ErrEgressPolicyRequired, ErrNoProviderKey,
			ErrManifestDrift{}, ErrToolCollision{}, ErrToolSetDrift{},
			&AbortError{Reason: "user"}, &GuardBlockedError{Stage: StageInput},
			&LimitExceededError{Limit: "MaxTurns"}, &ModelError{Class: ClassTransient},
			&ModelError{Class: ClassRateLimited}, &ModelError{Class: ClassContextOverflow},
			&ModelError{Class: ClassContentPolicy}, &ModelError{Class: ClassAuth},
			&ModelError{Class: ClassVersionDrift},
			&PartialError{}, &UncertainOutcomeError{},
			&StepError{Step: "retry", Err: errors.New("boom")},
		}
		for _, err := range covered {
			p := ProblemOf(err)
			if p.Code == "" || p.Code == CodeInternal {
				t.Errorf("%v: got internal code, want a catalog row", err)
				continue
			}
			if _, ok := catalogByCode[p.Code]; !ok {
				t.Errorf("%v: code %q has no catalog row", err, p.Code)
			}
		}
		// Innermost known error wins: a StepError wrapping a ModelError
		// reports the model code.
		p := ProblemOf(&StepError{Step: "model", Err: &ModelError{Class: ClassTransient}})
		if p.Code != CodeModelUnavailable {
			t.Errorf("innermost: got %q, want %q", p.Code, CodeModelUnavailable)
		}
		p = ProblemOf(&StepError{Step: "retry", Err: errors.New("boom")})
		if p.Code != CodeChainStep {
			t.Errorf("unknown inner: got %q, want %q", p.Code, CodeChainStep)
		}
	})
	t.Run("errors.problem-of-unknown-is-internal", func(t *testing.T) {
		p := ProblemOf(errors.New("boom"))
		if p.Code != CodeInternal {
			t.Errorf("Code = %q, want %q", p.Code, CodeInternal)
		}
		if p.Status != 500 {
			t.Errorf("Status = %d, want 500", p.Status)
		}
		if p.Detail != "internal error" {
			t.Errorf("Detail = %q, want %q", p.Detail, "internal error")
		}
	})
}
