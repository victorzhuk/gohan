// Compaction publication contracts shared by the memory store and the
// PostgreSQL adapter: the deterministic publication identity, the canonical
// summary checksum, the audit payload, and the optional append seams the
// sinks implement.
package stores

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"strconv"

	"github.com/victorzhuk/gohan/core/types"
)

// The domain tags frame the canonical byte encodings below so hashes from
// different records are domain-separated.
const (
	compactionPublicationDomain = "gohan.compaction.publication.v1"
	compactionAuditDomain       = "gohan.compaction.audit.v1"
)

// MetadataPayloadKey carries the durable publication payload inside the
// compacted message's Meta map.
const MetadataPayloadKey = "gohan.compaction.publication"

var (
	// errCompactionPayload rejects a malformed or unauthorized publication
	// request before any identity lookup runs.
	errCompactionPayload = errors.New("gohan: invalid compaction publication")
	// errCompactionConflict rejects a publication that reuses an accepted
	// identity with different producer fields. It never wraps
	// types.ErrVersionConflict.
	errCompactionConflict = errors.New("gohan: conflicting compaction publication")
)

// CompactionPublication is the durable payload a producer stores under
// Message.Meta[MetadataPayloadKey]. RunID names the original publisher; a
// reconciling run never rewrites it. FromVersion is the loaded version and
// ToVersion is FromVersion + 1. Tenant and Subject describe the durable
// publisher, never the credentials of the appending request.
type CompactionPublication struct {
	PublicationID string
	SessionID     string
	RunID         string
	RootRunID     string
	Flow          string
	Tenant        string
	Subject       string
	FromVersion   int64
	ToVersion     int64
	TokensBefore  int
	TokensAfter   int
	Policy        string
	SummarySHA    string
}

// CompactionFallback folds history like a Compactor and additionally lets a
// policy validate that a profile is acceptable before the fold runs.
type CompactionFallback interface {
	Projection
	ValidateCompactionFallback(p types.ModelProfile) error
}

// SummarySource produces the summary blocks covering the newly covered
// ordinary messages of a history. It receives neither previously covered raw
// messages nor prior Compaction blocks; History.Version is the selected
// absolute CoversUpTo.
type SummarySource func(context.Context, History, types.ModelProfile) ([]types.Block, error)

// SessionCompactionAppender is the optional seam a session log implements to
// accept a compaction publication as exactly one durable message with a
// predetermined identity. Ordinary Append keeps counter-assigned IDs.
type SessionCompactionAppender interface {
	AppendCompaction(ctx context.Context, sessionID string, expectedVersion int64, message types.Message) (int64, error)
}

// CompactionAuditAppender is the optional seam an audit log implements to
// accept a compaction record once per (SessionID, PublicationID).
type CompactionAuditAppender interface {
	AppendCompactionAudit(ctx context.Context, record AuditRecord) (bool, error)
}

// CompactionEventAppender is the optional seam an event log implements to
// accept a compaction event once per (runID, publicationID), independently of
// event retention.
type CompactionEventAppender interface {
	AppendCompactionEvent(ctx context.Context, publicationID string, runID string, event Event) (bool, error)
}

// CompactionAudit is the producer-owned payload of an AuditCompacted audit
// record. The sink owns At, PrevHash, and Hash; they are excluded from the
// canonical hash segment.
type CompactionAudit struct {
	PublicationID string
	FromVersion   int64
	ToVersion     int64
	TokensBefore  int
	TokensAfter   int
	Policy        string
	SummarySHA    string
}

// CompactionPublicationID derives the deterministic publication identity from
// the identity triple. Identical triples produce identical IDs; any distinct
// triple produces a different ID.
func CompactionPublicationID(sessionID string, runID string, fromVersion int64) string {
	buf := appendFrameComponent(nil, compactionPublicationDomain)
	buf = appendFrameComponent(buf, sessionID)
	buf = appendFrameComponent(buf, runID)
	buf = appendFrameComponent(buf, strconv.FormatInt(fromVersion, 10))
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:])
}

// CompactionSummarySHA returns the lowercase SHA-256 hex of the canonical
// encoding/json/v2 serialization of the summary blocks. The producer, the
// store validator, and the replay decoder must all use this helper so they
// checksum the same bytes.
func CompactionSummarySHA(summary []types.Block) (string, error) {
	encoded, err := json.Marshal(summary)
	if err != nil {
		return "", fmt.Errorf("gohan: encode compaction summary: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

// CompactionAuditHashBytes frames the canonical hash segment of a compaction
// audit payload: the audit domain tag, then every payload field in declaration
// order with integers in base-10 ASCII. A nil payload has no segment.
func CompactionAuditHashBytes(p *CompactionAudit) []byte {
	if p == nil {
		return nil
	}
	buf := appendFrameComponent(nil, compactionAuditDomain)
	buf = appendFrameComponent(buf, p.PublicationID)
	buf = appendFrameComponent(buf, strconv.FormatInt(p.FromVersion, 10))
	buf = appendFrameComponent(buf, strconv.FormatInt(p.ToVersion, 10))
	buf = appendFrameComponent(buf, strconv.Itoa(p.TokensBefore))
	buf = appendFrameComponent(buf, strconv.Itoa(p.TokensAfter))
	buf = appendFrameComponent(buf, p.Policy)
	buf = appendFrameComponent(buf, p.SummarySHA)
	return buf
}

// CloneCompactionAudit returns an independent copy of p, or nil for nil, so
// callers cannot observe or mutate stored payload bytes through a returned
// pointer.
func CloneCompactionAudit(p *CompactionAudit) *CompactionAudit {
	if p == nil {
		return nil
	}
	clone := *p
	return &clone
}

// appendFrameComponent appends one canonical component: the base-10 UTF-8
// byte length, a colon, then the exact bytes.
func appendFrameComponent(dst []byte, s string) []byte {
	dst = append(dst, strconv.Itoa(len(s))...)
	dst = append(dst, ':')
	return append(dst, s...)
}
