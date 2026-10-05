package gohan

import (
	"context"
	"fmt"
	"slices"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// RunView is the owner-checked answer to Inspect: the stored run row plus
// the resume input a suspended run waits for. It carries stores data only,
// so any pod can answer it; no limits-remaining value is derived.
type RunView struct {
	stores.Run
	Input *stores.ResumeInput
}

// runByIDReader is the optional read-by-id surface on a Runs store. Only
// ByOperation is required; a store without ByID cannot answer Inspect.
type runByIDReader interface {
	ByID(ctx context.Context, runID string) (stores.Run, error)
}

// errInspectStoresRequired marks an Inspect against a stack built without
// the runs or session store: there is nothing to read from.
var errInspectStoresRequired = fmt.Errorf("inspect stack: %w", types.ErrRunNotActive)

// Inspect returns the stored run and the input its suspension waits on,
// reading the runs, session-log and checkpoint stores only. The caller's
// principal must own the run's session — its tenant, and either its
// subject or a session:read scope — or the run is refused with
// ErrSessionForbidden; without a principal at all, ErrNoPrincipal.
func (s *Stack) Inspect(ctx context.Context, runID string) (RunView, error) {
	if s.stores.Runs == nil || s.stores.SessionLog == nil {
		return RunView{}, errInspectStoresRequired
	}
	byID, ok := s.stores.Runs.(runByIDReader)
	if !ok {
		return RunView{}, errInspectStoresRequired
	}
	if err := requirePrincipal(ctx, s.allowAnonymous); err != nil {
		return RunView{}, err
	}
	p, _ := types.PrincipalFrom(ctx)
	run, err := byID.ByID(ctx, runID)
	if err != nil {
		return RunView{}, fmt.Errorf("inspect run %s: %w", runID, err)
	}
	reader, ok := s.stores.SessionLog.(SessionOwnerReader)
	if !ok {
		return RunView{}, fmt.Errorf("inspect run %s: session owner lookup is not wired: %w", runID, types.ErrSessionForbidden)
	}
	owner, err := reader.Owner(ctx, run.SessionID)
	if err != nil {
		return RunView{}, fmt.Errorf("inspect run %s: %w", runID, err)
	}
	if p.Tenant != owner.Tenant {
		return RunView{}, fmt.Errorf("inspect run %s: tenant %s: %w", runID, p.Tenant, types.ErrSessionForbidden)
	}
	if p.Subject != owner.Subject && !slices.Contains(p.Scopes, types.ScopeSessionRead) {
		return RunView{}, fmt.Errorf("inspect run %s: subject %s: %w", runID, p.Subject, types.ErrSessionForbidden)
	}
	view := RunView{Run: run}
	if s.stores.Checkpoints != nil {
		if _, in, err := s.stores.Checkpoints.PendingInput(ctx, runID); err == nil {
			view.Input = &in
		}
	}
	return view, nil
}
