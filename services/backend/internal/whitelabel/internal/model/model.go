// Package model holds the whitelabel module's request and response DTOs. They
// define the module's HTTP contract shape; the service that populates them is a
// scaffold returning errs.ErrNotImplemented until white-label branding is built.
package model

import "time"

// UpdateWhitelabelRequest is the body of PUT /whitelabel: the branding an agency
// applies to reports and the public badge shown to its clients.
type UpdateWhitelabelRequest struct {
	BrandName    string `json:"brand_name" binding:"required"`
	LogoURL      string `json:"logo_url"`
	PrimaryColor string `json:"primary_color"`
	CustomDomain string `json:"custom_domain"`
}

// WhitelabelResponse is the caller's current white-label branding.
type WhitelabelResponse struct {
	BrandName    string    `json:"brand_name"`
	LogoURL      string    `json:"logo_url"`
	PrimaryColor string    `json:"primary_color"`
	CustomDomain string    `json:"custom_domain"`
	UpdatedAt    time.Time `json:"updated_at"`
}
