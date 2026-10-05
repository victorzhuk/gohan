package gohan

import (
	"bytes"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"

	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

const checkpointEnvelopeVersion = 1

type checkpointEnvelope struct {
	Version    int                  `json:"version"`
	Run        types.RunInfo        `json:"run"`
	Generation uint64               `json:"generation"`
	State      runtime.State        `json:"state"`
	Approvals  []checkpointApproval `json:"approvals,omitempty"`
	Input      *types.InputRequest  `json:"input,omitempty"`
}

type checkpointApproval struct {
	Call        types.ToolUse          `json:"call"`
	Risk        types.RiskTier         `json:"risk"`
	Fingerprint stores.Fingerprint     `json:"fingerprint"`
	Reversible  bool                   `json:"reversible"`
	Eligible    permission.Eligibility `json:"eligible"`
	ApprovedBy  []types.Principal      `json:"approved_by,omitempty"`
}

var envelopeKeys = map[string]bool{
	"version":    true,
	"run":        true,
	"generation": true,
	"state":      true,
	"approvals":  true,
	"input":      true,
}

func encodeCheckpoint(env checkpointEnvelope) ([]byte, error) {
	env.Version = checkpointEnvelopeVersion
	data, err := jsonv2.Marshal(env)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", types.ErrCheckpointIncompatible, err)
	}
	return data, nil
}

// decodeCheckpoint decodes cp.Data as either a versioned envelope or a
// legacy raw runtime.State, validates its binding against the persisted
// checkpoint, the receiving flow spec and the receiving runtime, and
// returns the envelope plus the resumable state. It is the single decoder
// shared by resume and recovery.
func decodeCheckpoint(cp stores.Checkpoint, rt runtime.Runtime, spec string) (checkpointEnvelope, runtime.State, error) {
	if len(bytes.TrimSpace(cp.Data)) == 0 {
		return checkpointEnvelope{}, runtime.State{}, fmt.Errorf("%w: empty checkpoint data", types.ErrCheckpointIncompatible)
	}
	dec := jsontext.NewDecoder(bytes.NewReader(cp.Data))
	if dec.PeekKind() != '{' {
		return checkpointEnvelope{}, runtime.State{}, fmt.Errorf("%w: checkpoint data is not a JSON object", types.ErrCheckpointIncompatible)
	}
	keys, err := readTopLevelKeys(dec)
	if err != nil {
		if errors.Is(err, jsontext.ErrDuplicateName) {
			return checkpointEnvelope{}, runtime.State{}, fmt.Errorf("%w: duplicate object key", types.ErrCheckpointIncompatible)
		}
		return checkpointEnvelope{}, runtime.State{}, fmt.Errorf("%w: malformed checkpoint data", types.ErrCheckpointIncompatible)
	}
	if dec.PeekKind() != 0 {
		return checkpointEnvelope{}, runtime.State{}, fmt.Errorf("%w: trailing value after checkpoint object", types.ErrCheckpointIncompatible)
	}

	hasEnvelope := false
	for _, k := range keys {
		if envelopeKeys[k] {
			hasEnvelope = true
			break
		}
	}

	if hasEnvelope {
		return decodeEnvelope(cp, rt, spec)
	}
	return decodeLegacyRawState(cp, keys)
}

func decodeEnvelope(cp stores.Checkpoint, rt runtime.Runtime, spec string) (checkpointEnvelope, runtime.State, error) {
	var env checkpointEnvelope
	if err := jsonv2.Unmarshal(cp.Data, &env); err != nil {
		return checkpointEnvelope{}, runtime.State{}, fmt.Errorf("%w: malformed envelope: %s", types.ErrCheckpointIncompatible, err)
	}
	if env.Version != checkpointEnvelopeVersion {
		return checkpointEnvelope{}, runtime.State{}, fmt.Errorf("%w: unsupported envelope version %d", types.ErrCheckpointIncompatible, env.Version)
	}
	if env.Run.RunID == "" || env.Run.RunID != cp.RunID {
		return checkpointEnvelope{}, runtime.State{}, fmt.Errorf("%w: run id mismatch", types.ErrCheckpointIncompatible)
	}
	if env.Run.SessionID == "" || env.Run.SessionID != cp.SessionID {
		return checkpointEnvelope{}, runtime.State{}, fmt.Errorf("%w: session id mismatch", types.ErrCheckpointIncompatible)
	}
	if env.Run.Flow == "" || cp.Flow == "" || env.Run.Flow != cp.Flow || cp.Flow != spec {
		return checkpointEnvelope{}, runtime.State{}, fmt.Errorf("%w: flow mismatch", types.ErrTokenMismatch)
	}
	if rt == nil || cp.Backend != rt.Name() {
		return checkpointEnvelope{}, runtime.State{}, fmt.Errorf("%w: backend mismatch", types.ErrTokenMismatch)
	}
	if env.Generation == 0 {
		return checkpointEnvelope{}, runtime.State{}, fmt.Errorf("%w: missing argument generation", types.ErrCheckpointIncompatible)
	}
	if env.State.Turn < 0 || env.State.HistoryVersion < 0 {
		return checkpointEnvelope{}, runtime.State{}, fmt.Errorf("%w: negative turn or history version", types.ErrCheckpointIncompatible)
	}
	if err := validatePending(env.State.Pending, env.Approvals); err != nil {
		return checkpointEnvelope{}, runtime.State{}, err
	}
	return env, env.State, nil
}

