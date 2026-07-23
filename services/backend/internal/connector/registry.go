package connector

import (
	"fmt"
	"sort"

	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// Registry maps each Platform to its Connector implementation.
//
// It is designed for a build-once, read-many lifecycle: all Register calls
// happen during single-threaded startup wiring, after which the Registry is
// only read (Get/List) and is safe for concurrent use by the audit worker
// pool. Register is intentionally not synchronized and must not race with
// reads or other Registers.
type Registry struct {
	connectors map[Platform]Connector
}

// NewRegistry returns an empty Registry ready for Register calls.
func NewRegistry() *Registry {
	return &Registry{connectors: make(map[Platform]Connector)}
}

// Register adds c under its Platform. Registering a second connector for a
// platform already present is a KindConflict error rather than a silent
// overwrite, so a misconfigured startup fails loudly instead of shadowing a
// connector. A nil connector is rejected as KindInvalid.
func (r *Registry) Register(c Connector) error {
	if c == nil {
		return errs.New(errs.KindInvalid, "connector.nil",
			"cannot register a nil connector")
	}

	p := c.Platform()
	if _, exists := r.connectors[p]; exists {
		return errs.New(errs.KindConflict, "connector.duplicate",
			fmt.Sprintf("a connector is already registered for platform %q", p))
	}

	r.connectors[p] = c
	return nil
}

// Get returns the connector registered for p. The boolean is false when no
// connector is registered for that platform.
func (r *Registry) Get(p Platform) (Connector, bool) {
	c, ok := r.connectors[p]
	return c, ok
}

// List returns the registered platforms in a deterministic (ascending) order,
// making it safe to use for stable logging, admin listings, and tests.
func (r *Registry) List() []Platform {
	platforms := make([]Platform, 0, len(r.connectors))
	for p := range r.connectors {
		platforms = append(platforms, p)
	}
	sort.Slice(platforms, func(i, j int) bool { return platforms[i] < platforms[j] })
	return platforms
}
