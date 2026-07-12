// Package connector defines the platform-agnostic contract every social
// platform integration implements, plus the declarative configuration and
// registry that let an operator add a platform by implementing one interface,
// registering it, and dropping a config block into connectors.yaml.
//
// The audit orchestrator depends only on the Connector interface and the
// Snapshot value it returns; it never imports a concrete platform package.
// This keeps rate-limit handling, auth quirks, and scraping fallbacks isolated
// per platform and independently swappable.
package connector

import (
	"context"
	"time"
)

// Platform identifies a social platform a Connector integrates with. Values
// are lowercase and stable: they key the Registry, index connectors.yaml, and
// are persisted on audit snapshots, so they must never be renamed casually.
type Platform string

// The Platform values below must stay in sync with the platform enum in
// packages/config/connectors.schema.json; config loading rejects any value not
// named here.
const (
	PlatformYouTube   Platform = "youtube"
	PlatformInstagram Platform = "instagram"
	PlatformFacebook  Platform = "facebook"
	PlatformTikTok    Platform = "tiktok"
	PlatformX         Platform = "x"
	PlatformLinkedIn  Platform = "linkedin"
)

// DataSource names the concrete data path that produced a Snapshot. It is
// distinct from Platform: the same platform can be reached by a live,
// authenticated API pull, an uploaded export, or a licensed public-data
// provider, and those carry very different trust. Downstream, the verification
// tier of a score is derived from the sources of the snapshots that fed it — a
// live-API pull is "verified", an upload or provider read is "estimated".
//
// This is deliberately the one provenance axis added to the connector layer;
// the broader per-field {value, source, confidence} wrapper is left for when the
// content-quality and reach/valuation models land and actually need it.
type DataSource string

const (
	// SourceYouTubeAPI is a live pull from the YouTube Data API.
	SourceYouTubeAPI DataSource = "youtube-api"
	// SourceInstagramGraph is a live pull from the Meta/Instagram Graph API
	// against a connected Business/Creator account (Flow A).
	SourceInstagramGraph DataSource = "instagram-graph"
	// SourceProviderPublic is a read from a licensed third-party public-data
	// provider (Flow B). Inherently incomplete versus an OAuth pull.
	SourceProviderPublic DataSource = "provider"
	// SourceCSVUpload is a creator's own uploaded Insights export.
	SourceCSVUpload DataSource = "csv"
)

// Capability enumerates a discrete kind of data a Connector can return. A
// FetchRequest names the subset the orchestrator wants; a Connector advertises
// the superset it can serve via Capabilities. The intersection is what a Fetch
// actually attempts, which is what lets an audit proceed against a connector
// that cannot, for example, resolve audience demographics.
type Capability string

// The Capability values below must stay in sync with the capabilities enum in
// packages/config/connectors.schema.json.
const (
	CapabilityProfile           Capability = "profile"
	CapabilityMetrics           Capability = "metrics"
	CapabilityRecentPosts       Capability = "recent_posts"
	CapabilityAudienceBreakdown Capability = "audience_breakdown"
	// CapabilityComments returns individual comments with their author
	// identifier and timestamp. It is the sole fuel for the co-commenter graph
	// that drives engagement-pod detection; a platform that cannot supply it
	// can be audited, but not for coordinated engagement.
	CapabilityComments Capability = "comments"
)

// OAuthToken is the live, decrypted credential handed to a Connector for a
// single Fetch. It is the in-memory counterpart of the envelope-encrypted token
// held at rest by the oauth module (see internal/platform/crypto): callers
// decrypt just before the fetch and discard afterward, so this type never
// touches the database and is never logged.
//
// A nil *OAuthToken on a FetchRequest means an unauthenticated fetch (public
// profile data only), which many connectors support with reduced fidelity.
type OAuthToken struct {
	AccessToken  string
	RefreshToken string
	Expiry       time.Time
	Scopes       []string
}

// Expired reports whether the access token is no longer valid at now. A zero
// Expiry is treated as "never expires", matching tokens issued without an
// explicit lifetime.
func (t *OAuthToken) Expired(now time.Time) bool {
	return !t.Expiry.IsZero() && !now.Before(t.Expiry)
}

// FetchRequest is the input to Connector.Fetch. It names the subject account
// and bounds the work so a connector can respect the platform's quota.
type FetchRequest struct {
	// Handle is the platform-specific public identifier (e.g. an Instagram
	// username or YouTube channel handle). Always set.
	Handle string
	// AccountID is the platform's internal id for the account when already
	// known, letting a connector skip a handle-resolution call. Optional.
	AccountID string
	// Token is the live credential for authenticated fetches; nil requests an
	// unauthenticated, public-data-only fetch.
	Token *OAuthToken
	// Capabilities is the subset of data the orchestrator wants. An empty slice
	// means "everything this connector supports".
	Capabilities []Capability
	// Since lower-bounds returned time-series points and posts. A zero value
	// lets the connector apply its own default lookback window.
	Since time.Time
	// MaxPosts caps how many recent posts to return. Zero lets the connector
	// apply its own default, chosen to stay within one quota budget.
	MaxPosts int
}

