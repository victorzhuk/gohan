package postgres

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

const (
	scopeSessionRead  = "session:read"
	scopeSessionWrite = "session:write"

	defaultSessionPage = 25
	maxSessionPage     = 200
	archivedRetention  = 180 * 24 * time.Hour
)

// SessionLog persists message history per session on Postgres and carries
// the optional SessionIndex listing surface.
type SessionLog struct {
	db         *DB
	reg        *stores.SchemaRegistry
	principals stores.PrincipalSource
	dependents []stores.SessionDependent
	leases     stores.SessionLeaseHolder
}

// SessionLogOption configures NewSessionLog.
type SessionLogOption func(*SessionLog)

// WithSessionPrincipals injects the principal extractor the ownership
// checks use; the harness wiring supplies the seam's extractor.
func WithSessionPrincipals(src stores.PrincipalSource) SessionLogOption {
	return func(s *SessionLog) { s.principals = src }
}

// WithSessionDependents registers stores that follow the delete cascade.
func WithSessionDependents(d ...stores.SessionDependent) SessionLogOption {
	return func(s *SessionLog) { s.dependents = append(s.dependents, d...) }
}

// WithSessionLeases injects the lease holder consulted before a delete.
func WithSessionLeases(l stores.SessionLeaseHolder) SessionLogOption {
	return func(s *SessionLog) { s.leases = l }
}

// NewSessionLog builds the session store on db.
func NewSessionLog(db *DB, opts ...SessionLogOption) *SessionLog {
	s := &SessionLog{db: db, reg: stores.NewSchemaRegistry()}
	for _, o := range opts {
		o(s)
	}
	return s
}

type sessionMetaRecord struct {
	Title        string                `json:"title,omitempty"`
	TitleLocked  bool                  `json:"title_locked,omitempty"`
	Pinned       bool                  `json:"pinned,omitempty"`
	Flow         string                `json:"flow,omitempty"`
	Control      stores.SessionControl `json:"control,omitempty"`
	Hold         string                `json:"hold,omitempty"`
	Handoff      *types.HandoffContext `json:"handoff,omitempty"`
	CreatedAt    time.Time             `json:"created_at"`
	LastActivity time.Time             `json:"last_activity"`
	Messages     int                   `json:"messages"`
}

type sessionRow struct {
	id       string
	owner    types.SessionOwner
	kind     stores.SessionKind
	archived bool
	version  int64
	forked   *stores.ForkPoint
	history  json.RawMessage
	payload  []byte
}

func scanSessionRow(row pgx.Row) (*sessionRow, error) {
	var r sessionRow
	var owner []byte
	err := row.Scan(&r.id, &owner, &r.kind, &r.archived, &r.version, &r.forked, &r.history, &r.payload)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(owner, &r.owner); err != nil {
		return nil, fmt.Errorf("decode session owner: %w", err)
	}
	return &r, nil
}

const sessionColumns = `session_id, owner, kind, archived, version, forked_from, history, payload`

func (s *SessionLog) principal(ctx context.Context) (types.Principal, error) {
	if s.principals == nil {
		return types.Principal{}, errors.New("gohan: no principal source wired")
	}
	p, ok := s.principals(ctx)
	if !ok {
		return types.Principal{}, errors.New("gohan: no principal in context")
	}
	return p, nil
}

func checkSessionAccess(p types.Principal, owner types.SessionOwner, scope string) error {
	if p.Tenant != owner.Tenant {
		return fmt.Errorf("tenant %s: %w", p.Tenant, types.ErrSessionForbidden)
	}
	if p.Subject != owner.Subject && !slices.Contains(p.Scopes, scope) {
		return fmt.Errorf("subject %s: %w", p.Subject, types.ErrSessionForbidden)
	}
	return nil
}

func (s *SessionLog) getSession(ctx context.Context, q executor, sessionID, scope string) (*sessionRow, types.Principal, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return nil, types.Principal{}, err
	}
	r, err := scanSessionRow(q.QueryRow(ctx, `SELECT `+sessionColumns+` FROM session_record WHERE session_id = $1 FOR UPDATE`, sessionID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, p, fmt.Errorf("session %s: %w", sessionID, stores.ErrSessionNotFound)
	}
	if err != nil {
		return nil, types.Principal{}, fmt.Errorf("load session %s: %w", sessionID, err)
	}
	if err := checkSessionAccess(p, r.owner, scope); err != nil {
		return nil, types.Principal{}, fmt.Errorf("session %s: %w", sessionID, err)
	}
	return r, p, nil
}

