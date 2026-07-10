package app

import (
	"net/http"
	"os"
	"path/filepath"

	"github.com/gin-gonic/gin"
	swgui "github.com/swaggest/swgui/v5emb"

	"github.com/getnyx/influaudit/backend/internal/platform/config"
	"github.com/getnyx/influaudit/backend/internal/platform/db"
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

	r.GET("/healthz", a.healthz)
	r.GET("/readyz", a.readyz)

	// Module routers mount under /v1 as each module lands.
	_ = r.Group("/v1")

	a.mountSwagger(r)

	return r
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
