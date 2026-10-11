package gohan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/victorzhuk/gohan/core/guards"
	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

var (
	// errCompactionOversized reports a compaction whose persisted view still
	// exceeds the request budget after the fallback ran once; the stage fails
	// closed instead of invoking the provider.
	errCompactionOversized = errors.New("gohan: compaction did not fit the request budget")
	// errCompactionBoundary reports a compactor that returned a CoversUpTo
	// other than the selected prefix boundary; the stage persists nothing.
	errCompactionBoundary = errors.New("gohan: compactor returned a different CoversUpTo")
	// errCompactionGuard reports a compaction whose flow has no context
	// guard bound to the drive path. The stage refuses rather than
	// persisting an unguarded summary.
	errCompactionGuard = errors.New("gohan: compaction needs a context guard bound to the drive path")
	// errCompactionAppend reports a compaction whose flow lost the session
	// compaction append seam between the build gate and the drive.
	errCompactionAppend = errors.New("gohan: compaction needs a session log that implements stores.SessionCompactionAppender")
	// errCompactionMeasure reports a same-turn measurement that disagrees
	// with the durable publication payload; the stage publishes to no sink.
	errCompactionMeasure = errors.New("gohan: compaction tokens after the append do not match the durable publication")
	// errCompactionPublication reports a durable publication whose metadata
	// fails validation: the stage trusts no unverified payload.
	errCompactionPublication = errors.New("gohan: compaction publication is not trustworthy")
	// errCompactionReload reports a compaction whose flow lost the session
	// log reload seam between the build gate and the drive.
	errCompactionReload = errors.New("gohan: compaction needs a session log reload bound to the drive path")
)

// selectCompactionPrefix returns the absolute CoversUpTo boundary of the
// largest safe prefix of the loaded log: a completed tool pair is covered
// whole or left whole, a pending tool call is never covered, and an
// already-covered prefix is never re-covered. The boolean is false when no
// additional message can be covered.
func selectCompactionPrefix(msgs []types.Message) (int64, bool) {
	var prior int64
	for _, m := range msgs {
		for _, b := range m.Blocks {
			if c, ok := b.(types.Compaction); ok && c.CoversUpTo > prior {
				prior = c.CoversUpTo
			}
		}
	}
	open := map[string]bool{}
	best := prior
	for i, m := range msgs {
		if int64(i) < prior {
			continue
		}
		// A prior summary and an internal receipt or foreign admission
		// record never fold: their absolute history positions stay
		// uncovered. A prior summary does not stop the scan — the next
		// compaction must advance past it and fold only the ordinary
		// messages that follow, with the prior summary carried once by
		// the compactor itself.
		stop := false
		for _, b := range m.Blocks {
			switch v := b.(type) {
			case types.ToolUse:
				open[v.ID] = true
			case types.ToolResult:
				delete(open, v.ID)
			case types.Compaction:
				continue
			}
		}
		if _, ok := m.Meta[ApprovalReceiptKey]; ok {
			stop = true
		}
		if _, ok := m.Meta[foreignAdmissionKey]; ok {
			stop = true
		}
		if stop {
			break
		}
		// The prefix never covers the last message: the boundary leaves
		// the live tail of the conversation in place.
		if i == len(msgs)-1 {
			break
		}
		if len(open) == 0 {
			best = int64(i + 1)
		}
	}
	return best, best > prior
}

// compactionOriginOf names the origin a guard consultation carries: the
// summary blocks' own model origin when one declares it.
func compactionOriginOf(blocks []types.Block) types.Origin {
	for _, b := range blocks {
		switch v := b.(type) {
		case types.Text:
			return v.Origin
		case types.Compaction:
			return v.Origin
		}
	}
	return types.Origin{Kind: types.OriginModel}
}

// publicationMessage builds the durable compaction message: the summary
// block with the predetermined publication identity and the payload under
// the metadata key.
func publicationMessage(pubID string, comp types.Compaction, p stores.CompactionPublication) types.Message {
	comp.BlockBase = types.BlockBase{Origin: types.Origin{Kind: types.OriginModel}}
	return types.Message{
		ID:     pubID,
		Role:   types.RoleAssistant,
		Blocks: []types.Block{comp},
		Meta:   map[string]any{stores.MetadataPayloadKey: p},
	}
}

