package model

// Unit names a metered resource tracked against a user's plan quota. The two
// values map to the audits_used and bulk_audits_used columns of usage_counter.
type Unit string

// The Unit values below are the complete set of metered resources. Any other
// value is rejected as invalid by the quota service.
const (
	// UnitAudit is a single influencer audit, capped by plan.audit_quota.
	UnitAudit Unit = "audit"
	// UnitBulkAudit is a single bulk audit, capped by plan.bulk_quota.
	UnitBulkAudit Unit = "bulk_audit"
)

// Valid reports whether u is a metered resource the quota system understands.
func (u Unit) Valid() bool {
	return u == UnitAudit || u == UnitBulkAudit
}

// ReservationID is an opaque token returned by a successful reservation. It
// encodes the unit, user, and billing period the reservation belongs to so the
// commit and release paths can act on it without a separate reservation table.
type ReservationID string
