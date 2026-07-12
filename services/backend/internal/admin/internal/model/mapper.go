package model

import (
	"github.com/google/uuid"

	"github.com/getnyx/influaudit/backend/internal/admin/port"
)

// ToDisputeResponse projects a domain Dispute onto its DTO. A uuid.Nil actor —
// an account deleted after the fact, or an unresolved dispute's resolver — maps
// to an empty string so the omitempty field drops out rather than rendering an
// all-zero uuid.
func ToDisputeResponse(d Dispute) DisputeResponse {
	resp := DisputeResponse{
		ID:                d.ID.String(),
		AuditJobID:        d.AuditJobID.String(),
		Reason:            d.Reason,
		Status:            string(d.Status),
		Resolution:        d.Resolution,
		ResolvedAt:        d.ResolvedAt,
		LabelEvidence:     string(d.LabelEvidence),
		ScoreShownToAdmin: d.ScoreShownToAdmin,
		CreatedAt:         d.CreatedAt,
		UpdatedAt:         d.UpdatedAt,
	}
	if d.RaisedBy != uuid.Nil {
		resp.RaisedBy = d.RaisedBy.String()
	}
	if d.ResolvedBy != uuid.Nil {
		resp.ResolvedBy = d.ResolvedBy.String()
	}
	return resp
}

// ToFraudFeatures projects the stored fraud estimate onto its DTO. Every
// measurement stays a pointer: nil means the signal was never observed, and it
// travels as JSON null rather than a zero a reader would mistake for a confident
// clean measurement. It is a copy of what the audit run recorded, never a
// recomputation.
func ToFraudFeatures(v port.FraudView) FraudFeatures {
	return FraudFeatures{
		Present:                  v.Present,
		RiskScore:                v.RiskScore,
		EngagementAnomaly:        v.EngagementAnomaly,
		CliqueCount:              v.CliqueCount,
		CliqueMembershipFraction: v.CliqueMembershipFraction,
		Confidence:               v.Confidence,
		ModelVersion:             v.ModelVersion,
	}
}
