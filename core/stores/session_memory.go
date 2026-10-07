package stores

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

var (
	ErrSessionNotFound  = errors.New("gohan: session not found")
	ErrMessageNotFound  = errors.New("gohan: fork point message not found")
	ErrSessionLinked    = errors.New("gohan: session already linked to a parent")
	ErrHoldScopeMissing = errors.New("gohan: setting a hold needs the session:hold scope")
)

const (
	scopeSessionRead    = "session:read"
	scopeSessionWrite   = "session:write"
	scopeSessionHold    = "session:hold"
	scopeSessionControl = types.ScopeSessionControl
)

const (
	defaultSessionPage  = 25
	maxSessionPage      = 200
	defaultArchivedHold = 180 * 24 * time.Hour
)

// PrincipalSource reports the transport-verified principal in ctx. The
// harness wiring injects the seam's extractor; the store never reads
// identity context itself.
type PrincipalSource func(ctx context.Context) (types.Principal, bool)

type sessionRecord struct {
	meta        SessionMeta
	hist        History
	rawMessages [][]byte
	kidsOf      map[SessionKind][]string
	linked      bool
	state       sharedStateRow
}

// sharedStateRow is the session's persisted shared state: the JSON value
// and the state's own monotonic version, independent of history versions.
type sharedStateRow struct {
	value   json.RawMessage
	raw     []byte
	version int64
}

// MemorySessionLog is the in-memory SessionLog and SessionIndex reference
// implementation.
type MemorySessionLog struct {
	mu          sync.Mutex
	now         func() time.Time
	principals  PrincipalSource
	dependents  []SessionDependent
	leases      SessionLeaseHolder
	audit       AuditLog
	archivedFor time.Duration
	sessions    map[string]*sessionRecord
	blobIDs     map[string]map[string]blobIDRow
	msgSeq      int64
	idSeq       int64
	schemas     *SchemaRegistry
	raw         *MemoryRawRecords
	region      string
}

type MemorySessionOption func(*MemorySessionLog)

// WithMemorySessionClock replaces the store clock. The index derives
// LastActivity from it, so tests drive time through it.
func WithMemorySessionClock(now func() time.Time) MemorySessionOption {
	return func(s *MemorySessionLog) { s.now = now }
}

func WithMemorySessionRegion(region string) MemorySessionOption {
	return func(s *MemorySessionLog) { s.region = region }
}

func (s *MemorySessionLog) Region() string { return s.region }

// WithSessionPrincipals injects the principal extractor used for the
// ownership checks.
func WithSessionPrincipals(src PrincipalSource) MemorySessionOption {
	return func(s *MemorySessionLog) { s.principals = src }
}

// WithSessionDependents registers stores that follow the cascade and fork
// operations.
func WithSessionDependents(d ...SessionDependent) MemorySessionOption {
	return func(s *MemorySessionLog) { s.dependents = append(s.dependents, d...) }
}

// WithSessionLeases injects the lease holder consulted before a fork or a
// delete.
func WithSessionLeases(l SessionLeaseHolder) MemorySessionOption {
	return func(s *MemorySessionLog) { s.leases = l }
}

// WithArchivedRetention overrides the archived purge threshold.
func WithArchivedRetention(d time.Duration) MemorySessionOption {
	return func(s *MemorySessionLog) { s.archivedFor = d }
}

func WithMemorySessionSchemas(reg *SchemaRegistry) MemorySessionOption {
	return func(s *MemorySessionLog) { s.schemas = reg }
}

func (s *MemorySessionLog) RawRecords() RawRecords {
	return s.raw
}