func (s *SessionLog) meta(ctx context.Context, r *sessionRow) (sessionMetaRecord, error) {
	env, err := envelope(r.payload)
	if err != nil {
		return sessionMetaRecord{}, err
	}
	var meta sessionMetaRecord
	if err := json.Unmarshal(env.Payload, &meta); err != nil {
		return sessionMetaRecord{}, fmt.Errorf("decode session meta: %w", err)
	}
	return meta, nil
}

func messageID() (string, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("mint message id: %w", err)
	}
	return "m" + hex.EncodeToString(raw[:]), nil
}

// Load replays the recorded history, decoding every message envelope
// through the schema registry.
func (s *SessionLog) Load(ctx context.Context, sessionID string) (stores.History, error) {
	r, _, err := s.getSession(ctx, poolConn{s.db.pool}, sessionID, scopeSessionRead)
	if err != nil {
		return stores.History{}, err
	}
	var envelopes []stores.RecordEnvelope
	if err := json.Unmarshal(r.history, &envelopes); err != nil {
		return stores.History{}, fmt.Errorf("decode session %s history: %w", sessionID, err)
	}
	msgs := make([]types.Message, len(envelopes))
	for i, env := range envelopes {
		msg, err := stores.DecodeRecord[types.Message](ctx, s.reg, env)
		if err != nil {
			return stores.History{}, err
		}
		msgs[i] = msg
	}
	return stores.History{Owner: r.owner, Version: r.version, ForkedFrom: r.forked, Messages: msgs}, nil
}

// Append assigns message ids, wraps every message in a record envelope and
// advances the version only when the caller held the expected one.
func (s *SessionLog) Append(ctx context.Context, sessionID string, expectedVersion int64, msgs ...types.Message) (int64, error) {
	tx, err := s.db.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("append session %s: %w", sessionID, err)
	}
	defer tx.Rollback(ctx)

	now := s.db.clock()
	version, history, meta, owner, err := s.appendLocked(ctx, tx, sessionID, expectedVersion, msgs, now)
	if err != nil {
		return 0, err
	}
	payload, err := encodeRecord("SessionMeta", meta)
	if err != nil {
		return 0, fmt.Errorf("append session %s: %w", sessionID, err)
	}
	var ownerRaw []byte
	if owner != nil {
		if ownerRaw, err = json.Marshal(*owner); err != nil {
			return 0, fmt.Errorf("append session %s: %w", sessionID, err)
		}
	}
	if owner == nil {
		_, err = tx.Exec(ctx,
			`UPDATE session_record SET version = $1, history = $2, payload = $3, updated_at = $4 WHERE session_id = $5`,
			version, history, payload, now, sessionID)
	} else {
		_, err = tx.Exec(ctx,
			`INSERT INTO session_record (session_id, owner, kind, archived, version, history, payload, updated_at)
			 VALUES ($1, $2, 0, false, $3, $4, $5, $6)`,
			sessionID, ownerRaw, version, history, payload, now)
	}
	if err != nil {
		return 0, fmt.Errorf("append session %s: %w", sessionID, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("append session %s: %w", sessionID, err)
	}
	return version, nil
}

