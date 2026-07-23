package model

import (
	"time"

	"github.com/google/uuid"

	"github.com/getnyx/influaudit/backend/internal/connector"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// Influencer is a creator tracked by the system, independent of the platforms
// they publish on. Nullable columns map to pointer fields so "unset" is
// distinguishable from a zero value.
//
// Tier is never assigned from client input: it is derived from the follower
// counts of the influencer's Handles by TierForFollowers, so a caller cannot
// place an account into a more favorable benchmark cohort.
type Influencer struct {
	ID          uuid.UUID
	DisplayName *string
	Niche       *Niche
	Tier        *Tier
	Country     *string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	// Handles is populated only when a caller asks for a single influencer; the
	// list projection leaves it nil to avoid a fan-out query per row.
	Handles []Handle
}

// Handle is one platform account belonging to an Influencer. The pair
// (Platform, Handle) is unique across the whole table.
type Handle struct {
	ID             uuid.UUID
	InfluencerID   uuid.UUID
	Platform       connector.Platform
	Handle         string
	PlatformUserID *string
	FollowerCount  *int64
	Verified       bool
	LastSeenAt     *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// knownPlatforms is the closed set the influencer_handle.platform enum accepts.
// It mirrors the platform enum declared in migration 000001; a handle for any
// other value is rejected before it reaches the database.
var knownPlatforms = map[connector.Platform]struct{}{
	connector.PlatformYouTube:   {},
	connector.PlatformInstagram: {},
	connector.PlatformFacebook:  {},
	connector.PlatformTikTok:    {},
	connector.PlatformX:         {},
	connector.PlatformLinkedIn:  {},
}

// ParsePlatform validates raw against the known platform set and returns it. An
// unrecognized value yields errs.KindInvalid so the handler renders a 400 rather
// than letting the database reject the enum with a 500.
func ParsePlatform(raw string) (connector.Platform, error) {
	p := connector.Platform(raw)
	if _, ok := knownPlatforms[p]; !ok {
		return "", errs.New(errs.KindInvalid, "influencer.platform_invalid", "platform is not a recognized network")
	}
	return p, nil
}
