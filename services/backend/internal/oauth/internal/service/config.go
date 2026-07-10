package service

import (
	"time"

	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// DefaultStateTTL bounds how long a user has to complete an authorization after
// starting it. It is deliberately short: the state is a single-use CSRF token,
// not a session, and a shorter window shrinks the replay surface.
const DefaultStateTTL = 10 * time.Minute

// Config carries the deployment-specific settings the service needs. app
// populates it from the application configuration.
type Config struct {
	// RedirectBaseURL is the public origin the provider redirects back to, with
	// no trailing slash (e.g. "https://api.influaudit.com"). The per-provider
	// callback path is appended to it, and the exact same value is sent to the
	// provider at authorization time, so it must match the app's registered
	// redirect URIs.
	RedirectBaseURL string
	// StateTTL overrides DefaultStateTTL when positive.
	StateTTL time.Duration
}

func (c Config) stateTTL() time.Duration {
	if c.StateTTL > 0 {
		return c.StateTTL
	}
	return DefaultStateTTL
}

func (c Config) validate() error {
	if c.RedirectBaseURL == "" {
		return errs.New(errs.KindInternal, "oauth.misconfigured",
			"oauth redirect base url is not configured")
	}
	return nil
}
