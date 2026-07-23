package app

import (
	"context"
	"crypto/subtle"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	adminport "github.com/getnyx/influaudit/backend/internal/admin/port"
	auditport "github.com/getnyx/influaudit/backend/internal/audit/port"
	"github.com/getnyx/influaudit/backend/internal/connector"
	"github.com/getnyx/influaudit/backend/internal/mlops"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
	"github.com/getnyx/influaudit/backend/internal/platform/httpx"
	"github.com/getnyx/influaudit/backend/internal/platform/storage"
	"github.com/getnyx/influaudit/backend/internal/scoring"
)

// This file wires the mlops module's non-identity ports. Its AdminGuard is the
// same adminGuard{} the admin module uses (auth's admin bit); the two below are
// mlops-specific: the ml service-token auth for the machine-to-machine
// prediction-ingest route, and a nil-safe object-store adapter.

// mlServiceCtxKey marks a request that presented the valid ml service token. It
// is unexported so only the composition root can set it, mirroring how auth
// guards identity: no other package can forge the marker.
type mlServiceCtxKey struct{}

// mlServiceTokenMiddleware authenticates the ml server on the /v1/ml routes with
// the static service token, compared in constant time. A request without the
// exact token is rejected before the handler runs; a valid one is marked on the
// request context so mlServiceAuth.RequireService can confirm it. An unset token
// (dev without shadow logging configured) rejects every call — the route is then
// simply unavailable, never open.
func mlServiceTokenMiddleware(token string) gin.HandlerFunc {
	want := []byte(token)
	return func(c *gin.Context) {
		presented := []byte(strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer "))
		if token == "" || subtle.ConstantTimeCompare(presented, want) != 1 {
			httpx.RenderError(c, errs.New(errs.KindUnauthorized, "app.ml_service_unauthorized",
				"this endpoint requires a valid ml service token"))
			c.Abort()
			return
		}
		ctx := context.WithValue(c.Request.Context(), mlServiceCtxKey{}, true)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

// mlServiceAuth adapts the service-token check onto the mlops ServiceAuth port.
// The middleware above performs the comparison and aborts on failure; this
// confirms the marker, so a handler reached without the middleware fails closed.
type mlServiceAuth struct{}

func (mlServiceAuth) RequireService(ctx context.Context) error {
	if ok, _ := ctx.Value(mlServiceCtxKey{}).(bool); ok {
		return nil
	}
	return errs.New(errs.KindUnauthorized, "app.ml_service_unauthorized",
		"this endpoint requires the ml service token")
}

// mlopsStore adapts the platform S3 client onto the mlops ArtifactStore port. The
// client may be nil when object storage is unconfigured (a dev machine with no
// S3): registering a model artifact then fails with an unavailable error rather
// than panicking on a nil client, the same degrade-at-call-time posture the
// report module's storage adapter takes.
type mlopsStore struct{ s *storage.Client }

func (m mlopsStore) PutObject(ctx context.Context, key, contentType string, data []byte) (string, error) {
	if m.s == nil {
		return "", errs.New(errs.KindUnavailable, "app.storage_unconfigured",
			"object storage is not configured")
	}
	return m.s.PutObject(ctx, key, contentType, data)
}

// --- FeatureRecorder: audit -> mlops feature store (the flywheel intake) --

// mlopsFeatureRecorder adapts the audit FeatureRecorder port onto mlops. The
// audit run supplies the snapshots and the fraud sub-vector; this adapter enriches
// them with the niche/tier/verification the score already carries (read back from
// scoring's ReportView) and a real reach label extracted from an Instagram Graph
// pull, then hands a FeatureCapture to the feature store. Nothing is fabricated:
// a missing score leaves niche/tier empty, and no real reach figure leaves
// ReachLabel nil.
type mlopsFeatureRecorder struct {
	mlops   *mlops.Module
	scoring *scoring.Module
}

func (r mlopsFeatureRecorder) RecordFeatures(ctx context.Context, rec auditport.FeatureRecord) error {
	view, err := r.scoring.ReportView(ctx, rec.InfluencerID)
	if err != nil && errs.KindOf(err) != errs.KindNotFound {
		return err
	}
	// On a not-found score the zero view leaves niche/tier/verification empty.
	return r.mlops.RecordFeatureRow(ctx, mlops.FeatureCapture{
		AuditJobID:   rec.AuditJobID,
		InfluencerID: rec.InfluencerID,
		Snapshots:    rec.Snapshots,
		Fraud: mlops.FraudSignal{
			Present:                  rec.Fraud.Present,
			RiskScore:                rec.Fraud.RiskScore,
			EngagementAnomaly:        rec.Fraud.EngagementAnomaly,
			CliqueCount:              rec.Fraud.CliqueCount,
			CliqueMembershipFraction: rec.Fraud.CliqueMembershipFraction,
			Confidence:               rec.Fraud.Confidence,
			ModelVersion:             rec.Fraud.ModelVersion,
		},
		Niche:            view.Niche,
		Tier:             view.Tier,
		VerificationTier: view.VerificationTier,
		ReachLabel:       reachLabelFromSnapshots(rec.Snapshots),
	})
}

// reachLabelFromSnapshots extracts a real reach figure — the median of the reach
// MetricPoints from a live Instagram Graph pull — as the reach model's training
// label. Only instagram-graph snapshots carry a genuine Insights reach; a CSV or
// provider source has none. Returns nil when no real figure exists (never faked).
func reachLabelFromSnapshots(snaps []connector.Snapshot) *int64 {
	var reaches []int64
	for _, s := range snaps {
		if s.Source != connector.SourceInstagramGraph {
			continue
		}
		for _, m := range s.Metrics {
			if m.Name == "reach" && m.Value > 0 {
				reaches = append(reaches, int64(m.Value))
			}
		}
	}
	if len(reaches) == 0 {
		return nil
	}
	sort.Slice(reaches, func(i, j int) bool { return reaches[i] < reaches[j] })
	median := reaches[len(reaches)/2]
	return &median
}

// --- TrainingLabelSink: admin -> mlops fraud-label backfill ---------------

// mlopsLabelSink adapts the admin TrainingLabelSink port onto mlops, mapping a
// resolved dispute's fraudulent/legitimate verdict onto the mlops label source.
type mlopsLabelSink struct{ mlops *mlops.Module }

func (s mlopsLabelSink) RecordDisputeLabel(ctx context.Context, auditJobID uuid.UUID, fraudulent bool, evidence adminport.LabelEvidence) error {
	source := mlops.LabelSourceDisputeUpheld
	if fraudulent {
		source = mlops.LabelSourceDisputeRejected
	}
	// The two enums are declared independently (business modules import no other
	// business module) but their values are identical strings by construction, so
	// the mapping is a conversion. mlops re-validates and REJECTS an unrecognised
	// evidence rather than defaulting it: a label whose basis we cannot name must
	// never be silently treated as an observation.
	return s.mlops.SetFraudLabel(ctx, auditJobID, fraudulent, source, mlops.FraudLabelEvidence(evidence))
}