// decodePublicationMeta reads the durable publication payload of one loaded
// message. A message without a payload carries none; a payload present but
// not decodable is an error the reconciliation reports.
func decodePublicationMeta(m types.Message) (stores.CompactionPublication, bool, error) {
	raw, ok := m.Meta[stores.MetadataPayloadKey]
	if !ok || raw == nil {
		return stores.CompactionPublication{}, false, nil
	}
	if p, ok := raw.(stores.CompactionPublication); ok {
		return p, true, nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return stores.CompactionPublication{}, false, fmt.Errorf("gohan: compaction publication %s metadata: %w", m.ID, err)
	}
	var p stores.CompactionPublication
	if err := json.Unmarshal(encoded, &p); err != nil {
		return stores.CompactionPublication{}, false, fmt.Errorf("gohan: compaction publication %s metadata: %w", m.ID, err)
	}
	return p, true, nil
}

// validatePublication checks one durable publication against the session it
// loaded with before any sink trusts it: the session identity, the
// predetermined message and publication identity, the version relation and
// the summary checksum.
func validatePublication(sessionID string, m types.Message, p stores.CompactionPublication) error {
	if p.SessionID != sessionID {
		return fmt.Errorf("gohan: compaction publication %s names session %q, loaded %q: %w", p.PublicationID, p.SessionID, sessionID, errCompactionPublication)
	}
	if m.ID == "" || m.ID != p.PublicationID {
		return fmt.Errorf("gohan: compaction publication carries message id %q, payload %q: %w", m.ID, p.PublicationID, errCompactionPublication)
	}
	if want := stores.CompactionPublicationID(p.SessionID, p.RunID, p.FromVersion); p.PublicationID != want {
		return fmt.Errorf("gohan: compaction publication id %s, derived %s: %w", p.PublicationID, want, errCompactionPublication)
	}
	if p.ToVersion != p.FromVersion+1 {
		return fmt.Errorf("gohan: compaction publication versions %d to %d: %w", p.FromVersion, p.ToVersion, errCompactionPublication)
	}
	var summary []types.Block
	for _, b := range m.Blocks {
		if c, ok := b.(types.Compaction); ok {
			summary = c.Summary
			break
		}
	}
	sha, err := stores.CompactionSummarySHA(summary)
	if err != nil {
		return fmt.Errorf("gohan: compaction publication %s: %w", p.PublicationID, err)
	}
	if sha != p.SummarySHA {
		return fmt.Errorf("gohan: compaction publication %s checksum %s, payload %s: %w", p.PublicationID, sha, p.SummarySHA, errCompactionPublication)
	}
	return nil
}

// publishCompaction records one publication in the configured audit and
// event sinks under the payload's original publisher identity. A nil sink
// is skipped; an identical duplicate returns without a second record; a
// conflicting record is an error.
func publishCompaction(ctx context.Context, c turnConfig, p stores.CompactionPublication) error {
	if c.compactionAudit != nil {
		_, err := c.compactionAudit.AppendCompactionAudit(ctx, stores.AuditRecord{
			Kind:      stores.AuditCompacted,
			SessionID: p.SessionID,
			RunID:     p.RunID,
			RootRunID: p.RootRunID,
			Flow:      p.Flow,
			Tenant:    p.Tenant,
			Subject:   p.Subject,
			Model:     p.Policy,
			Compaction: &stores.CompactionAudit{
				PublicationID: p.PublicationID,
				FromVersion:   p.FromVersion,
				ToVersion:     p.ToVersion,
				TokensBefore:  p.TokensBefore,
				TokensAfter:   p.TokensAfter,
				Policy:        p.Policy,
				SummarySHA:    p.SummarySHA,
			},
		})
		if err != nil {
			return fmt.Errorf("compaction audit %s: %w", p.PublicationID, err)
		}
	}
	if c.compactionEvents != nil {
		_, err := c.compactionEvents.AppendCompactionEvent(ctx, p.PublicationID, p.RunID, stores.Event{
			Payload: types.Compacted{
				FromVersion:   p.FromVersion,
				ToVersion:     p.ToVersion,
				TokensBefore:  p.TokensBefore,
				TokensAfter:   p.TokensAfter,
				Policy:        p.Policy,
				PublicationID: p.PublicationID,
			},
			Meta: types.EventMeta{
				SessionID: p.SessionID,
				RunID:     p.RunID,
				RootRunID: p.RootRunID,
				Flow:      p.Flow,
			},
		})
		if err != nil {
			return fmt.Errorf("compaction event %s: %w", p.PublicationID, err)
		}
	}
	return nil
}

// reconcileCompactionPublications publishes every durable publication
// loaded with the session under its original publisher identity. It runs
// before every native provider call, independent of an active compactor or
// a disabled policy: the durable publication outlives the turn that
// published it. It validates every payload before it trusts the metadata,
// it propagates a configured sink's refusal before the provider call, and
// it skips an absent sink.
func reconcileCompactionPublications(ctx context.Context, c turnConfig, loaded []types.Message) error {
	info, _ := types.RunInfoFrom(ctx)
	for _, m := range loaded {
		p, ok, err := decodePublicationMeta(m)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if err := validatePublication(info.SessionID, m, p); err != nil {
			return err
		}
		if err := publishCompaction(ctx, c, p); err != nil {
			return err
		}
	}
	return nil
}

