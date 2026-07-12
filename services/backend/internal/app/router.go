package app

import (
	"net/http"
	"os"
	"path/filepath"

	"github.com/gin-gonic/gin"
	swgui "github.com/swaggest/swgui/v5emb"

	"github.com/getnyx/influaudit/backend/internal/auth"
	"github.com/getnyx/influaudit/backend/internal/billing"
	"github.com/getnyx/influaudit/backend/internal/platform/config"
	"github.com/getnyx/influaudit/backend/internal/platform/db"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
	"github.com/getnyx/influaudit/backend/internal/platform/httpx"
	"github.com/getnyx/influaudit/backend/internal/platform/redis"
)

// specPath is where cmd/openapigen writes the assembled OpenAPI document. It is
// served alongside the Swagger UI so the UI and the CI drift check read the
// exact same bytes.
const specPath = "packages/contracts/openapi/influaudit.yaml"

// Router builds the HTTP surface: middleware, health probes, module routes, and
// — outside production — the Swagger UI.
func (a *App) Router() *gin.Engine {
	if a.Config.Environment == config.EnvProd {
		gin.SetMode(gin.ReleaseMode)
	}

	// gin.New, not gin.Default: Default installs its own logger and a recovery
	// middleware that writes the panic value to the response.
	r := gin.New()
	r.Use(httpx.RequestID(), httpx.Recovery())

	// Without this, gin answers a wrong-method request with 404 and the NoMethod
	// handler below never runs.
	r.HandleMethodNotAllowed = true

	// Gin's defaults render plain text ("404 page not found"), so an unknown
	// route would answer in a different shape from every other error the API
	// emits. Route both through RenderError to keep one envelope.
	r.NoRoute(func(c *gin.Context) {
		httpx.RenderError(c, errs.New(errs.KindNotFound, "http.not_found", "no such endpoint"))
	})
	r.NoMethod(func(c *gin.Context) {
		httpx.RenderError(c, errs.New(errs.KindInvalid, "http.method_not_allowed", "method not allowed for this endpoint"))
	})

	r.GET("/healthz", a.healthz)
	r.GET("/readyz", a.readyz)

	a.mountModules(r)
	a.mountSwagger(r)

	return r
}

// apiBasePath is the group every module mounts under. cmd/openapigen emits it as
// the spec's server URL; TestEverySpecPathIsMounted fails if the two disagree.
const apiBasePath = "/v1"