func validatePending(pending []types.ToolUse, approvals []checkpointApproval) error {
	seen := map[string]bool{}
	for _, call := range pending {
		if call.ID == "" || call.Name == "" || len(call.Args) == 0 {
			return fmt.Errorf("%w: pending call missing id, name or arguments", types.ErrCheckpointIncompatible)
		}
		if seen[call.ID] {
			return fmt.Errorf("%w: duplicate pending call id %q", types.ErrCheckpointIncompatible, call.ID)
		}
		seen[call.ID] = true
	}
	approvedIDs := map[string]bool{}
	for _, ap := range approvals {
		if ap.Call.ID == "" || !seen[ap.Call.ID] {
			return fmt.Errorf("%w: approval without a matching pending call", types.ErrCheckpointIncompatible)
		}
		if approvedIDs[ap.Call.ID] {
			return fmt.Errorf("%w: duplicate approval for call %q", types.ErrCheckpointIncompatible, ap.Call.ID)
		}
		approvedIDs[ap.Call.ID] = true
	}
	return nil
}

func decodeLegacyRawState(cp stores.Checkpoint, keys []string) (checkpointEnvelope, runtime.State, error) {
	known := map[string]bool{
		"Turn":           true,
		"HistoryVersion": true,
		"Pending":        true,
		"ActiveTools":    true,
		"Flags":          true,
		"Usage":          true,
		"Calibration":    true,
		"Backend":        true,
	}
	isState := false
	for _, k := range keys {
		if known[k] {
			isState = true
			break
		}
	}
	if !isState {
		return checkpointEnvelope{}, runtime.State{}, fmt.Errorf("%w: unrecognized checkpoint format", types.ErrCheckpointIncompatible)
	}
	if cp.Reason == types.HumanApproval {
		return checkpointEnvelope{}, runtime.State{}, fmt.Errorf("%w: legacy raw state cannot prove the approval request", types.ErrCheckpointIncompatible)
	}
	if cp.RunID == "" || cp.SessionID == "" {
		return checkpointEnvelope{}, runtime.State{}, fmt.Errorf("%w: legacy raw state without a persisted run identity", types.ErrCheckpointIncompatible)
	}
	var st runtime.State
	if err := jsonv2.Unmarshal(cp.Data, &st); err != nil {
		return checkpointEnvelope{}, runtime.State{}, fmt.Errorf("%w: malformed legacy state: %s", types.ErrCheckpointIncompatible, err)
	}
	if st.Turn < 0 || st.HistoryVersion < 0 {
		return checkpointEnvelope{}, runtime.State{}, fmt.Errorf("%w: negative turn or history version", types.ErrCheckpointIncompatible)
	}
	if err := validatePending(st.Pending, nil); err != nil {
		return checkpointEnvelope{}, runtime.State{}, err
	}
	return checkpointEnvelope{}, st, nil
}

func readTopLevelKeys(dec *jsontext.Decoder) ([]string, error) {
	if _, err := dec.ReadToken(); err != nil {
		return nil, err
	}
	var keys []string
	for {
		if dec.PeekKind() == '}' {
			if _, err := dec.ReadToken(); err != nil {
				return nil, err
			}
			return keys, nil
		}
		tok, err := dec.ReadToken()
		if err != nil {
			return nil, err
		}
		if tok.Kind() != '"' {
			return nil, fmt.Errorf("expected object name")
		}
		name := tok.String()
		for _, k := range keys {
			if k == name {
				return nil, jsontext.ErrDuplicateName
			}
		}
		keys = append(keys, name)
		if err := dec.SkipValue(); err != nil {
			return nil, err
		}
	}
}
