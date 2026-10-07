package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
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
		for _, d := range s.dependents {
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

// Sessions lists a page of the session index for an owner.
func (s *SessionLog) Sessions(ctx context.Context, owner types.SessionOwner, q stores.SessionQuery) ([]stores.SessionMeta, string, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = defaultSessionPage
	}
	if limit > maxSessionPage {
		limit = maxSessionPage
	}
	rows, err := s.db.pool.Query(ctx,
		`SELECT `+sessionColumns+` FROM session_record
		 WHERE owner->>'Tenant' = $1 AND owner->>'Subject' = $2
		   AND ($3::text = '' OR session_id > $3)
		 ORDER BY session_id LIMIT $4`,
		owner.Tenant, owner.Subject, q.After, limit+1)
	if err != nil {
		return nil, "", fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()

	var out []stores.SessionMeta
	for rows.Next() {
		r, err := scanSessionRow(rows)
		if err != nil {
			return nil, "", fmt.Errorf("list sessions: %w", err)
		}
		meta, err := s.meta(ctx, r)
		if err != nil {
			return nil, "", err
		}
		if q.Archived != nil && r.archived != *q.Archived {
			continue
		}
		if len(q.Kinds) > 0 && !slices.Contains(q.Kinds, r.kind) {
			continue
		}
		out = append(out, stores.SessionMeta{
			ID: r.id, Owner: r.owner, Title: meta.Title, TitleLocked: meta.TitleLocked,
			Archived: r.archived, Pinned: meta.Pinned, Flow: meta.Flow, Kind: r.kind,
			ForkedFrom: r.forked, Messages: meta.Messages,
			CreatedAt: meta.CreatedAt, LastActivity: meta.LastActivity,
			Control: meta.Control, Hold: meta.Hold, Handoff: derefHandoff(meta.Handoff),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("list sessions: %w", err)
	}
	var cursor string
	if len(out) > limit {
		out = out[:limit]
		cursor = out[len(out)-1].ID
	}
	return out, cursor, nil
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