// budgetRatio returns the ratio the compaction decision passes to
// types.ContextBudget: the reciprocal of the profile's calibration, so a
// profile whose real usage exceeds its estimate gets a smaller ceiling and
// compacts sooner. The reciprocal is clamped to the calibration band; no
// calibration keeps the frozen zero ratio.
func budgetRatio(profile string, calib map[string]float64) float64 {
	v, ok := calib[profile]
	if !ok || v <= 0 {
		return 0
	}
	return clampCalibration(1 / v)
}

// clampCalibration clamps a calibration factor to the band the requirement
// fixes: a factor below 0.5 or above 2.0 is capped at the band edge.
func clampCalibration(v float64) float64 {
	if v < 0.5 {
		return 0.5
	}
	if v > 2.0 {
		return 2.0
	}
	return v
}

// runCompactionStage runs the persisted compaction for one model
// invocation. It decides the trigger over the full assembled request,
// selects the safe prefix, folds it through the policy compactor, guards
// the summary at StageContext, appends the guarded block and its
// publication payload through the session compaction seam, reassembles the
// same-turn request over the actual history and publishes the audit record
// and the event idempotently under the publishing run's identity. A guard
// rejection persists nothing: the injected fallback folds the working
// history once, the stage reassembles once, and an oversized result fails
// closed. The returned request replaces the caller's request for this turn.
func runCompactionStage(ctx context.Context, st *runtime.State, env *turnEnv, req types.ModelRequest) (types.ModelRequest, []types.Event, error) {
	c := env.c
	if c.compactor == nil || c.compactionCount == nil {
		return req, nil, nil
	}
	before, err := c.compactionCount(ctx, req)
	if err != nil {
		return types.ModelRequest{}, nil, fmt.Errorf("gohan: compaction count: %w", err)
	}
	budget := types.ContextBudget(c.profile, req, nil, budgetRatio(c.profile.Name, st.Calibration))
	if before <= budget.Limit {
		return req, nil, nil
	}
	boundary, ok := selectCompactionPrefix(env.msgs)
	if !ok {
		return req, nil, nil
	}
	info, _ := types.RunInfoFrom(ctx)

	comp, err := c.compactor.Compact(ctx, stores.History{Messages: env.msgs[:boundary], Version: boundary}, c.profile)
	if err != nil {
		return types.ModelRequest{}, nil, fmt.Errorf("gohan: compaction: %w", err)
	}
	if comp.CoversUpTo != boundary {
		return types.ModelRequest{}, nil, fmt.Errorf("gohan: compaction at boundary %d: %w", boundary, errCompactionBoundary)
	}

	if c.contextGuard == nil {
		return types.ModelRequest{}, nil, errCompactionGuard
	}
	d, err := ConsultGuard(ctx, c.contextGuard, guards.GuardInput{
		Stage:  types.StageContext,
		Blocks: comp.Summary,
		Origin: compactionOriginOf(comp.Summary),
		Run:    info,
	})
	if err != nil {
		return types.ModelRequest{}, nil, fmt.Errorf("gohan: compaction guard: %w", err)
	}
	summary := comp.Summary
	if d.Value.Action == guards.Rewrite {
		summary = d.Value.Blocks
	}
	if d.Value.Action == guards.Block {
		env.emitDelta(ctx, types.GuardBlocked{Stage: types.StageContext, Reason: d.Value.Reason})
		if tel := telemetryFrom(ctx); tel != nil {
			tel.Count(ctx, "gohan.guard.context_rejected", 1, types.String("provider", "compaction"))
		}
		return compactWithFallback(ctx, st, env)
	}
	comp.Summary = summary

	// The rendered final guarded summary counts separately for the
	// durable Compaction.Tokens record.
	stokens, err := c.compactionCount(ctx, types.ModelRequest{Messages: []types.Message{{Blocks: summary}}})
	if err != nil {
		return types.ModelRequest{}, nil, fmt.Errorf("gohan: compaction summary count: %w", err)
	}
	comp.Tokens = stokens

	from := st.HistoryVersion
	pubID := stores.CompactionPublicationID(info.SessionID, info.RunID, from)
	sha, err := stores.CompactionSummarySHA(summary)
	if err != nil {
		return types.ModelRequest{}, nil, fmt.Errorf("gohan: compaction summary: %w", err)
	}
	p := stores.CompactionPublication{
		PublicationID: pubID,
		SessionID:     info.SessionID,
		RunID:         info.RunID,
		RootRunID:     info.RootRunID,
		Flow:          info.Flow,
		Tenant:        string(info.Principal.Tenant),
		Subject:       info.Principal.Subject,
		FromVersion:   from,
		ToVersion:     from + 1,
		TokensBefore:  before,
		Policy:        comp.Model,
		SummarySHA:    sha,
	}

	// The prospective TokensAfter must reach a fixed point with the payload
	// it travels in before the append freezes it; the digit width of the
	// count is the only thing the payload itself can move. The assembly is
	// bounded to three passes; a failure to converge persists nothing.
	converged := false
	for range 3 {
		msg := publicationMessage(pubID, comp, p)
		prospective := append(slices.Clone(env.msgs), msg)
		n, err := countAssembled(ctx, env, prospective, from+1)
		if err != nil {
			return types.ModelRequest{}, nil, err
		}
		if n == p.TokensAfter {
			converged = true
			break
		}
		p.TokensAfter = n
	}
	if !converged {
		return types.ModelRequest{}, nil, fmt.Errorf("gohan: compaction tokens did not converge in three passes: %w", errCompactionMeasure)
	}

	if c.compactionAppend == nil {
		return types.ModelRequest{}, nil, errCompactionAppend
	}
	if c.compactionLoad == nil {
		return types.ModelRequest{}, nil, errCompactionReload
	}
	v, err := c.compactionAppend(ctx, info.SessionID, from, publicationMessage(pubID, comp, p))
	if err != nil {
		return types.ModelRequest{}, nil, fmt.Errorf("gohan: compaction append: %w", err)
	}
	// The append is the commit point: the stage reloads the durable history
	// once and works from the loaded values, never from a local copy.
	loaded, lv, err := c.compactionLoad(ctx, info.SessionID)
	if err != nil {
		return types.ModelRequest{}, nil, fmt.Errorf("gohan: compaction reload: %w", err)
	}
	if lv != v {
		return types.ModelRequest{}, nil, fmt.Errorf("gohan: compaction reload version %d, append version %d: %w", lv, v, errCompactionMeasure)
	}
	env.msgs = loaded
	st.HistoryVersion = v

	// The same turn measures the request it actually sends.
	req2, err := assembleTurnRequest(ctx, env, v)
	if err != nil {
		return types.ModelRequest{}, nil, err
	}
	after, err := c.compactionCount(ctx, req2)
	if err != nil {
		return types.ModelRequest{}, nil, fmt.Errorf("gohan: compaction count: %w", err)
	}
	if after != p.TokensAfter {
		return types.ModelRequest{}, nil, fmt.Errorf("gohan: compaction measured %d, durable %d: %w", after, p.TokensAfter, errCompactionMeasure)
	}
	if err := publishCompaction(ctx, c, p); err != nil {
		return types.ModelRequest{}, nil, err
	}
	if tel := telemetryFrom(ctx); tel != nil {
		tel.Count(ctx, "gohan.context.compactions", 1)
	}
	return req2, nil, nil
}

