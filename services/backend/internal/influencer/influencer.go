// Package influencer is the public entry point of the influencer module: the one
// package outside internal/influencer/internal that the composition root
// imports. It wires the repository, service, and handler together and exposes
// route registration.
//
// Everything behind it lives under internal/influencer/internal, which Go
// forbids any sibling module from importing, so a collaborator can only reach
// this module through this surface.
package influencer

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/getnyx/influaudit/backend/internal/connector"
	"github.com/getnyx/influaudit/backend/internal/influencer/internal/handler"
	"github.com/getnyx/influaudit/backend/internal/influencer/internal/repository"
	"github.com/getnyx/influaudit/backend/internal/influencer/internal/service"
	"github.com/getnyx/influaudit/backend/internal/platform/db"
)

// AuditHandle is one platform account of an influencer, for the audit path.
type AuditHandle struct {
	Platform  connector.Platform
	Handle    string
	AccountID string
}

// AuditProfile is what the audit orchestrator needs to fetch an influencer's
// data: the owning user (nil when the profile has no connected owner, in which
// case only public data is available) and the platform handles to fetch.
type AuditProfile struct {
	OwnerUserID *uuid.UUID
	Handles     []AuditHandle
}

// Module is the wired influencer module. Construct it with New and mount it with
// RegisterRoutes.
type Module struct {
	handler *handler.Handler
	svc     service.InfluencerService
	repo    *repository.PostgresRepository
}

// New builds the influencer module over the shared connection pool. It cannot
// fail: every dependency it needs is already constructed.
func New(pool *db.Pool) *Module {
	repo := repository.New(pool)
	svc := service.New(repo)
	return &Module{handler: handler.New(svc), svc: svc, repo: repo}
}

// AuditProfileOf returns the owner and handles the audit orchestrator needs. It
// exists so audit never imports this module; the composition root adapts it onto
// audit's Connections port.
func (m *Module) AuditProfileOf(ctx context.Context, influencerID uuid.UUID) (AuditProfile, error) {
	owner, handles, err := m.repo.AuditOwnerAndHandles(ctx, influencerID.String())
	if err != nil {
		return AuditProfile{}, err
	}

	out := AuditProfile{OwnerUserID: owner, Handles: make([]AuditHandle, 0, len(handles))}
	for _, h := range handles {
		accountID := ""
		if h.PlatformUserID != nil {
			accountID = *h.PlatformUserID
		}
		out.Handles = append(out.Handles, AuditHandle{
			Platform:  h.Platform,
			Handle:    h.Handle,
			AccountID: accountID,
		})
	}
	return out, nil
}

// NicheOf returns the influencer's content niche, satisfying the scoring
// module's Profiles port. An unset niche yields an empty string with a nil
// error, which scoring treats as the default benchmark cohort.
//
// It exists so scoring never imports this module: the composition root adapts
// this method onto scoring's port.
func (m *Module) NicheOf(ctx context.Context, influencerID uuid.UUID) (string, error) {
	resp, err := m.svc.GetInfluencer(ctx, influencerID.String())
	if err != nil {
		return "", err
	}
	if resp.Niche == nil {
		return "", nil
	}
	return *resp.Niche, nil
}

// UpsertInstagramInfluencer finds or creates the influencer for a connected
// Instagram account owned by ownerUserID, ensuring its handle is on record, and
// returns the influencer id. The composition root adapts it onto the oauth
// module's signup provisioner port (OAuth-as-signup): connecting an account both
// creates the account's profile and claims it for the connecting creator. It is
// not an HTTP route.
func (m *Module) UpsertInstagramInfluencer(ctx context.Context, ownerUserID uuid.UUID, accountID, handle string) (uuid.UUID, error) {
	return m.repo.UpsertInstagramInfluencer(ctx, ownerUserID, accountID, handle)
}

// InstagramHandleOf returns the influencer's Instagram handle, and false when it
// has none on record. The composition root adapts it onto the report module's
// HandleReader port so the public /@handle badge can freeze the creator's handle
// at publish time. It is not an HTTP route.
func (m *Module) InstagramHandleOf(ctx context.Context, influencerID uuid.UUID) (string, bool, error) {
	profile, err := m.AuditProfileOf(ctx, influencerID)
	if err != nil {
		return "", false, err
	}
	for _, h := range profile.Handles {
		if h.Platform == connector.PlatformInstagram && h.Handle != "" {
			return h.Handle, true, nil
		}
	}
	return "", false, nil
}

// RegisterRoutes mounts the influencer endpoints under rg. Every endpoint here
// identifies or mutates a specific influencer, so the composition root applies
// the auth middleware to the group it passes in.
func (m *Module) RegisterRoutes(rg *gin.RouterGroup) {
	m.handler.RegisterRoutes(rg)
}