// compactionPublicationPayload validates the durable payload, the message
// identity and the summary checksum of an incoming publication. It mirrors
// the memory store's validator so both stores accept and refuse the same
// requests. It runs no store lookup.
func compactionPublicationPayload(message types.Message, sessionID string) (stores.CompactionPublication, error) {
	raw, ok := message.Meta[stores.MetadataPayloadKey]
	if !ok {
		return stores.CompactionPublication{}, fmt.Errorf("metadata %s: %w", stores.MetadataPayloadKey, errCompactionPayload)
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return stores.CompactionPublication{}, fmt.Errorf("metadata %s: %w", stores.MetadataPayloadKey, errCompactionPayload)
	}
	var payload stores.CompactionPublication
	if err := json.Unmarshal(encoded, &payload); err != nil {
		return stores.CompactionPublication{}, fmt.Errorf("metadata %s: %w", stores.MetadataPayloadKey, errCompactionPayload)
	}
	if payload.SessionID != sessionID {
		return stores.CompactionPublication{}, fmt.Errorf("payload session %s: %w", payload.SessionID, errCompactionPayload)
	}
	if payload.RunID == "" || payload.FromVersion < 0 || payload.ToVersion != payload.FromVersion+1 {
		return stores.CompactionPublication{}, fmt.Errorf("payload versions %d..%d: %w", payload.FromVersion, payload.ToVersion, errCompactionPayload)
	}
	identity := stores.CompactionPublicationID(sessionID, payload.RunID, payload.FromVersion)
	if payload.PublicationID != "" && payload.PublicationID != identity {
		return stores.CompactionPublication{}, fmt.Errorf("payload publication %s: %w", payload.PublicationID, errCompactionPayload)
	}
	if message.ID != "" && message.ID != identity {
		return stores.CompactionPublication{}, fmt.Errorf("message id %s: %w", message.ID, errCompactionPayload)
	}
	summary, err := compactionMessageSummary(message)
	if err != nil {
		return stores.CompactionPublication{}, err
	}
	checksum, err := stores.CompactionSummarySHA(summary)
	if err != nil {
		return stores.CompactionPublication{}, fmt.Errorf("summary checksum: %w", errCompactionPayload)
	}
	if payload.SummarySHA != checksum {
		return stores.CompactionPublication{}, fmt.Errorf("summary sha %s: %w", payload.SummarySHA, errCompactionPayload)
	}
	payload.PublicationID = identity
	return payload, nil
}

// compactionMessageSummary returns the summary of the message's single
// compaction block.
func compactionMessageSummary(message types.Message) ([]types.Block, error) {
	var summary []types.Block
	found := false
	for _, b := range message.Blocks {
		cb, ok := b.(types.Compaction)
		if !ok {
			continue
		}
		if found {
			return nil, fmt.Errorf("message blocks: multiple compaction blocks: %w", errCompactionPayload)
		}
		summary = cb.Summary
		found = true
	}
	if !found {
		return nil, fmt.Errorf("message blocks: no compaction block: %w", errCompactionPayload)
	}
	return summary, nil
}

// compactionPublicationVersion resolves payload's identity against the
// session's stored history inside the caller's transaction. An existing
// identical identity reports the version its original append committed; a
// conflicting payload is refused without mutation.
func compactionPublicationVersion(ctx context.Context, tx pgx.Tx, history json.RawMessage, payload stores.CompactionPublication) (int64, bool, error) {
	var envelopes []stores.RecordEnvelope
	if err := json.Unmarshal(history, &envelopes); err != nil {
		return 0, false, fmt.Errorf("decode history: %w", err)
	}
	for _, env := range envelopes {
		var msg types.Message
		if err := json.Unmarshal(env.Payload, &msg); err != nil {
			return 0, false, fmt.Errorf("decode message: %w", err)
		}
		raw, ok := msg.Meta[stores.MetadataPayloadKey]
		if !ok {
			continue
		}
		encoded, err := json.Marshal(raw)
		if err != nil {
			return 0, false, fmt.Errorf("decode publication: %w", err)
		}
		var stored stores.CompactionPublication
		if err := json.Unmarshal(encoded, &stored); err != nil {
			return 0, false, fmt.Errorf("decode publication: %w", err)
		}
		if stored.PublicationID != payload.PublicationID {
			continue
		}
		if stored != payload {
			return 0, false, fmt.Errorf("compaction publication %s: %w", payload.PublicationID, errCompactionConflict)
		}
		// ToVersion is FromVersion + 1, the append version the original
		// insert committed.
		return stored.ToVersion, true, nil
	}
	return 0, false, nil
}