// compactWithFallback applies the injected fallback to the working history
// once, reassembles once, and refuses the provider call when the request
// still exceeds its budget. Nothing is persisted.
func compactWithFallback(ctx context.Context, st *runtime.State, env *turnEnv) (types.ModelRequest, []types.Event, error) {
	c := env.c
	if c.compactionFallback == nil {
		return types.ModelRequest{}, nil, errCompactionOversized
	}
	h, err := c.compactionFallback.Project(ctx, stores.History{Messages: env.msgs, Version: st.HistoryVersion}, c.profile)
	if err != nil {
		return types.ModelRequest{}, nil, fmt.Errorf("gohan: compaction fallback: %w", err)
	}
	env.msgs = h.Messages
	req2, err := assembleTurnRequest(ctx, env, st.HistoryVersion)
	if err != nil {
		return types.ModelRequest{}, nil, err
	}
	if c.compactionCount != nil {
		n, err := c.compactionCount(ctx, req2)
		if err != nil {
			return types.ModelRequest{}, nil, fmt.Errorf("gohan: compaction count: %w", err)
		}
		if n > types.ContextBudget(c.profile, req2, nil, budgetRatio(c.profile.Name, st.Calibration)).Limit {
			return types.ModelRequest{}, nil, errCompactionOversized
		}
	}
	return req2, nil, nil
}

// countAssembled assembles one candidate history at the given version and
// counts the full request the same transformations produce.
func countAssembled(ctx context.Context, env *turnEnv, msgs []types.Message, version int64) (int, error) {
	saved := env.msgs
	env.msgs = msgs
	defer func() { env.msgs = saved }()
	req, err := assembleTurnRequest(ctx, env, version)
	if err != nil {
		return 0, err
	}
	return env.c.compactionCount(ctx, req)
}
