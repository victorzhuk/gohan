package stores

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

// TrailRecord is one decision in the reconstructed trail: the audit record
// plus its position in the session's chain.
type TrailRecord struct {
	Seq int
	Rec AuditRecord
}

// ChainBreak names the record whose hash no longer matches its chain
// position.
type ChainBreak struct {
	Seq    int
	Kind   AuditKind
	At     time.Time
	Reason string
}

// DecisionTrail is the ordered decision trail of one session: who ran what,
// for whom, which tools were allowed, denied or asked, who approved, and
// which model, prompt and toolset versions were in force.
type DecisionTrail struct {
	SessionID string
	Owner     types.SessionOwner
	Records   []TrailRecord
	Broken    *ChainBreak
}

// Reconstruct joins the audit log and the session log into an ordered
// decision trail for sessionID, verifying the per-session hash chain as it
// reads. A modified record is reported in Broken; the trail is still
// returned up to and past the break.
func Reconstruct(ctx context.Context, audit AuditLog, session SessionLog, sessionID string) (DecisionTrail, error) {
	hist, err := session.Load(ctx, sessionID)
	if err != nil {
		return DecisionTrail{}, fmt.Errorf("gohan: reconstruct %s: %w", sessionID, err)
	}

	trail := DecisionTrail{SessionID: sessionID, Owner: hist.Owner}
	var prevHash string
	broken := false
	for rec, rerr := range audit.Read(ctx, sessionID) {
		if rerr != nil {
			return DecisionTrail{}, fmt.Errorf("gohan: reconstruct %s: %w", sessionID, rerr)
		}
		seq := len(trail.Records) + 1
		if !broken {
			check := rec
			check.PrevHash = prevHash
			if got := auditHash(check); got != rec.Hash {
				trail.Broken = &ChainBreak{
					Seq:    seq,
					Kind:   rec.Kind,
					At:     rec.At,
					Reason: fmt.Sprintf("hash mismatch at record %d (%s)", seq, rec.Kind),
				}
				broken = true
			} else if rec.PrevHash != prevHash {
				trail.Broken = &ChainBreak{
					Seq:    seq,
					Kind:   rec.Kind,
					At:     rec.At,
					Reason: fmt.Sprintf("chain link mismatch at record %d (%s)", seq, rec.Kind),
				}
				broken = true
			}
		}
		trail.Records = append(trail.Records, TrailRecord{Seq: seq, Rec: rec})
		prevHash = rec.Hash
	}

	sort.SliceStable(trail.Records, func(i, j int) bool {
		return trail.Records[i].Rec.At.Before(trail.Records[j].Rec.At)
	})
	for i := range trail.Records {
		trail.Records[i].Seq = i + 1
	}
	if trail.Broken != nil {
		// Re-anchor the break to the sorted position so the report names the
		// record's place in the delivered trail.
		at := trail.Broken.At
		for i, tr := range trail.Records {
			if tr.Rec.At.Equal(at) {
				trail.Broken.Seq = i + 1
				break
			}
		}
	}
	return trail, nil
}
