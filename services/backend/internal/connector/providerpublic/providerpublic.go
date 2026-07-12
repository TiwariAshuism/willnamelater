// Package providerpublic is the Flow B seam: a connector that serves a platform's
// public-profile data through a LICENSED third-party data provider (e.g. Modash,
// Phyllo, Bright Data), never by scraping. Scraping a platform at any real volume
// carries ToS, IP-ban, and legal risk, so the only sanctioned public-data path is
// a provider whose licensing already covers it.
//
// The provider itself is injected as an Adapter. Until a real, licensed adapter
// is wired, Fetch returns errs.ErrNotImplemented — honest 501 scaffolding, not a
// stub that fabricates public numbers. This lets the shape exist in the registry
// (precedence: live OAuth > provider > uploaded CSV) while the provider choice
// and its ToS sign-off are still pending.
package providerpublic

import (
	"context"

	"github.com/getnyx/influaudit/backend/internal/connector"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// Adapter is the narrow seam a licensed public-data provider implements: given a
// public handle, return a normalized Snapshot. The provider integration lives
// behind this interface so the connector, the registry, and the orchestrator
// never learn which vendor (or none) is wired.
type Adapter interface {
	FetchPublic(ctx context.Context, handle string) (connector.Snapshot, error)
}

// Connector serves a platform's public data via an injected provider Adapter. A
// nil adapter is the pending state: every Fetch is an explicit not-implemented
// error. It holds no mutable state and is safe for concurrent use.
type Connector struct {
	platform connector.Platform
	adapter  Adapter
}

var _ connector.Connector = (*Connector)(nil)

// New builds the provider connector for a platform. adapter may be nil, in which
// case Fetch returns errs.ErrNotImplemented until a licensed provider is wired.
func New(platform connector.Platform, adapter Adapter) *Connector {
	return &Connector{platform: platform, adapter: adapter}
}

// Platform returns the platform this connector serves.
func (c *Connector) Platform() connector.Platform { return c.platform }

// Capabilities reports what a licensed public-data provider can serve: profile
// identity, follower/engagement metrics, and recent posts. It deliberately does
// NOT advertise comments or audience breakdown — a public provider exposes no
// per-comment author identities, so the co-commenter coordination signal
// genuinely cannot be computed from this path (the same honest limitation the
// csvimport connector has). A fresh slice is returned each call.
func (c *Connector) Capabilities() []connector.Capability {
	return []connector.Capability{
		connector.CapabilityProfile,
		connector.CapabilityMetrics,
		connector.CapabilityRecentPosts,
	}
}

// Fetch returns the provider's public snapshot, or errs.ErrNotImplemented when no
// adapter is wired. A real adapter's snapshot is stamped SourceProviderPublic and
// Partial=true: public-provider data is inherently less complete than an OAuth
// pull, and its provenance must downgrade the audit's verification tier to
// "estimated". The connector overwrites these two fields itself so no adapter can
// mislabel its data as a verified, complete pull.
func (c *Connector) Fetch(ctx context.Context, req connector.FetchRequest) (connector.Snapshot, error) {
	if c.adapter == nil {
		return connector.Snapshot{}, errs.ErrNotImplemented
	}
	snap, err := c.adapter.FetchPublic(ctx, req.Handle)
	if err != nil {
		return connector.Snapshot{}, err
	}
	snap.Platform = c.platform
	snap.Source = connector.SourceProviderPublic
	snap.Partial = true
	return snap, nil
}
