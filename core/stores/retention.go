package stores

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// AuditPurge marks the per-tenant record the retention sweep appends with
// the policy in force and the per-store counts. It carries no content.
const AuditPurge AuditKind = "purge"

// ErrRetentionAuditShort is returned when a policy sets the Audit tier
// shorter than the Conversation tier; Build refuses such a policy.
var ErrRetentionAuditShort = errors.New("gohan: audit retention is shorter than conversation")

// RetentionPolicy is one stack's retention: the Conversation tier covers
// everything keyed by a session, the Working tier notes and subject memory,
// the Audit tier the decision trail. A zero Conversation tier is zero
// retention: the conversation records go at Finish.
type RetentionPolicy struct {
	Conversation time.Duration
	Working      time.Duration
	Audit        time.Duration
}

// Validate reports a policy the stack refuses to build. A zero Working or
// Audit tier keeps that tier forever.
func (p RetentionPolicy) Validate() error {
	if p.Audit < p.Conversation {
		return fmt.Errorf("%w: audit %s < conversation %s", ErrRetentionAuditShort, p.Audit, p.Conversation)
	}
	return nil
}

// RetentionSource overrides the stack policy per tenant. A false second
// result keeps the stack default.
type RetentionSource interface {
	Retention(ctx context.Context, tenant string) (RetentionPolicy, bool)
}

// MaintainReport sums one sweep: per-store purged record counts and the
// sessions a hold kept.
type MaintainReport struct {
	Purged map[string]int
	Held   int
}

// SessionRetentionLister lists a tenant's sessions with the metadata
// retention judges on. Consumer-owned: the session log adapter implements it.
type SessionRetentionLister interface {
	RetentionSessions(ctx context.Context, tenant string) ([]SessionMeta, error)
}

// RetentionAging answers whether a session must be kept despite its age: a
// Running run never ages and a Suspended one is kept until its checkpoint's
// ExpiresAt passes. Consumer-owned: the runs and checkpoint adapters
// implement it.
type RetentionAging interface {
	Kept(ctx context.Context, sessionID string, now time.Time) (bool, error)
}

// SessionPurge deletes one store's conversation-tier records for a session
// and reports how many records it removed.
type SessionPurge func(ctx context.Context, tenant, sessionID string) (int, error)

// NamedPurge pairs a session purge with the store key that names its count
// in the purge audit record.
type NamedPurge struct {
	Store string
	Purge SessionPurge
}

// TierPurge deletes one store's tenant-scoped records older than cut and
// reports how many it removed. The Working tier uses it for notes and
// subject memory.
type TierPurge struct {
	Store string
	Purge func(ctx context.Context, tenant string, olderThan time.Time) (int, error)
}

// RetentionDeps injects everything the sweep needs. Stack.Maintain wires the
// real stores into it; tests drive it with fakes and the injected clock.
type RetentionDeps struct {
	Now func() time.Time
	// Tenants lists the tenants to sweep.
	Tenants  func(ctx context.Context) ([]string, error)
	Sessions SessionRetentionLister
	Aging    RetentionAging
	Source   RetentionSource
	// Session holds one entry per conversation-tier store.
	Session []NamedPurge
	// Working holds the Working-tier stores, typically notes and subject
	// memory.
	Working []TierPurge
	Trail   AuditLog
}

func retentionPolicy(ctx context.Context, deps RetentionDeps, def RetentionPolicy, tenant string) (RetentionPolicy, error) {
	policy := def
	if deps.Source != nil {
		if p, ok := deps.Source.Retention(ctx, tenant); ok {
			policy = p
		}
	}
	if err := policy.Validate(); err != nil {
		return RetentionPolicy{}, fmt.Errorf("gohan: retention %s: %w", tenant, err)
	}
	return policy, nil
}

