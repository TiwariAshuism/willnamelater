// Package api is the openapigen source for the dataimport module: an annotated
// Go interface from which the OpenAPI contract for the import endpoints is
// derived. The handler and service are hand-written; this interface exists so
// openapigen discovers the routes and reflects their request/response shapes.
package api

import (
	"context"

	"github.com/getnyx/influaudit/backend/internal/dataimport/internal/model"
)

// DataImportAPI declares the dataimport module's HTTP endpoints. The
// authenticated caller's identity travels on the context, so it is not a
// parameter.
type DataImportAPI interface {
	// POST /imports/instagram
	ImportInstagram(ctx context.Context, req model.ImportRequest) (model.ImportResponse, error)
}
