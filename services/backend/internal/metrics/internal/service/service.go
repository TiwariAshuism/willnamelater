package service

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/getnyx/influaudit/backend/internal/connector"
	"github.com/getnyx/influaudit/backend/internal/metrics/internal/model"
	"github.com/getnyx/influaudit/backend/internal/metrics/internal/repository"
	"github.com/getnyx/influaudit/backend/internal/platform/db"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// Ingester is the write surface the audit worker calls. It is separate from the
// HTTP-facing MetricsService: ingestion is a task, not a route.
type Ingester interface {
	// Ingest persists a connector Snapshot for one influencer and audit job. It
	// writes metric_point, post, and comment_sample atomically; commenter
	// identities are pseudonymized before they reach the database.
	Ingest(ctx context.Context, influencerID, auditJobID uuid.UUID, snap connector.Snapshot) error
}

// Service implements both the read API (MetricsService) and the write path
// (Ingester) over the metrics module's tables.
type Service struct {
	beginner db.Beginner
	read     repository.MetricsRepository
	ingest   repository.IngestRepository
	salt     *SaltProvider
}

// New builds the metrics service. beginner starts the ingest transaction; read
// and ingest are the two repository halves; salt pseudonymizes commenters.
func New(beginner db.Beginner, read repository.MetricsRepository, ingest repository.IngestRepository, salt *SaltProvider) *Service {
	return &Service{beginner: beginner, read: read, ingest: ingest, salt: salt}
}

var (
	_ MetricsService = (*Service)(nil)
	_ Ingester       = (*Service)(nil)
)

// GetInfluencerMetrics validates the influencer id and returns its metric
// series. The id is validated here so a malformed path parameter is a clean
// KindInvalid rather than surfacing as a database error.
func (s *Service) GetInfluencerMetrics(ctx context.Context, id string, req model.MetricSeriesRequest) (model.MetricSeriesResponse, error) {
	if _, err := uuid.Parse(id); err != nil {
		return model.MetricSeriesResponse{}, errs.New(errs.KindInvalid, "metrics.invalid_influencer_id", "influencer id must be a uuid")
	}
	return s.read.GetInfluencerMetrics(ctx, id, req)
}

// ListInfluencerPosts validates the influencer id and returns its posts.
func (s *Service) ListInfluencerPosts(ctx context.Context, id string, req model.ListPostsRequest) ([]model.PostResponse, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, errs.New(errs.KindInvalid, "metrics.invalid_influencer_id", "influencer id must be a uuid")
	}
	return s.read.ListInfluencerPosts(ctx, id, req)
}

// pendingComment is a comment whose author has already been hashed but whose
// post foreign key is not yet resolved. The hash is computed before the write
// transaction opens; the post id is filled in once the posts are upserted.
type pendingComment struct {
	platformPostID string
	authorHash     []byte
	body           *string
	postedAt       *time.Time
}