// SeedRecord installs envelope as the stored bytes of identity, so the next
// public read consumes them as persisted bytes. Seeding bypasses ownership
// checks: fixtures replay historical bytes, not client writes.
func (s *MemorySessionLog) SeedRecord(_ context.Context, identity string, envelope RecordEnvelope) error {
	raw, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("seed %s: %w", identity, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.HasSuffix(identity, "/state") {
		rec, err := s.stateSlot(identity)
		if err != nil {
			return err
		}
		rec.state.raw = raw
		return nil
	}
	rec, index, err := s.messageSlot(identity)
	if err != nil {
		return err
	}
	rec.rawMessages[index] = raw
	return nil
}

// StoredRecord returns the stored bytes of identity unchanged.
func (s *MemorySessionLog) StoredRecord(_ context.Context, identity string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.HasSuffix(identity, "/state") {
		rec, err := s.stateSlot(identity)
		if err != nil {
			return nil, err
		}
		return append([]byte(nil), rec.state.raw...), nil
	}
	rec, index, err := s.messageSlot(identity)
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), rec.rawMessages[index]...), nil
}

// stateSlot resolves a "session/<id>/state" identity to the record holding
// the shared state's stored bytes. Session ids must not carry a slash.
func (s *MemorySessionLog) stateSlot(identity string) (*sessionRecord, error) {
	rest, ok := strings.CutPrefix(identity, "session/")
	if !ok {
		return nil, fmt.Errorf("record identity %s: not a session identity", identity)
	}
	sid, kind, _ := strings.Cut(rest, "/")
	if kind != "state" {
		return nil, fmt.Errorf("record identity %s: unknown kind %q", identity, kind)
	}
	rec, ok := s.sessions[sid]
	if !ok {
		return nil, fmt.Errorf("record identity %s: not found", identity)
	}
	return rec, nil
}

// messageSlot resolves a "session/<id>/message/<i>" identity to the record
// holding that message's stored bytes. Session ids must not carry a slash.
func (s *MemorySessionLog) messageSlot(identity string) (*sessionRecord, int, error) {
	rest, ok := strings.CutPrefix(identity, "session/")
	if !ok {
		return nil, 0, fmt.Errorf("record identity %s: not a session identity", identity)
	}
	sid, rest, _ := strings.Cut(rest, "/")
	kind, index, _ := strings.Cut(rest, "/")
	if kind != "message" {
		return nil, 0, fmt.Errorf("record identity %s: unknown kind %q", identity, kind)
	}
	i, err := strconv.Atoi(index)
	if err != nil || i < 0 {
		return nil, 0, fmt.Errorf("record identity %s: bad index %q", identity, index)
	}
	rec, ok := s.sessions[sid]
	if !ok || i >= len(rec.rawMessages) {
		return nil, 0, fmt.Errorf("record identity %s: not found", identity)
	}
	return rec, i, nil
}

// blobIDRow is one ledger entry: the provider that issued the id for a ref
// and the id itself.
type blobIDRow struct {
	provider string
	id       string
}