// Wants reports whether capability was requested. An empty request
// Capabilities means every capability is wanted.
func (r FetchRequest) Wants(capability Capability) bool {
	if len(r.Capabilities) == 0 {
		return true
	}
	for _, c := range r.Capabilities {
		if c == capability {
			return true
		}
	}
	return false
}

// MetricPoint is one time-stamped scalar reading, e.g. follower count on a
// given day. Connectors return the densest history the platform exposes; the
// scoring engine downsamples as needed for trend charts.
type MetricPoint struct {
	At    time.Time
	Name  string // e.g. "followers", "subscribers", "views", "impressions"
	Value float64
}

// Post is a single piece of published content with its engagement counters.
// Counters absent on a platform are left zero rather than guessed.
type Post struct {
	ID          string
	URL         string
	PublishedAt time.Time
	Caption     string
	Likes       int64
	Comments    int64
	Shares      int64
	Views       int64
}

// Comment is a single comment left on a Post, carrying just enough to build a
// co-commenter graph: who commented, on what, and when.
//
// AuthorID is the platform's stable identifier for the commenter (a YouTube
// channel ID, an Instagram user ID). It is PERSONAL DATA about a third party
// who is not our user and never consented, so it must be salted-hash
// pseudonymized before it is persisted — see the metrics module. It is carried
// in the clear only in memory, for the duration of one audit.
type Comment struct {
	// PostID matches the ID of the Post this comment belongs to. Without it a
	// comment cannot be joined to its post, and every per-post coordination
	// feature is unrecoverable.
	PostID   string
	AuthorID string
	Text     string
	At       time.Time
}

// AudienceBreakdown holds demographic distributions. Each map's values are
// fractions in [0,1] that sum to at most 1 (a platform may report only its top
// buckets). A nil map means the platform did not expose that dimension.
type AudienceBreakdown struct {
	// Countries maps ISO 3166-1 alpha-2 code to its audience fraction.
	Countries map[string]float64
	// AgeGroups maps a bucket label (e.g. "18-24") to its audience fraction.
	AgeGroups map[string]float64
	// Gender maps a label (e.g. "female", "male", "unknown") to its fraction.
	Gender map[string]float64
}

// Snapshot is the normalized result of one Fetch: everything the scoring and
// authenticity engines need from a platform at a point in time. Every connector
// maps its platform's native shape onto this type so downstream code stays
// platform-agnostic.
type Snapshot struct {
	Platform Platform
	// Source is the concrete data path that produced this snapshot (live API,
	// upload, or provider). It distinguishes a verified live pull from an
	// estimated one independent of Platform, and drives a score's verification
	// tier. A connector sets it on every Snapshot it returns.
	Source     DataSource
	Handle     string
	AccountID  string
	CapturedAt time.Time
	Followers  int64
	Metrics    []MetricPoint
	Posts      []Post
	// Comments are the individual comments sampled across Posts, present only
	// when CapabilityComments was requested and the connector supports it. Each
	// Comment.PostID references a Post.ID in the same Snapshot.
	Comments []Comment
	// Audience is nil when audience data was not requested or the platform
	// (or the account's access tier) does not expose it.
	Audience *AudienceBreakdown
	// Partial is true when the connector deliberately omitted one or more
	// requested capabilities, e.g. because a rate limit was hit mid-fetch. The
	// orchestrator records the audit as partial rather than failing it.
	Partial bool
}

// Connector integrates one social platform. Implementations are constructed
// from a PlatformConfig at startup and registered in a Registry.
//
// Implementations must be safe for concurrent use: the orchestrator fans out
// audits across a worker pool and may call Fetch on the same Connector value
// from multiple goroutines.
type Connector interface {
	// Platform returns the platform this connector serves. It is constant for
	// the lifetime of the value and is the key under which the connector is
	// registered.
	Platform() Platform

	// Capabilities returns every Capability this connector can satisfy. The
	// slice is treated as read-only by callers.
	Capabilities() []Capability

	// Fetch retrieves a Snapshot for the requested account.
	//
	// Contract:
	//   - Fetch MUST honor ctx cancellation and deadline, abandoning in-flight
	//     platform calls and returning ctx.Err() promptly when ctx is done.
	//   - On a platform rate limit, Fetch MUST return a *RateLimitError (with
	//     RetryAfter set when the platform reports it) so the orchestrator can
	//     degrade to a partial audit and reschedule rather than failing the
	//     whole job. Callers detect it with errors.As.
	//   - On exhausted daily/periodic quota, Fetch MUST return a
	//     *QuotaExhaustedError for the same reason.
	//   - Any returned Snapshot with Partial set signals that the result is
	//     usable but incomplete; a non-nil error means no usable Snapshot.
	Fetch(ctx context.Context, req FetchRequest) (Snapshot, error)
}
