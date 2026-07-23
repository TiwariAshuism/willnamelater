package service

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/getnyx/influaudit/backend/internal/oauth/internal/model"
)

// ErrNoInstagramBusinessAccount is the sentinel the provider client returns when
// a Meta login carries no Instagram Business (or Creator) account linked to a
// managed Page. It is a distinct, recoverable condition — the person can fix it
// by linking an account — not a transport failure, so the service maps it to a
// guided-fix domain error rather than a generic exchange failure. It is a
// sentinel (not a domain error) so both the connect and signup callbacks can
// detect it with errors.Is without the provider layer depending on errs codes.
var ErrNoInstagramBusinessAccount = errors.New("oauth: no instagram business account is linked to this login")

// UserProvisioner finds or creates a user by email and returns their id. It is a
// CONSUMER-SIDE PORT: the oauth module declares what signup needs, and the
// composition root adapts the auth module onto it, so oauth never imports auth.
//
// It must be idempotent on email — a visitor who already has an account and
// signs up again through OAuth is logged into the SAME account, never a
// duplicate — which is why it is find-or-create rather than create.
type UserProvisioner interface {
	FindOrCreateUserByEmail(ctx context.Context, email string) (uuid.UUID, error)
}

// InfluencerSignup is the narrow input the signup flow hands the influencer
// provisioner: the owning user, the connected Instagram account id we audit, its
// handle, and the provider's app-scoped user id (carried so the influencer record
// can be reconciled with the deauthorize/data-deletion callbacks later).
type InfluencerSignup struct {
	OwnerUserID        uuid.UUID
	InstagramAccountID string
	Handle             string
	ProviderUserID     string
}

// InfluencerProvisioner upserts the influencer (and its Instagram handle) owned
// by a user for a connected account, returning the influencer id. Another
// CONSUMER-SIDE PORT: the composition root adapts the influencer module onto it.
//
// It is an upsert, not an insert, so a returning creator who reconnects the same
// Instagram account keeps one influencer record rather than accumulating
// duplicates.
type InfluencerProvisioner interface {
	UpsertInstagramInfluencer(ctx context.Context, in InfluencerSignup) (uuid.UUID, error)
}

// SessionIssuer mints an authenticated session (access + refresh tokens) for a
// user id, exactly as login does. The signup callback calls it last, once the
// user, influencer, and connection exist, so the web can set session cookies for
// the new account. The composition root adapts the auth module onto it.
type SessionIssuer interface {
	IssueSession(ctx context.Context, userID uuid.UUID) (model.AuthSession, error)
}

// AuditStarter kicks off an audit for a just-provisioned account so the creator
// lands on a score without a manual step (the PRD's landing→score funnel). It is
// a CONSUMER-SIDE PORT the composition root adapts onto the audit module, so
// oauth never imports audit.
//
// It is OPTIONAL: the signup callback calls it BEST-EFFORT after the account
// exists and a nil starter (or a failure) is not fatal — the creator can still
// run the audit from the dashboard. It takes only the owning user and influencer
// ids; a decrypted platform token is never handed to it.
type AuditStarter interface {
	StartAudit(ctx context.Context, ownerUserID, influencerID uuid.UUID) error
}

// SignupService is the OAuth-as-signup surface the hand-written signup handler
// depends on. It is deliberately separate from the generated OAuthService so the
// existing connect handler and its contract are untouched.
type SignupService interface {
	// AuthorizeSignup begins an anonymous Meta authorization, binding the captured
	// email to the single-use state, and returns the provider consent URL.
	AuthorizeSignup(ctx context.Context, email string) (model.AuthorizeResponse, error)
	// CallbackSignup completes the anonymous authorization: it exchanges the code,
	// provisions the user + influencer + connection from the Meta identity and the
	// captured email, persists the sealed token, and returns a session.
	CallbackSignup(ctx context.Context, params CallbackParams) (model.AuthSession, error)
}
