package x

import (
	"context"
	"errors"
	"testing"

	"github.com/getnyx/influaudit/backend/internal/connector"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// The scaffold satisfies the connector.Connector contract.
var _ connector.Connector = New()

func TestPlatform(t *testing.T) {
	if got := New().Platform(); got != connector.PlatformX {
		t.Fatalf("Platform() = %q, want %q", got, connector.PlatformX)
	}
}

func TestCapabilitiesReturnsAFreshSlice(t *testing.T) {
	c := New()
	first := c.Capabilities()
	if len(first) == 0 {
		t.Fatal("Capabilities() must advertise the platform's capabilities")
	}
	first[0] = connector.CapabilityAudienceBreakdown
	if c.Capabilities()[0] == connector.CapabilityAudienceBreakdown {
		t.Fatal("Capabilities() leaked shared state; a caller mutated the connector")
	}
}

func TestFetchReturnsNotImplemented(t *testing.T) {
	snap, err := New().Fetch(context.Background(), connector.FetchRequest{Handle: "someone"})
	if !errors.Is(err, errs.ErrNotImplemented) {
		t.Fatalf("Fetch() error = %v, want errs.ErrNotImplemented", err)
	}
	if errs.Status(err) != 501 {
		t.Fatalf("errs.Status = %d, want 501", errs.Status(err))
	}
	if snap.Platform != "" || snap.Handle != "" || len(snap.Posts) != 0 {
		t.Fatalf("Fetch() returned a non-zero snapshot on error: %+v", snap)
	}
}