// AppendCompaction accepts a compaction publication as exactly one durable
// message with a predetermined identity, under the session transaction.
// Validation and authorization run before the identity lookup, so a refused
// request never exposes a stored publication. An identical retry returns the
// originally committed append version even when expectedVersion is stale; a
// conflicting payload is refused without mutation; an absent identity with a
// stale expectedVersion is refused with types.ErrVersionConflict.
func (s *SessionLog) AppendCompaction(ctx context.Context, sessionID string, expectedVersion int64, message types.Message) (int64, error) {
	payload, err := compactionPublicationPayload(message, sessionID)
	if err != nil {
		return 0, fmt.Errorf("compaction session %s: %w", sessionID, err)
	}
	tx, err := s.db.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("compaction session %s: %w", sessionID, err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	r, _, err := s.getSession(ctx, txConn{tx}, sessionID, scopeSessionWrite)
	if err != nil {
		return 0, fmt.Errorf("compaction session %s: %w", sessionID, err)
	}
	if committed, ok, err := compactionPublicationVersion(ctx, tx, r.history, payload); ok || err != nil {
		return committed, err
	}
	if expectedVersion != r.version {
		return 0, fmt.Errorf("compaction session %s at version %d: %w", sessionID, expectedVersion, types.ErrVersionConflict)
	}
	stored := message
	stored.ID = payload.PublicationID
	raw, err := json.Marshal(stored)
	if err != nil {
		return 0, fmt.Errorf("compaction message: %w", err)
	}
	// The stored history is a JSON array of envelopes, so the publication is
	// appended as a one-element array, not a bare object.
	envelope, err := json.Marshal([]stores.RecordEnvelope{{
		Shape:         "Message",
		SchemaVersion: int(stores.CurrentSchemaVersion),
		Payload:       raw,
	}})
	if err != nil {
		return 0, fmt.Errorf("compaction message: %w", err)
	}
	history, err := concatHistory(r.history, envelope)
	if err != nil {
		return 0, fmt.Errorf("compaction session %s: %w", sessionID, err)
	}
	meta, err := s.meta(ctx, r)
	if err != nil {
		return 0, fmt.Errorf("compaction session %s: %w", sessionID, err)
	}
	now := s.db.clock()
	meta.LastActivity = now
	meta.Messages++
	version := r.version + 1
	payloadBytes, err := encodeRecord("SessionMeta", meta)
	if err != nil {
		return 0, fmt.Errorf("compaction session %s: %w", sessionID, err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE session_record SET version = $1, history = $2, payload = $3, updated_at = $4 WHERE session_id = $5`,
		version, history, payloadBytes, now, sessionID); err != nil {
		return 0, fmt.Errorf("compaction session %s: %w", sessionID, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("compaction session %s: %w", sessionID, err)
	}
	return version, nil
}

func (s *SessionLog) appendLocked(ctx context.Context, tx pgx.Tx, sessionID string, expectedVersion int64, msgs []types.Message, now time.Time) (int64, json.RawMessage, sessionMetaRecord, *types.SessionOwner, error) {
	r, p, err := s.getSession(ctx, txConn{tx}, sessionID, scopeSessionWrite)
	switch {
	case errors.Is(err, stores.ErrSessionNotFound):
		if expectedVersion != 0 {
			return 0, nil, sessionMetaRecord{}, nil, fmt.Errorf("append session %s: %w", sessionID, types.ErrVersionConflict)
		}
		meta := sessionMetaRecord{CreatedAt: now, LastActivity: now, Messages: len(msgs)}
		history, err := s.envelopes(ctx, msgs)
		if err != nil {
			return 0, nil, sessionMetaRecord{}, nil, err
		}
		owner := types.SessionOwner{Tenant: p.Tenant, Subject: p.Subject}
		return int64(len(msgs)), history, meta, &owner, nil
	case err != nil:
		return 0, nil, sessionMetaRecord{}, nil, err
	}
	if expectedVersion != r.version {
		return 0, nil, sessionMetaRecord{}, nil, fmt.Errorf("append session %s at version %d: %w", sessionID, expectedVersion, types.ErrVersionConflict)
	}
	meta, err := s.meta(ctx, r)
	if err != nil {
		return 0, nil, sessionMetaRecord{}, nil, err
	}
	fresh, err := s.envelopes(ctx, msgs)
	if err != nil {
		return 0, nil, sessionMetaRecord{}, nil, err
	}
	history, err := concatHistory(r.history, fresh)
	if err != nil {
		return 0, nil, sessionMetaRecord{}, nil, err
	}
	meta.LastActivity = now
	meta.Messages += len(msgs)
	return r.version + int64(len(msgs)), history, meta, nil, nil
}

func (s *SessionLog) envelopes(ctx context.Context, msgs []types.Message) (json.RawMessage, error) {
	out := make([]stores.RecordEnvelope, len(msgs))
	for i := range msgs {
		id, err := messageID()
		if err != nil {
			return nil, err
		}
		msgs[i].ID = id
		raw, err := json.Marshal(msgs[i])
		if err != nil {
			return nil, fmt.Errorf("append message: %w", err)
		}
		out[i] = stores.RecordEnvelope{Shape: "Message", SchemaVersion: int(stores.CurrentSchemaVersion), Payload: raw}
	}
	history, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("encode history: %w", err)
	}
	return history, nil
}

func concatHistory(existing json.RawMessage, fresh json.RawMessage) (json.RawMessage, error) {
	if string(existing) == "null" || len(existing) == 0 {
		return fresh, nil
	}
	var all []stores.RecordEnvelope
	if err := json.Unmarshal(existing, &all); err != nil {
		return nil, fmt.Errorf("decode history: %w", err)
	}
	var add []stores.RecordEnvelope
	if err := json.Unmarshal(fresh, &add); err != nil {
		return nil, fmt.Errorf("decode history: %w", err)
	}
	out, err := json.Marshal(append(all, add...))
	if err != nil {
		return nil, fmt.Errorf("encode history: %w", err)
	}
	return out, nil
}

// Delete removes the session, its blob ledger rows and the dependents,
// refusing a held session or a live run lease first.
func (s *SessionLog) Delete(ctx context.Context, sessionID string) error {
	tx, err := s.db.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("delete session %s: %w", sessionID, err)
	}
	defer tx.Rollback(ctx)

	r, _, err := s.getSession(ctx, txConn{tx}, sessionID, scopeSessionWrite)
	if err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	meta, err := s.meta(ctx, r)
	if err != nil {
		return fmt.Errorf("delete session %s: %w", sessionID, err)
	}
	if meta.Hold != "" {
		return fmt.Errorf("delete session %s: %w", sessionID, types.ErrSessionHeld)
	}
	if s.leases != nil && s.leases.SessionLeaseActive(ctx, sessionID) {
		return fmt.Errorf("delete session %s: %w", sessionID, types.ErrRunActive)
	}
	if err := s.removeSession(ctx, tx, sessionID); err != nil {
		return fmt.Errorf("delete session %s: %w", sessionID, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("delete session %s: %w", sessionID, err)
	}
	return nil
}

// sessionDependentTx is the package-local seam every concrete dependent in
// this package supplies: the cascade deletes its rows inside the session
// delete's own transaction. A third-party stores.SessionDependent without
// the transaction entry falls back to its pool method and stays outside the
// bounded atomicity guarantee.
type sessionDependentTx interface {
	deleteSessionDependentsTx(ctx context.Context, tx pgx.Tx, sessionID string) error
}

func (s *SessionLog) removeSession(ctx context.Context, tx pgx.Tx, sessionID string) error {
	rows, err := tx.Query(ctx,
		`WITH RECURSIVE tree AS (
			SELECT session_id FROM session_record WHERE session_id = $1
			UNION
			SELECT r.session_id FROM session_record r
			JOIN tree t ON r.forked_from->>'SessionID' = t.session_id
			WHERE r.kind IN (2, 3)
		)
		DELETE FROM session_record WHERE session_id IN (SELECT session_id FROM tree) RETURNING session_id`,
		sessionID)
	if err != nil {
		return err
	}
	defer rows.Close()
	var removed []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		removed = append(removed, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range removed {
		if _, err := tx.Exec(ctx, `DELETE FROM session_blob WHERE session_id = $1`, id); err != nil {
			return err
		}
		// Publication tombstones are owned by their publishing session, so
		// they go with it regardless of any run_record discovery.
		if _, err := tx.Exec(ctx, `DELETE FROM event_compaction_publication WHERE session_id = $1`, id); err != nil {
			return err
		}
		for _, d := range s.dependents {
			if dt, ok := d.(sessionDependentTx); ok {
				if err := dt.deleteSessionDependentsTx(ctx, tx, id); err != nil {
					return err
				}
				continue
			}
			if err := d.DeleteSessionDependents(ctx, id); err != nil {
				return err
			}
		}
	}
	return nil
}

// Purge removes unpinned sessions past their age rule; a legal hold blocks
// the purge and archived sessions follow the archive retention first.
func (s *SessionLog) Purge(ctx context.Context, olderThan time.Time) (int, error) {
	tx, err := s.db.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("purge sessions: %w", err)
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `SELECT `+sessionColumns+` FROM session_record`)
	if err != nil {
		return 0, fmt.Errorf("purge sessions: %w", err)
	}
	var candidates []string
	for rows.Next() {
		r, err := scanSessionRow(rows)
		if err != nil {
			rows.Close()
			return 0, fmt.Errorf("purge sessions: %w", err)
		}
		meta, err := s.meta(ctx, r)
		if err != nil {
			rows.Close()
			return 0, fmt.Errorf("purge sessions: %w", err)
		}
		if meta.Pinned || meta.Hold != "" {
			continue
		}
		if r.archived {
			if meta.LastActivity.Before(s.db.clock().Add(-archivedRetention)) {
				candidates = append(candidates, r.id)
			}
			continue
		}
		if meta.LastActivity.Before(olderThan) {
			candidates = append(candidates, r.id)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("purge sessions: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM session_cursor WHERE expires_at <= $1`, s.db.clock()); err != nil {
		return 0, fmt.Errorf("purge sessions: %w", err)
	}
	for _, id := range candidates {
		if err := s.removeSession(ctx, tx, id); err != nil {
			return 0, fmt.Errorf("purge session %s: %w", id, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("purge sessions: %w", err)
	}
	return len(candidates), nil
}

// errInvalidSessionCursor refuses a malformed, missing, expired or
// filter-mismatched continuation cursor. Its message is part of the
// adapter's cursor contract.
var errInvalidSessionCursor = errors.New("list sessions: invalid cursor")

// sessionCursorFilters is the filter fingerprint stored on a session_cursor
// header; a continuation carrying different filters is refused.
type sessionCursorFilters struct {
	Archived *bool                  `json:"archived,omitempty"`
	Control  *stores.SessionControl `json:"control,omitempty"`
	Kinds    []stores.SessionKind   `json:"kinds,omitempty"`
}

// Sessions lists a page of the session index for an owner.
//
// The listing is a materialized snapshot: the first page freezes the
// complete matching membership, ordered by LastActivity descending then
// session_id descending, into a session_cursor in SQL. Continuations walk
// the frozen ordinal range and resolve every surviving member against its
// live row, so returned metadata is current while membership and order stay
// frozen; deleted members leave gaps the scan continues past.
//
// The cursor is opaque, owner-scoped and expires a fixed stores.LeaseTTL
// after materialization; reading a page never extends it. A malformed,
// missing, expired or filter-mismatched cursor is refused with an error
// whose message begins "list sessions: invalid cursor"; a cursor another
// owner submits is refused with types.ErrSessionForbidden. Expired cursors
// are pruned before either a first page or a continuation is processed;
// Purge prunes them inside its own transaction. Limit defaults to 25 and is
// capped at 200.
func (s *SessionLog) Sessions(ctx context.Context, owner types.SessionOwner, q stores.SessionQuery) ([]stores.SessionMeta, string, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = defaultSessionPage
	}
	if limit > maxSessionPage {
		limit = maxSessionPage
	}
	archived := false
	if q.Archived != nil {
		archived = *q.Archived
	}
	kinds := q.Kinds
	if len(kinds) == 0 {
		kinds = []stores.SessionKind{stores.SessionPrimary, stores.SessionFork}
	}
	filters, err := json.Marshal(sessionCursorFilters{Archived: &archived, Control: q.Control, Kinds: kinds})
	if err != nil {
		return nil, "", fmt.Errorf("list sessions: %w", err)
	}
	if _, err := s.db.pool.Exec(ctx, `DELETE FROM session_cursor WHERE expires_at <= $1`, s.db.clock()); err != nil {
		return nil, "", fmt.Errorf("list sessions: %w", err)
	}
	if q.After == "" {
		cursorID, err := newSessionCursorID()
		if err != nil {
			return nil, "", fmt.Errorf("list sessions: %w", err)
		}
		if err := s.materializeSessionCursor(ctx, cursorID, owner, string(filters), q); err != nil {
			return nil, "", err
		}
		return s.readSessionCursorPage(ctx, cursorID, owner, string(filters), 0, int64(limit))
	}
	cursorID, last, err := decodeSessionCursor(q.After)
	if err != nil {
		return nil, "", err
	}
	return s.readSessionCursorPage(ctx, cursorID, owner, string(filters), last, int64(limit))
}

// materializeSessionCursor inserts the cursor header and the complete
// ordered membership in one transaction. Membership, filters and order are
// selected entirely in SQL: the application never loads the id list. The
// default listing covers primaries and forks only, excludes archived
// sessions and narrows to the filters the caller set, matching the memory
// index.
func (s *SessionLog) materializeSessionCursor(ctx context.Context, cursorID string, owner types.SessionOwner, filters string, q stores.SessionQuery) error {
	archived := false
	if q.Archived != nil {
		archived = *q.Archived
	}
	kinds := q.Kinds
	if len(kinds) == 0 {
		kinds = []stores.SessionKind{stores.SessionPrimary, stores.SessionFork}
	}
	where := `owner->>'Tenant' = $2 AND owner->>'Subject' = $3 AND archived = $4 AND kind = ANY($5)`
	args := []any{owner.Tenant, owner.Subject, archived, kinds}
	addArg := func(v any) string {
		args = append(args, v)
		// The cursor id is prepended as $1 at the insert.
		return fmt.Sprintf("$%d", len(args)+1)
	}
	if q.Control != nil {
		where += ` AND convert_from(payload, 'utf8')::jsonb->'payload'->>'control' = ` + addArg(strconv.Itoa(int(*q.Control)))
	}
	now := s.db.clock()
	tx, err := s.db.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("list sessions: %w", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx,
		`INSERT INTO session_cursor (cursor_id, tenant, subject, filters, created_at, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		cursorID, owner.Tenant, owner.Subject, filters, now, now.Add(stores.LeaseTTL)); err != nil {
		return fmt.Errorf("list sessions: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO session_cursor_item (cursor_id, ordinal, session_id)
		 SELECT $1, row_number() OVER (ORDER BY act DESC, session_id DESC), session_id
		 FROM (
		   SELECT session_id,
		          COALESCE((convert_from(payload, 'utf8')::jsonb->'payload'->>'last_activity')::timestamptz, '-infinity'::timestamptz) AS act
		   FROM session_record WHERE `+where+`
		 ) m`, append([]any{cursorID}, args...)...); err != nil {
		return fmt.Errorf("list sessions: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("list sessions: %w", err)
	}
	return nil
}

// readSessionCursorPage reads one ordinal range of the frozen membership,
// refuses a foreign, expired or filter-mismatched cursor, and resolves the
// surviving members against their live rows so metadata is current.
func (s *SessionLog) readSessionCursorPage(ctx context.Context, cursorID string, owner types.SessionOwner, filters string, after, limit int64) ([]stores.SessionMeta, string, error) {
	var tenant, subject, stored string
	var expires time.Time
	err := s.db.pool.QueryRow(ctx,
		`SELECT tenant, subject, filters, expires_at FROM session_cursor WHERE cursor_id = $1`, cursorID).
		Scan(&tenant, &subject, &stored, &expires)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", fmt.Errorf("%w: cursor not found", errInvalidSessionCursor)
	}
	if err != nil {
		return nil, "", fmt.Errorf("list sessions: %w", err)
	}
	if tenant != owner.Tenant || subject != owner.Subject {
		return nil, "", fmt.Errorf("list sessions: %w", types.ErrSessionForbidden)
	}
	if stored != filters {
		return nil, "", fmt.Errorf("%w: filters changed", errInvalidSessionCursor)
	}
	if !s.db.clock().Before(expires) {
		return nil, "", fmt.Errorf("%w: cursor expired", errInvalidSessionCursor)
	}
	rows, err := s.db.pool.Query(ctx,
		`SELECT ordinal, session_id FROM session_cursor_item
		 WHERE cursor_id = $1 AND ordinal > $2 ORDER BY ordinal LIMIT $3`,
		cursorID, after, limit+1)
	if err != nil {
		return nil, "", fmt.Errorf("list sessions: %w", err)
	}
	type item struct {
		ordinal int64
		id      string
	}
	var items []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.ordinal, &it.id); err != nil {
			rows.Close()
			return nil, "", fmt.Errorf("list sessions: %w", err)
		}
		items = append(items, it)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("list sessions: %w", err)
	}
	// One row beyond the page is read only to learn whether the frozen
	// membership holds another member past this page. A full page whose
	// look-ahead is empty is the terminal page and yields no cursor.
	more := len(items) > int(limit)
	if more {
		items = items[:limit]
	}
	if len(items) == 0 {
		return nil, "", nil
	}
	ids := make([]string, len(items))
	for i, it := range items {
		ids[i] = it.id
	}
	live := make(map[string]*sessionRow, len(ids))
	rrows, err := s.db.pool.Query(ctx,
		`SELECT `+sessionColumns+` FROM session_record WHERE session_id = ANY($1)`, ids)
	if err != nil {
		return nil, "", fmt.Errorf("list sessions: %w", err)
	}
	defer rrows.Close()
	for rrows.Next() {
		r, err := scanSessionRow(rrows)
		if err != nil {
			return nil, "", fmt.Errorf("list sessions: %w", err)
		}
		live[r.id] = r
	}
	if err := rrows.Err(); err != nil {
		return nil, "", fmt.Errorf("list sessions: %w", err)
	}
	out := make([]stores.SessionMeta, 0, len(items))
	for _, it := range items {
		r, ok := live[it.id]
		if !ok {
			continue
		}
		meta, err := s.meta(ctx, r)
		if err != nil {
			return nil, "", err
		}
		out = append(out, stores.SessionMeta{
			ID: r.id, Owner: r.owner, Title: meta.Title, TitleLocked: meta.TitleLocked,
			Archived: r.archived, Pinned: meta.Pinned, Flow: meta.Flow, Kind: r.kind,
			ForkedFrom: r.forked, Messages: meta.Messages,
			CreatedAt: meta.CreatedAt, LastActivity: meta.LastActivity,
			Control: meta.Control, Hold: meta.Hold, Handoff: derefHandoff(meta.Handoff),
		})
	}
	cursor := ""
	if more {
		cursor = encodeSessionCursor(cursorID, items[len(items)-1].ordinal)
	}
	return out, cursor, nil
}

// encodeSessionCursor makes the opaque continuation token from the cursor
// id and the last consumed ordinal.
func encodeSessionCursor(cursorID string, ordinal int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("%s:%d", cursorID, ordinal)))
}