func NewMemorySessionLog(opts ...MemorySessionOption) *MemorySessionLog {
	s := &MemorySessionLog{
		now:         time.Now,
		archivedFor: defaultArchivedHold,
		sessions:    make(map[string]*sessionRecord),
		blobIDs:     make(map[string]map[string]blobIDRow),
		schemas:     NewSchemaRegistry(),
		raw:         NewMemoryRawRecords(),
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

func (s *MemorySessionLog) principal(ctx context.Context) (types.Principal, error) {
	if s.principals == nil {
		return types.Principal{}, types.ErrNoPrincipal
	}
	p, ok := s.principals(ctx)
	if !ok {
		return types.Principal{}, types.ErrNoPrincipal
	}
	return p, nil
}

func hasScope(p types.Principal, scope string) bool {
	return slices.Contains(p.Scopes, scope)
}

// checkAccess enforces the ownership rule: the principal's tenant must match
// and it needs either the subject or the given scope.
func (s *MemorySessionLog) checkAccess(p types.Principal, owner types.SessionOwner, scope string) error {
	if p.Tenant != owner.Tenant {
		return fmt.Errorf("tenant %s: %w", p.Tenant, types.ErrSessionForbidden)
	}
	if p.Subject != owner.Subject && !hasScope(p, scope) {
		return fmt.Errorf("subject %s: %w", p.Subject, types.ErrSessionForbidden)
	}
	return nil
}

func (s *MemorySessionLog) get(ctx context.Context, sessionID, scope string) (*sessionRecord, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return nil, err
	}
	rec, ok := s.sessions[sessionID]
	if !ok {
		return nil, fmt.Errorf("session %s: %w", sessionID, ErrSessionNotFound)
	}
	if err := s.checkAccess(p, rec.meta.Owner, scope); err != nil {
		return nil, fmt.Errorf("session %s: %w", sessionID, err)
	}
	return rec, nil
}

func (s *MemorySessionLog) Load(ctx context.Context, sessionID string) (History, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, err := s.get(ctx, sessionID, scopeSessionRead)
	if err != nil {
		return History{}, err
	}
	h := rec.hist
	h.Messages = make([]types.Message, len(rec.rawMessages))
	for i, raw := range rec.rawMessages {
		var env RecordEnvelope
		if err := json.Unmarshal(raw, &env); err != nil {
			return History{}, fmt.Errorf("load message: %w", err)
		}
		msg, err := DecodeRecord[types.Message](ctx, s.schemas, env)
		if err != nil {
			return History{}, err
		}
		h.Messages[i] = msg
	}
	return h, nil
}

func (s *MemorySessionLog) Append(ctx context.Context, sessionID string, expectedVersion int64, msgs ...types.Message) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	rec, ok := s.sessions[sessionID]
	if !ok {
		if expectedVersion != 0 {
			return 0, fmt.Errorf("append session %s: %w", sessionID, types.ErrVersionConflict)
		}
		p, err := s.principal(ctx)
		if err != nil {
			return 0, fmt.Errorf("append session %s: %w", sessionID, err)
		}
		rec = &sessionRecord{
			meta: SessionMeta{
				ID:           sessionID,
				Owner:        types.SessionOwner{Tenant: p.Tenant, Subject: p.Subject},
				Kind:         SessionPrimary,
				CreatedAt:    now,
				LastActivity: now,
			},
		}
		s.sessions[sessionID] = rec
	} else if _, err := s.get(ctx, sessionID, scopeSessionWrite); err != nil {
		return 0, err
	}

	if expectedVersion != rec.hist.Version {
		return 0, fmt.Errorf("append session %s at version %d: %w", sessionID, expectedVersion, types.ErrVersionConflict)
	}
	raws := make([][]byte, len(msgs))
	for i := range msgs {
		s.msgSeq++
		msgs[i].ID = fmt.Sprintf("m%016x", s.msgSeq)
		payload, err := json.Marshal(msgs[i])
		if err != nil {
			return 0, fmt.Errorf("append message: %w", err)
		}
		raws[i], err = json.Marshal(RecordEnvelope{
			Shape:         "Message",
			SchemaVersion: int(CurrentSchemaVersion),
			Payload:       payload,
		})
		if err != nil {
			return 0, fmt.Errorf("append message: %w", err)
		}
	}
	rec.hist.Messages = append(rec.hist.Messages, msgs...)
	rec.rawMessages = append(rec.rawMessages, raws...)
	rec.hist.Version += int64(len(msgs))
	rec.hist.Owner = rec.meta.Owner
	rec.meta.Messages = len(rec.hist.Messages)
	rec.meta.LastActivity = now
	return rec.hist.Version, nil
}

// Link records a child or shadow session of a parent for the delete cascade.
// Forks are never linked: they survive a parent delete.
func (s *MemorySessionLog) Link(ctx context.Context, parentID, childID string, kind SessionKind) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	parent, ok := s.sessions[parentID]
	if !ok {
		return fmt.Errorf("session %s: %w", parentID, ErrSessionNotFound)
	}
	child, ok := s.sessions[childID]
	if !ok {
		return fmt.Errorf("session %s: %w", childID, ErrSessionNotFound)
	}
	if child.linked {
		return fmt.Errorf("session %s: %w", childID, ErrSessionLinked)
	}
	if kind != SessionChild && kind != SessionShadow {
		return fmt.Errorf("session %s: kind %d cannot be linked", childID, kind)
	}
	child.meta.Kind = kind
	child.linked = true
	if parent.kidsOf == nil {
		parent.kidsOf = make(map[SessionKind][]string)
	}
	parent.kidsOf[kind] = append(parent.kidsOf[kind], childID)
	return nil
}

