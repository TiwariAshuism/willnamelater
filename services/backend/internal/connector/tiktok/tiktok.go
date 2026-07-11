// Package tiktok is a scaffold connector for TikTok. It implements
// connector.Connector so the platform's shape exists in the registry model, but
// Fetch returns errs.ErrNotImplemented: a live integration needs a TikTok
// developer app and Content Posting / Research API access review that this
// project does not yet hold, so it cannot be honestly verified against the real
// API. Until that access lands the connector must stay disabled in
// connectors.yaml and unregistered in the composition root.
//
// When implemented it will follow the youtube connector's shape: an injected
// Doer HTTP client, a Config carrying the API base URL and credentials, and a
// CostOf pre-debit against the rate-limit ledger. The Capabilities advertised
// below describe what the TikTok API exposes, not what this scaffold serves.
package tiktok

import (
	"context"

	"github.com/getnyx/influaudit/backend/internal/connector"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// Connector is the TikTok scaffold. It holds no state and is safe for concurrent
// use; every Fetch returns errs.ErrNotImplemented until the integration is built.
type Connector struct{}

var _ connector.Connector = (*Connector)(nil)

// New returns the TikTok scaffold connector.
func New() *Connector { return &Connector{} }

// Platform returns connector.PlatformTikTok.
func (c *Connector) Platform() connector.Platform { return connector.PlatformTikTok }

// Capabilities reports the data the TikTok API exposes and a live connector will
// serve: profile identity, follower/engagement metrics, recent videos, and their
// comments. A fresh slice is returned each call so a caller cannot mutate shared
// state.
func (c *Connector) Capabilities() []connector.Capability {
	return []connector.Capability{
		connector.CapabilityProfile,
		connector.CapabilityMetrics,
		connector.CapabilityRecentPosts,
		connector.CapabilityComments,
	}
}

// Fetch returns errs.ErrNotImplemented. The scaffold cannot reach the TikTok API
// without an approved developer app, so it fails as an explicit typed error
// rather than returning an empty snapshot that would read as a real, if barren,
// result.
func (c *Connector) Fetch(context.Context, connector.FetchRequest) (connector.Snapshot, error) {
	return connector.Snapshot{}, errs.ErrNotImplemented
}