// mountModules mounts every business module. There are two groups because the
// API has two authentication models, and a single group would silently apply the
// wrong one to somebody:
//
//   - public: no auth middleware. Registration, login, token refresh, and the
//     Razorpay webhook all necessarily precede or bypass a session.
//   - protected: every request must carry a valid access token, and the caller's
//     identity is placed on the request context for the modules to read.
//
// A module decides which of its own routes belong where. The composition root
// only supplies the groups.
func (a *App) mountModules(r *gin.Engine) {
	m := a.Modules

	public := r.Group(apiBasePath)
	protected := r.Group(apiBasePath, m.Auth.Middleware(), billingCaller())

	// auth mounts on the public group and protects /auth/me itself, because it
	// owns the middleware and knows which of its routes need it.
	m.Auth.RegisterRoutes(public)

	// The webhook is unauthenticated (it proves itself with an HMAC over the raw
	// body); plans, subscription, and subscribe are not.
	m.Billing.RegisterRoutes(protected, public)

	m.OAuth.RegisterRoutes(protected)

	// Meta's deauthorize and data-deletion callbacks mount on the PUBLIC group:
	// Meta holds no session, and the signed_request HMAC'd with our app secret is
	// the only credential (verified in internal/platform/metasig before anything
	// acts on the payload). They are a contractual obligation, not a feature —
	// Meta Platform Terms §3.d requires us to delete a user's Platform Data
	// promptly on request, and these are how that request arrives. An unset
	// META_APP_SECRET fails every call closed.
	metaCallbacks{
		appSecret:     os.Getenv("META_APP_SECRET"),
		oauth:         m.OAuth,
		report:        m.Report,
		publicBaseURL: a.Config.HTTP.PublicBaseURL,
	}.RegisterPublicRoutes(public)

	m.Influencer.RegisterRoutes(protected)
	m.Metrics.RegisterRoutes(protected)
	m.Scoring.RegisterRoutes(protected)

	// The audit orchestrator's routes all act on behalf of a signed-in caller, so
	// they mount on the protected group.
	m.Audit.RegisterRoutes(protected)

	// The report routes hang off the audit resource and are caller-scoped too. The
	// public badge projection is the exception: it mounts on the public group with
	// no auth, exposing only the frozen snapshot captured at publish time.
	m.Report.RegisterRoutes(protected)
	m.Report.RegisterPublicRoutes(public)

	// The data-import upload endpoint is caller-scoped: a creator uploads their
	// own data.
	m.DataImport.RegisterRoutes(protected)

	// Admin routes all require a signed-in caller; the admin-only ones (the review
	// queue, resolve, dashboards, label export) additionally enforce the admin
	// role inside the service through the AdminGuard port. Filing a dispute is
	// open to any authenticated caller, so the whole module mounts on protected.
	m.Admin.RegisterRoutes(protected)

	// Deferred-feature scaffolds mount on the protected group. Every operation
	// returns 501 until the feature is built, so the endpoints are a documented,
	// honest part of the contract rather than a hidden 404.
	m.Alerts.RegisterRoutes(protected)
	m.BulkAudit.RegisterRoutes(protected)
	m.Whitelabel.RegisterRoutes(protected)
	m.Campaign.RegisterRoutes(protected)

	// mlops admin routes (feature-row export, model register/promote, canaries) are
	// admin-gated like the admin module, so they mount on the protected group. The
	// prediction-ingest route is machine-to-machine (the ml server), so it mounts on
	// a separate group carrying the service-token middleware, NOT the user auth
	// middleware.
	m.MLOps.RegisterAdminRoutes(protected)
	mlService := r.Group(apiBasePath, mlServiceTokenMiddleware(a.Config.ML.ServiceToken.Reveal()))
	m.MLOps.RegisterServiceRoutes(mlService)
}

// billingCaller copies the authenticated caller from the auth module's context
// into the billing module's context.
//
// Both modules keep their context keys unexported, so neither can read nor forge
// the other's identity. Bridging them is the composition root's job, and this is
// the seam. It runs after the auth middleware, so an unauthenticated request has
// already been rejected and there is nothing to copy.
func billingCaller() gin.HandlerFunc {
	return func(c *gin.Context) {
		if userID, ok := auth.UserID(c.Request.Context()); ok {
			c.Request = c.Request.WithContext(billing.WithCaller(c.Request.Context(), userID))
		}
		c.Next()
	}
}

// mountSwagger serves the generated OpenAPI document and a UI over it. It is
// never mounted in production: the document enumerates every endpoint and its
// schemas, which is reconnaissance we hand to nobody by default.
func (a *App) mountSwagger(r *gin.Engine) {
	if a.Config.Environment == config.EnvProd {
		return
	}

	spec, err := os.ReadFile(filepath.Clean(specPath))
	if err != nil {
		// The spec is a build artifact. Its absence must not stop the API from
		// serving traffic, so degrade to no UI and say why.
		r.GET("/swagger", func(c *gin.Context) {
			c.String(http.StatusServiceUnavailable,
				"OpenAPI spec not found at %s; run: go run ./cmd/openapigen", specPath)
		})
		return
	}

	r.GET("/openapi.yaml", func(c *gin.Context) {
		c.Data(http.StatusOK, "application/yaml", spec)
	})
	r.GET("/swagger/*any", gin.WrapH(
		swgui.NewHandler("InfluAudit API", "/openapi.yaml", "/swagger/"),
	))
}

// healthz reports process liveness only. It must not touch a dependency: a
// database blip should not cause an orchestrator to kill an otherwise healthy
// process.
func (a *App) healthz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok", "version": Version})
}

// readyz reports whether this process can serve traffic, which does depend on
// its datastores. Orchestrators route on this.
func (a *App) readyz(c *gin.Context) {
	ctx := c.Request.Context()

	if err := db.Check(ctx, a.Pool); err != nil {
		httpx.RenderError(c, err)
		return
	}
	if err := redis.Check(ctx, a.Redis); err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ready"})
}
