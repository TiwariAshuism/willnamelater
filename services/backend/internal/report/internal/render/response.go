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

// ShareRequest is the body of POST /audits/:id/report/share: a creator's express
// direction to disclose their published report to a named third party for a
// stated purpose. Both fields are required — Meta Platform Terms §3.c authorizes
// sharing only "with the third party for the purposes as specified in the User's
// direction", so an unnamed recipient or an unstated purpose is not a direction
// we can act on.
type ShareRequest struct {
	// Recipient names the brand or agency the report may be disclosed to.
	Recipient string `json:"recipient"`
	// Purpose states what the recipient may use the report for.
	Purpose string `json:"purpose"`
}

// ShareResult is the receipt for a recorded share grant: the evidence trail that
// the creator directed this disclosure. The grant is time-bounded and the creator
// can withdraw it at any time.
type ShareResult struct {
	GrantID   string `json:"grant_id"`
	Recipient string `json:"recipient"`
	Purpose   string `json:"purpose"`
	ExpiresAt string `json:"expires_at"`
}

// PublicBadge is the unauthenticated projection served at GET /reports/:slug. It
// is a deliberately-limited snapshot captured at publish time — the headline
// score and its context, never the private advisory narrative or the account
// owner. Available is always true for a resolved slug (an unknown slug is a
// not-found, not an empty badge).
type PublicBadge struct {
	Handle         string   `json:"handle,omitempty"`
	Overall        float64  `json:"overall"`
	Authenticity   *float64 `json:"authenticity,omitempty"`
	Niche          string   `json:"niche,omitempty"`
	Tier           string   `json:"tier,omitempty"`
	BenchmarkLabel string   `json:"benchmark_label,omitempty"`
	// VerificationTier is the trust tier ("verified"/"estimated"), the 🟢/🟡
	// signal shown on the public badge. A verified badge rests on live-API data;
	// an estimated one includes uploaded or provider-sourced data.
	VerificationTier string `json:"verification_tier,omitempty"`
	GeneratedAt      string `json:"generated_at,omitempty"`
	// PDFURL is a presigned, expiring link to the full rendered PDF.
	PDFURL string `json:"pdf_url,omitempty"`
}
