package model

import "github.com/google/uuid"

// ToDisputeResponse projects a domain Dispute onto its DTO. A uuid.Nil actor —
// an account deleted after the fact, or an unresolved dispute's resolver — maps
// to an empty string so the omitempty field drops out rather than rendering an
// all-zero uuid.
func ToDisputeResponse(d Dispute) DisputeResponse {
	resp := DisputeResponse{
		ID:         d.ID.String(),
		AuditJobID: d.AuditJobID.String(),
		Reason:     d.Reason,
		Status:     string(d.Status),
		Resolution: d.Resolution,
		ResolvedAt: d.ResolvedAt,
		CreatedAt:  d.CreatedAt,
		UpdatedAt:  d.UpdatedAt,
	}
	if d.RaisedBy != uuid.Nil {
		resp.RaisedBy = d.RaisedBy.String()
	}
	if d.ResolvedBy != uuid.Nil {
		resp.ResolvedBy = d.ResolvedBy.String()
	}
	return resp
}