// Ingest writes a snapshot's metrics, posts, and comment samples in one
// transaction. Commenter identities are hashed before the transaction opens, so
// no raw author id is ever held while a database write is in flight and none is
// ever handed to the repository.
func (s *Service) Ingest(ctx context.Context, influencerID, auditJobID uuid.UUID, snap connector.Snapshot) error {
	if influencerID == uuid.Nil {
		return errs.New(errs.KindInvalid, "metrics.ingest_influencer_required", "an influencer id is required to ingest a snapshot")
	}
	if auditJobID == uuid.Nil {
		return errs.New(errs.KindInvalid, "metrics.ingest_audit_required", "an audit job id is required to ingest a snapshot")
	}
	platform := string(snap.Platform)

	posts := make([]model.PostRow, 0, len(snap.Posts))
	for _, p := range snap.Posts {
		if p.ID == "" {
			return errs.New(errs.KindInvalid, "metrics.ingest_post_id_required", "a snapshot post is missing its platform id")
		}
		posts = append(posts, model.PostRow{
			InfluencerID:   influencerID,
			Platform:       platform,
			PlatformPostID: p.ID,
			AuditJobID:     auditJobID,
			PostedAt:       nonZeroTime(p.PublishedAt),
			Permalink:      nonEmpty(p.URL),
			Caption:        nonEmpty(p.Caption),
			LikeCount:      p.Likes,
			CommentCount:   p.Comments,
			ShareCount:     p.Shares,
			ViewCount:      p.Views,
		})
	}

	points := make([]model.MetricPointRow, 0, len(snap.Metrics))
	for _, m := range snap.Metrics {
		points = append(points, model.MetricPointRow{
			Time:         m.At,
			InfluencerID: influencerID,
			Platform:     platform,
			Metric:       m.Name,
			Value:        m.Value,
			AuditJobID:   auditJobID,
		})
	}

	captured := snap.CapturedAt
	if captured.IsZero() {
		captured = time.Now().UTC()
	}
	audience := audienceRows(influencerID, auditJobID, platform, captured, snap.Audience)

	pending := make([]pendingComment, 0, len(snap.Comments))
	for _, c := range snap.Comments {
		hash, err := s.salt.AuthorHash(ctx, c.AuthorID)
		if err != nil {
			return err
		}
		pending = append(pending, pendingComment{
			platformPostID: c.PostID,
			authorHash:     hash,
			body:           nonEmpty(c.Text),
			postedAt:       nonZeroTime(c.At),
		})
	}

	return db.InTx(ctx, s.beginner, func(tx pgx.Tx) error {
		postIDs, err := s.ingest.UpsertPosts(ctx, tx, posts)
		if err != nil {
			return err
		}
		if err := s.ingest.InsertMetricPoints(ctx, tx, points); err != nil {
			return err
		}

		comments := make([]model.CommentRow, 0, len(pending))
		for _, pc := range pending {
			postID, ok := postIDs[pc.platformPostID]
			if !ok {
				// The connector contract guarantees every sampled comment
				// references a post in the same snapshot. A miss is a connector
				// bug; fail loudly rather than silently drop coordination data.
				return errs.New(errs.KindInvalid, "metrics.ingest_orphan_comment",
					"a sampled comment references a post absent from the snapshot")
			}
			comments = append(comments, model.CommentRow{
				PostID:     postID,
				AuthorHash: pc.authorHash,
				Body:       pc.body,
				PostedAt:   pc.postedAt,
			})
		}
		if err := s.ingest.InsertComments(ctx, tx, comments); err != nil {
			return err
		}
		return s.ingest.InsertAudienceDemographics(ctx, tx, audience)
	})
}

// audienceRows flattens a connector AudienceBreakdown into persistence rows, one
// per OBSERVED bucket. A nil breakdown or a nil dimension map yields no rows for
// that dimension — absence is never a zero-filled bucket. It is called outside the
// write transaction; the rows are then written atomically with the rest.
func audienceRows(influencerID, auditJobID uuid.UUID, platform string, capturedAt time.Time, aud *connector.AudienceBreakdown) []model.AudienceDemographicRow {
	if aud == nil {
		return nil
	}
	dims := []struct {
		name string
		dist map[string]float64
	}{
		{"age", aud.AgeGroups},
		{"gender", aud.Gender},
		{"country", aud.Countries},
	}
	var rows []model.AudienceDemographicRow
	for _, d := range dims {
		for bucket, fraction := range d.dist {
			rows = append(rows, model.AudienceDemographicRow{
				InfluencerID: influencerID,
				AuditJobID:   auditJobID,
				Platform:     platform,
				Dimension:    d.name,
				Bucket:       bucket,
				Fraction:     fraction,
				CapturedAt:   capturedAt,
			})
		}
	}
	return rows
}

// nonEmpty returns a pointer to s, or nil when s is empty, so an absent string
// is stored as SQL NULL rather than an empty string.
func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// nonZeroTime returns a pointer to t, or nil when t is the zero time, so an
// unknown timestamp is stored as SQL NULL.
func nonZeroTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