func (s *MemorySessionLog) leaseActive(ctx context.Context, sessionID string) bool {
	return s.leases != nil && s.leases.SessionLeaseActive(ctx, sessionID)
}

func (s *MemorySessionLog) removeLocked(ctx context.Context, sessionID string) {
	rec, ok := s.sessions[sessionID]
	if !ok {
		return
	}
	delete(s.sessions, sessionID)
	delete(s.blobIDs, sessionID)
	for _, kind := range []SessionKind{SessionChild, SessionShadow} {
		for _, kid := range rec.kidsOf[kind] {
			s.removeLocked(ctx, kid)
		}
	}
	for _, d := range s.dependents {
		_ = d.DeleteSessionDependents(ctx, sessionID)
	}
}

func (s *MemorySessionLog) Delete(ctx context.Context, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, err := s.get(ctx, sessionID, scopeSessionWrite)
	if err != nil {
		return err
	}
	if rec.meta.Hold != "" {
		return fmt.Errorf("delete session %s: %w", sessionID, types.ErrSessionHeld)
	}
	if s.leaseActive(ctx, sessionID) {
		return fmt.Errorf("delete session %s: %w", sessionID, types.ErrRunActive)
	}
	s.removeLocked(ctx, sessionID)
	return nil
}

func (s *MemorySessionLog) Purge(ctx context.Context, olderThan time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	archivedCut := s.now().Add(-s.archivedFor)
	var purged []string
	for id, rec := range s.sessions {
		if rec.meta.Pinned {
			continue
		}
		// A legal hold blocks the purge: zero retention does not apply
		// either, so the hold check comes before every age rule.
		if rec.meta.Hold != "" {
			continue
		}
		if rec.meta.Archived {
			if rec.meta.LastActivity.Before(archivedCut) {
				purged = append(purged, id)
			}
			continue
		}
		if rec.meta.LastActivity.Before(olderThan) {
			purged = append(purged, id)
		}
	}
	for _, id := range purged {
		s.removeLocked(ctx, id)
	}
	return len(purged), nil
}

// Fork copies the parent's prefix through upTo into a new session of kind
// SessionFork: same owner, message IDs and origins preserved, no dependents
// inherited beyond what CopySessionDependents carries.
func (s *MemorySessionLog) Fork(ctx context.Context, from, upTo string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, err := s.get(ctx, from, scopeSessionWrite)
	if err != nil {
		return "", err
	}
	if s.leaseActive(ctx, from) {
		return "", fmt.Errorf("fork session %s: %w", from, types.ErrRunActive)
	}
	cut := -1
	for i, m := range rec.hist.Messages {
		if m.ID == upTo {
			cut = i
			break
		}
	}
	if cut < 0 {
		return "", fmt.Errorf("fork session %s: message %s: %w", from, upTo, ErrMessageNotFound)
	}
	s.idSeq++
	childID := fmt.Sprintf("s%016x", s.idSeq)
	now := s.now()
	point := &ForkPoint{SessionID: from, UpTo: upTo}
	child := &sessionRecord{
		meta: SessionMeta{
			ID:           childID,
			Owner:        rec.meta.Owner,
			Title:        rec.meta.Title,
			Flow:         rec.meta.Flow,
			Kind:         SessionFork,
			ForkedFrom:   point,
			Messages:     cut + 1,
			CreatedAt:    now,
			LastActivity: now,
			Control:      rec.meta.Control,
		},
	}
	child.hist = History{
		Owner:      rec.meta.Owner,
		Messages:   slices.Clone(rec.hist.Messages[:cut+1]),
		Version:    int64(cut + 1),
		ForkedFrom: point,
	}
	child.rawMessages = make([][]byte, cut+1)
	for i := range child.rawMessages {
		child.rawMessages[i] = slices.Clone(rec.rawMessages[i])
	}
	// A fork renumbers shared state from 1 so later writes in either
	// session never conflict with the other.
	if rec.state.version > 0 {
		child.state = sharedStateRow{value: slices.Clone(rec.state.value), raw: slices.Clone(rec.state.raw), version: 1}
	}
	s.sessions[childID] = child
	for _, d := range s.dependents {
		if err := d.CopySessionDependents(ctx, from, childID); err != nil {
			delete(s.sessions, childID)
			return "", fmt.Errorf("fork session %s: copy dependents: %w", from, err)
		}
	}
	if rows := s.blobIDs[from]; rows != nil {
		copied := make(map[string]blobIDRow, len(rows))
		for ref, row := range rows {
			copied[ref] = row
		}
		s.blobIDs[childID] = copied
	}
	return childID, nil
}

