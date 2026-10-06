package std

import (
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

type UncertainOutcomeError = types.UncertainOutcomeError

// NewUncertainOutcomeError builds the error for the given keys.
func NewUncertainOutcomeError(out any, keys ...types.CallKey) *UncertainOutcomeError {
	return &UncertainOutcomeError{Out: out, Uncertain: keys}
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