// Sweep runs every store's purge tenant by tenant under the tenant's
// policy: conversation-tier stores purge session by session from
// SessionMeta.LastActivity, Working and Audit tiers purge by their own
// clock, held sessions are skipped and counted in Held. One purge audit
// record per tenant carries the policy and the per-store counts.
func Sweep(ctx context.Context, deps RetentionDeps, def RetentionPolicy) (MaintainReport, error) {
	if err := def.Validate(); err != nil {
		return MaintainReport{}, err
	}
	if deps.Now == nil {
		return MaintainReport{}, errors.New("gohan: retention sweep needs a clock")
	}
	now := deps.Now()
	report := MaintainReport{Purged: map[string]int{}}
	tenants, err := deps.Tenants(ctx)
	if err != nil {
		return report, fmt.Errorf("gohan: retention tenants: %w", err)
	}
	for _, tenant := range tenants {
		if err := ctx.Err(); err != nil {
			return report, fmt.Errorf("gohan: retention sweep: %w", err)
		}
		policy, err := retentionPolicy(ctx, deps, def, tenant)
		if err != nil {
			return report, err
		}
		if err := sweepTenant(ctx, deps, now, policy, tenant, &report); err != nil {
			return report, err
		}
	}
	return report, nil
}

func sweepTenant(ctx context.Context, deps RetentionDeps, now time.Time, policy RetentionPolicy, tenant string, report *MaintainReport) error {
	counts := map[string]int{}
	held := 0
	sessions, err := deps.Sessions.RetentionSessions(ctx, tenant)
	if err != nil {
		return fmt.Errorf("gohan: retention sessions %s: %w", tenant, err)
	}
	for _, m := range sessions {
		if m.Hold != "" {
			held++
			continue
		}
		if m.Pinned || m.Archived {
			continue
		}
		if deps.Aging != nil {
			kept, err := deps.Aging.Kept(ctx, m.ID, now)
			if err != nil {
				return fmt.Errorf("gohan: retention aging %s: %w", m.ID, err)
			}
			if kept {
				continue
			}
		}
		if now.Sub(m.LastActivity) < policy.Conversation {
			continue
		}
		for _, d := range deps.Session {
			n, err := d.Purge(ctx, tenant, m.ID)
			if err != nil {
				return fmt.Errorf("gohan: retention purge %s session %s: %w", d.Store, m.ID, err)
			}
			if n > 0 {
				counts[d.Store] += n
			}
		}
	}
	if policy.Working > 0 {
		if err := purgeTier(ctx, deps.Working, tenant, now.Add(-policy.Working), counts); err != nil {
			return err
		}
	}
	if policy.Audit > 0 && deps.Trail != nil {
		n, err := deps.Trail.Purge(ctx, tenant, now.Add(-policy.Audit))
		if err != nil {
			return fmt.Errorf("gohan: retention audit %s: %w", tenant, err)
		}
		if n > 0 {
			counts["audit"] += n
		}
	}
	for store, n := range counts {
		if n > 0 {
			report.Purged[store] += n
		}
	}
	report.Held += held
	detail, err := json.Marshal(map[string]any{
		"policy": policy,
		"purged": counts,
		"held":   held,
	})
	if err != nil {
		return fmt.Errorf("gohan: retention detail %s: %w", tenant, err)
	}
	if err := deps.Trail.Append(ctx, AuditRecord{
		Kind:     AuditPurge,
		Tenant:   tenant,
		Decision: string(detail),
	}); err != nil {
		return fmt.Errorf("gohan: retention record %s: %w", tenant, err)
	}
	return nil
}

func purgeTier(ctx context.Context, purges []TierPurge, tenant string, olderThan time.Time, counts map[string]int) error {
	for _, d := range purges {
		n, err := d.Purge(ctx, tenant, olderThan)
		if err != nil {
			return fmt.Errorf("gohan: retention purge %s: %w", d.Store, err)
		}
		if n > 0 {
			counts[d.Store] += n
		}
	}
	return nil
}

// PurgeEphemeral deletes a finished session's conversation tier when its
// tenant policy has zero Conversation retention. The harness calls it at
// Finish; the audit trail is untouched.
func PurgeEphemeral(ctx context.Context, deps RetentionDeps, def RetentionPolicy, tenant, sessionID string) error {
	policy, err := retentionPolicy(ctx, deps, def, tenant)
	if err != nil {
		return err
	}
	if policy.Conversation > 0 {
		return nil
	}
	for _, d := range deps.Session {
		if _, err := d.Purge(ctx, tenant, sessionID); err != nil {
			return fmt.Errorf("gohan: ephemeral purge %s session %s: %w", d.Store, sessionID, err)
		}
	}
	return nil
}