func (s *MemorySessionLog) Sessions(ctx context.Context, owner types.SessionOwner, q SessionQuery) ([]SessionMeta, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.principal(ctx)
	if err != nil {
		return nil, "", err
	}
	if p.Tenant != owner.Tenant {
		return nil, "", fmt.Errorf("tenant %s: %w", p.Tenant, types.ErrSessionForbidden)
	}
	// A caller without session:read is scoped to its own subject whatever the
	// query asked for.
	subject := owner.Subject
	if p.Subject != subject && !hasScope(p, scopeSessionRead) {
		subject = p.Subject
	}
	kinds := q.Kinds
	if len(kinds) == 0 {
		kinds = []SessionKind{SessionPrimary, SessionFork}
	}
	limit := q.Limit
	if limit <= 0 {
		limit = defaultSessionPage
	}
	if limit > maxSessionPage {
		limit = maxSessionPage
	}

	var afterNano int64
	var afterID string
	var afterTS time.Time
	if q.After != "" {
		if _, err := fmt.Sscanf(q.After, "%d:%s", &afterNano, &afterID); err != nil {
			return nil, "", fmt.Errorf("session page: bad cursor: %w", types.ErrInputInvalid)
		}
		afterTS = time.Unix(0, afterNano)
	}

	var matches []*sessionRecord
	for _, rec := range s.sessions {
		m := &rec.meta
		if m.Owner.Tenant != owner.Tenant || m.Owner.Subject != subject {
			continue
		}
		if q.Archived != nil {
			if m.Archived != *q.Archived {
				continue
			}
		} else if m.Archived {
			continue
		}
		if q.Control != nil && m.Control != *q.Control {
			continue
		}
		if !slices.Contains(kinds, m.Kind) {
			continue
		}
		if afterID != "" && (m.LastActivity.After(afterTS) || (m.LastActivity.Equal(afterTS) && m.ID <= afterID)) {
			continue
		}
		matches = append(matches, rec)
	}
	sort.Slice(matches, func(i, j int) bool {
		a, b := &matches[i].meta, &matches[j].meta
		if !a.LastActivity.Equal(b.LastActivity) {
			return a.LastActivity.After(b.LastActivity)
		}
		return a.ID < b.ID
	})
	if len(matches) > limit {
		matches = matches[:limit]
	}
	out := make([]SessionMeta, len(matches))
	var cursor string
	for i, rec := range matches {
		out[i] = rec.meta
	}
	if len(out) == limit && len(out) > 0 {
		last := &matches[len(matches)-1].meta
		cursor = fmt.Sprintf("%d:%s", last.LastActivity.UnixNano(), last.ID)
	}
	return out, cursor, nil
}

// Control reports the session's control state. The conversation reads it
// at the Send gate; no ownership check applies beyond the session lookup,
// because the state only decides whether the agent or a human is in charge.
func (s *MemorySessionLog) Control(ctx context.Context, sessionID string) (SessionControl, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, err := s.get(ctx, sessionID, scopeSessionRead)
	if err != nil {
		// A session that has no first append yet runs under agent
		// control; Send creates it right after this gate.
		if errors.Is(err, ErrSessionNotFound) {
			return ControlAgent, nil
		}
		return ControlAgent, err
	}
	return rec.meta.Control, nil
}

// SetControl moves the session between agent and human control. The
// takeover and hand-back transitions are the only writers; the caller must
// hold the session's control scope.
func (s *MemorySessionLog) SetControl(ctx context.Context, sessionID string, control SessionControl) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, err := s.get(ctx, sessionID, scopeSessionControl)
	if err != nil {
		return err
	}
	rec.meta.Control = control
	return nil
}

