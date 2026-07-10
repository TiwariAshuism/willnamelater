package model

import (
	"time"

	"github.com/google/uuid"
)

// MetricPointRow is one row destined for the metric_point time series. It maps
// column-for-column onto migration 000008.
type MetricPointRow struct {
	Time         time.Time
	InfluencerID uuid.UUID
	Platform     string
	Metric       string
	Value        float64
	AuditJobID   uuid.UUID
}

// PostRow is one row to upsert into post (migrations 000009, 000015).
//
// The four engagement counters are plain int64 because the connector layer has
// already resolved "absent" to zero for them. The reach/impression/save
// insights are deliberately absent from this type: the connector snapshot does
// not carry them, so the ingest leaves those columns NULL rather than writing a
// zero that would understate an unconnected account.
type PostRow struct {
	InfluencerID   uuid.UUID
	Platform       string
	PlatformPostID string
	AuditJobID     uuid.UUID
	// Nullable columns are pointers so a missing value is written as SQL NULL,
	// never coerced to a zero that reads as real data downstream.
	PostedAt     *time.Time
	Permalink    *string
	Caption      *string
	LikeCount    int64
	CommentCount int64
	ShareCount   int64
	ViewCount    int64
}

// CommentRow is one row to insert into comment_sample (migrations 000009,
// 000014).
//
// AuthorHash is HMAC-SHA256(author_id, salt); it is the ONLY representation of
// the commenter this type can carry. There is deliberately no AuthorID field:
// the raw platform author id is personal data of a non-consenting third party
// and must never reach the repository, so its absence here is enforced by the
// compiler, not by review.
type CommentRow struct {
	PostID     uuid.UUID
	AuthorHash []byte
	Body       *string
	PostedAt   *time.Time
}