func decodeSessionCursor(cursor string) (string, int64, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return "", 0, errInvalidSessionCursor
	}
	id, ord, ok := strings.Cut(string(raw), ":")
	if !ok {
		return "", 0, errInvalidSessionCursor
	}
	ordinal, err := strconv.ParseInt(ord, 10, 64)
	if err != nil || ordinal < 0 {
		return "", 0, errInvalidSessionCursor
	}
	return id, ordinal, nil
}

func newSessionCursorID() (string, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("mint session cursor id: %w", err)
	}
	return "c" + hex.EncodeToString(raw[:]), nil
}

func derefHandoff(h *types.HandoffContext) types.HandoffContext {
	if h == nil {
		return types.HandoffContext{}
	}
	return *h
}

// UpdateSession applies the patch fields the caller set.
func (s *SessionLog) UpdateSession(ctx context.Context, sessionID string, p stores.SessionPatch) error {
	tx, err := s.db.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("update session %s: %w", sessionID, err)
	}
	defer tx.Rollback(ctx)

	r, _, err := s.getSession(ctx, txConn{tx}, sessionID, scopeSessionWrite)
	if err != nil {
		return fmt.Errorf("update session %s: %w", sessionID, err)
	}
	meta, err := s.meta(ctx, r)
	if err != nil {
		return fmt.Errorf("update session %s: %w", sessionID, err)
	}
	if p.Title != nil {
		meta.Title = *p.Title
		meta.TitleLocked = true
	}
	if p.Archived != nil {
		r.archived = *p.Archived
	}
	if p.Pinned != nil {
		meta.Pinned = *p.Pinned
	}
	if p.Hold != nil {
		meta.Hold = *p.Hold
	}
	if p.Handoff != nil {
		meta.Handoff = p.Handoff
	}
	payload, err := encodeRecord("SessionMeta", meta)
	if err != nil {
		return fmt.Errorf("update session %s: %w", sessionID, err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE session_record SET archived = $1, payload = $2, updated_at = $3 WHERE session_id = $4`,
		r.archived, payload, s.db.clock(), sessionID); err != nil {
		return fmt.Errorf("update session %s: %w", sessionID, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("update session %s: %w", sessionID, err)
	}
	return nil
}
