package gohan

import (
	"context"
	"errors"
	"fmt"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// redactionBundle carries the redaction wiring one lifecycle binds: the
// redactor, its policy and the map store the redacted entities persist
// through. WithLifecycleRedaction folds its four arguments so the
// correlated triple travels as one value and the session id stays the
// separate identity field.
type redactionBundle struct {
	redactor types.Redactor
	policy   types.RedactionPolicy
	maps     stores.RedactionMaps
}

// WithLifecycleRedaction binds the redactor a suspension redacts the
// pending calls through, its policy, the map store the entity maps persist
// in, and the session id the maps key on.
func WithLifecycleRedaction(redactor types.Redactor, policy types.RedactionPolicy, maps stores.RedactionMaps, sessionID string) LifecycleOption {
	return func(lc *Lifecycle) {
		lc.redaction = redactionBundle{redactor: redactor, policy: policy, maps: maps}
		lc.sessionID = sessionID
	}
}

func (lc *Lifecycle) redactState(ctx context.Context, st runtime.State) (runtime.State, error) {
	if lc.redaction.redactor == nil || len(lc.redaction.policy.Detectors) == 0 {
		return st, nil
	}
	if len(st.Pending) == 0 {
		return st, nil
	}
	msgs := make([]types.Message, len(st.Pending))
	for i, call := range st.Pending {
		msgs[i] = types.Message{Blocks: []types.Block{call}}
	}
	redacted, err := lc.redactMessages(ctx, msgs)
	if err != nil {
		return st, err
	}
	for i := range st.Pending {
		if len(redacted[i].Blocks) > 0 {
			if call, ok := redacted[i].Blocks[0].(types.ToolUse); ok {
				st.Pending[i] = call
			}
		}
	}
	return st, nil
}

func (lc *Lifecycle) redactMessages(ctx context.Context, msgs []types.Message) ([]types.Message, error) {
	if lc.redaction.redactor == nil || len(lc.redaction.policy.Detectors) == 0 {
		return msgs, nil
	}
	blocks := make([]types.Block, 0)
	for _, msg := range msgs {
		blocks = append(blocks, msg.Blocks...)
	}
	redacted, m, err := lc.redaction.redactor.Redact(ctx, blocks, lc.redaction.policy)
	if err != nil {
		return nil, fmt.Errorf("redact history: %w", err)
	}
	if lc.redaction.maps == nil {
		return nil, fmt.Errorf("redaction map store missing")
	}
	info, _ := types.RunInfoFrom(ctx)
	info.SessionID = lc.sessionID
	m.Session, m.Tenant = lc.sessionID, info.Principal.Tenant
	if old, err := lc.redaction.maps.Get(ctx, info.Principal.Tenant, lc.sessionID); err == nil {
		for token, entity := range old.Entries {
			if m.Entries == nil {
				m.Entries = make(map[string]types.Entity)
			}
			m.Entries[token] = entity
		}
	} else if !errors.Is(err, stores.ErrRedactionMapNotFound) {
		return nil, fmt.Errorf("load redaction map: %w", err)
	}
	if err := lc.redaction.maps.Put(ctx, info.Principal.Tenant, lc.sessionID, m); err != nil {
		return nil, fmt.Errorf("store redaction map: %w", err)
	}
	out := append([]types.Message(nil), msgs...)
	offset := 0
	for i := range out {
		n := len(out[i].Blocks)
		out[i].Blocks = append([]types.Block(nil), redacted[offset:offset+n]...)
		offset += n
	}
	return out, nil
}
