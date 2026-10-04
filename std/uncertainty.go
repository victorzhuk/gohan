package std

import (
	"fmt"
	"strings"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// UncertainOutcomeError reports that a run ended with journal entries whose
// outcome is still unknown: the effect may or may not have happened. Out
// carries whatever partial output the run produced; Keys names the calls a
// caller must verify before retrying.
type UncertainOutcomeError struct {
	Out  any
	Keys []types.CallKey
}

func (e *UncertainOutcomeError) Error() string {
	ids := make([]string, len(e.Keys))
	for i, k := range e.Keys {
		ids[i] = k.CallID
	}
	return fmt.Sprintf("outcome uncertain for %d call(s): %s", len(e.Keys), strings.Join(ids, ", "))
}

// NewUncertainOutcomeError builds the error for the given keys.
func NewUncertainOutcomeError(out any, keys ...types.CallKey) *UncertainOutcomeError {
	return &UncertainOutcomeError{Out: out, Keys: keys}
}

// UncertainKeys returns the keys of the entries whose result is still
// unknown, in order.
func UncertainKeys(sessionID string, entries []stores.Entry) []types.CallKey {
	var keys []types.CallKey
	for _, e := range entries {
		if e.Result.Outcome == types.Unknown {
			keys = append(keys, types.CallKey{SessionID: sessionID, CallID: e.Key})
		}
	}
	return keys
}
