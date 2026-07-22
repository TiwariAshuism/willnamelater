// Package api is the apigen source for the analytics module. The AnalyticsAPI
// interface is the single declaration of the module's HTTP surface; the OpenAPI
// generator reflects it into the committed spec, so every mounted route is a
// documented part of the contract.
package api

import (
	"context"

	"github.com/getnyx/influaudit/backend/internal/analytics/internal/model"
)

// AnalyticsAPI declares the analytics module's HTTP endpoints: the public
// first-party ingest for funnel + external-share-open events, and the caller-gated
// aggregate read.
type AnalyticsAPI interface {
	// POST /events
	//
	// PUBLIC. Records one funnel or share-open event. Unauthenticated first-party
	// ingest: the server hashes the User-Agent and never stores a raw IP, and it
	// never trusts a client-claimed identity for anything sensitive.
	Ingest(ctx context.Context, req model.IngestRequest) error

	// GET /events/summary
	//
	// The per-event-type counts over the raw event log. Caller-gated.
	Summary(ctx context.Context) (model.SummaryResponse, error)
}