// Owner reports the session owner to a principal authorized to access its metadata.
func (s *MemorySessionLog) Owner(ctx context.Context, sessionID string) (types.SessionOwner, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.principal(ctx)
	if err != nil {
		return types.SessionOwner{}, err
	}
	rec, ok := s.sessions[sessionID]
	if !ok {
		return types.SessionOwner{}, fmt.Errorf("session %s: %w", sessionID, ErrSessionNotFound)
	}
	for _, scope := range []string{scopeSessionRead, scopeSessionWrite, scopeSessionControl} {
		if s.checkAccess(p, rec.meta.Owner, scope) == nil {
			return rec.meta.Owner, nil
		}
	}
	return types.SessionOwner{}, fmt.Errorf("session %s: %w", sessionID, types.ErrSessionForbidden)
}

func (s *MemorySessionLog) UpdateSession(ctx context.Context, sessionID string, p SessionPatch) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, err := s.get(ctx, sessionID, scopeSessionWrite)
	if err != nil {
		return err
	}
	candidate := rec.meta
	if p.Hold != nil {
		pr, err := s.principal(ctx)
		if err != nil {
			return err
		}
		if !hasScope(pr, scopeSessionHold) {
			return fmt.Errorf("update session %s: %w", sessionID, ErrHoldScopeMissing)
		}
		prev := candidate.Hold
		candidate.Hold = *p.Hold
		if err := s.auditHold(ctx, candidate, prev); err != nil {
			return err
		}
	}
	if p.Title != nil {
		candidate.Title = *p.Title
		candidate.TitleLocked = true
	}
	if p.Archived != nil {
		candidate.Archived = *p.Archived
	}
	if p.Pinned != nil {
		candidate.Pinned = *p.Pinned
	}
	rec.meta = candidate
	return nil
}

// SharedStateMeta reads the session's shared state value and version.
func (s *MemorySessionLog) SharedStateMeta(ctx context.Context, sessionID string) (json.RawMessage, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, err := s.get(ctx, sessionID, scopeSessionRead)
	if err != nil {
		return nil, 0, err
	}
	if rec.state.raw == nil {
		return nil, rec.state.version, nil
	}
	var env RecordEnvelope
	if err := json.Unmarshal(rec.state.raw, &env); err != nil {
		return nil, 0, fmt.Errorf("decode State envelope: %w", err)
	}
	value, err := DecodeRecord[json.RawMessage](ctx, s.schemas, env)
	if err != nil {
		return nil, 0, err
	}
	return value, rec.state.version, nil
}

// BlobProviderID reads the ledger row a ref carries in this session.
func (s *MemorySessionLog) BlobProviderID(ctx context.Context, sessionID, ref string) (string, string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.get(ctx, sessionID, scopeSessionRead); err != nil {
		return "", "", false
	}
	row, ok := s.blobIDs[sessionID][ref]
	return row.provider, row.id, ok
}

// SetBlobProviderID writes the ledger row for a ref, naming the provider
// that issued the id.
func (s *MemorySessionLog) SetBlobProviderID(ctx context.Context, sessionID, ref, provider, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.get(ctx, sessionID, scopeSessionWrite); err != nil {
		return err
	}
	rows := s.blobIDs[sessionID]
	if rows == nil {
		rows = make(map[string]blobIDRow)
		s.blobIDs[sessionID] = rows
	}
	rows[ref] = blobIDRow{provider, id}
	return nil
}

// SetSharedStateMeta writes the shared state under an expected-version
// check and returns the advanced version.
func (s *MemorySessionLog) SetSharedStateMeta(ctx context.Context, sessionID string, expectedVersion int64, value json.RawMessage) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, err := s.get(ctx, sessionID, scopeSessionWrite)
	if err != nil {
		return 0, err
	}
	if rec.state.version != expectedVersion {
		return 0, fmt.Errorf("shared state session %s at version %d: %w", sessionID, expectedVersion, types.ErrVersionConflict)
	}
	raw, err := encodeEnvelope("State", value)
	if err != nil {
		return 0, err
	}
	rec.state.value = slices.Clone(value)
	rec.state.raw = raw
	rec.state.version = expectedVersion + 1
	return rec.state.version, nil
}
