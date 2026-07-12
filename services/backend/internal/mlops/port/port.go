// Package port declares the consumer-side interfaces the mlops module depends
// on. The module owns the feature store, the model registry, the canary set, and
// the prediction log, but it reaches identity, service authentication, and
// object storage only through the interfaces below, so it imports no other
// business module and constructs no client of its own.
//
// The composition root satisfies each port: AdminGuard over auth's admin bit,
// ServiceAuth over the static ml service token, and ArtifactStore over the
// hand-rolled internal/platform/storage client (which satisfies it directly).
package port

import (
	"context"

	"github.com/google/uuid"
)

// AdminGuard authorises an admin-only action. It returns the caller's id when
// they are an authenticated admin, an unauthorized domain error when no identity
// is present, and a forbidden domain error when the caller is authenticated but
// not an admin. app satisfies it over auth's identity (the users.role = 'admin'
// bit), exactly as the admin module's AdminGuard is wired. The trainer
// authenticates with the same admin JWT `make ml-train` passes as --token.
type AdminGuard interface {
	RequireAdmin(ctx context.Context) (uuid.UUID, error)
}

// ServiceAuth authorises a machine caller (the ml server) on the prediction-log
// ingest route. It returns nil when the request carried the valid ml service
// token and an unauthorized domain error otherwise. The caller is a service, not
// a user, so this is a static bearer (INFLUAUDIT_ML_SERVICE_TOKEN) rather than a
// JWT; app satisfies it, comparing the presented token in constant time.
type ServiceAuth interface {
	RequireService(ctx context.Context) error
}

// ArtifactStore is the object-storage surface the register endpoint needs: it
// PUTs a challenger's model file and manifest under the model/version prefix.
// The backend is the only writer of ml-model artifacts (the trainer never holds
// S3 credentials). The hand-rolled internal/platform/storage.Client satisfies it
// directly, so app injects the bucket-scoped client with no adapter. Reads for
// rollback are not needed here: the promote endpoint returns the manifest stored
// in Postgres and the CLI re-materialises the artifact from S3 itself.
type ArtifactStore interface {
	PutObject(ctx context.Context, key, contentType string, data []byte) (etag string, err error)
}
