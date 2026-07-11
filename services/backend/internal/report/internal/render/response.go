package render

// This file holds the report module's API response DTOs that are not the
// rendered document itself: the result of publishing a shareable report, and the
// public badge projection served unauthenticated by slug. They live in the
// render package so the openapigen source (internal/report/api) reflects one
// package for every report response shape.

// PublishResult is returned by POST /audits/:id/report/publish. It hands the
// caller the durable public slug for the badge, the relative badge URL, and a
// time-limited presigned link to the stored PDF. The slug is stable across
// re-publishes of the same audit, so a shared badge link never breaks.
type PublishResult struct {
	PublicSlug string `json:"public_slug"`
	// BadgeURL is the API-relative path to the public badge projection.
	BadgeURL string `json:"badge_url"`
	// PDFURL is a presigned, expiring link to the rendered PDF in object storage.
	PDFURL    string `json:"pdf_url"`
	ExpiresAt string `json:"expires_at,omitempty"`
}

// PublicBadge is the unauthenticated projection served at GET /reports/:slug. It
// is a deliberately-limited snapshot captured at publish time — the headline
// score and its context, never the private advisory narrative or the account
// owner. Available is always true for a resolved slug (an unknown slug is a
// not-found, not an empty badge).
type PublicBadge struct {
	Handle         string  `json:"handle,omitempty"`
	Overall        float64 `json:"overall"`
	Authenticity   float64 `json:"authenticity"`
	Niche          string  `json:"niche,omitempty"`
	Tier           string  `json:"tier,omitempty"`
	BenchmarkLabel string  `json:"benchmark_label,omitempty"`
	GeneratedAt    string  `json:"generated_at,omitempty"`
	// PDFURL is a presigned, expiring link to the full rendered PDF.
	PDFURL string `json:"pdf_url,omitempty"`
}
