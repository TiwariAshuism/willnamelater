package providerpublic_test

import (
	"context"
	"errors"
	"testing"

	"github.com/getnyx/influaudit/backend/internal/connector"
	"github.com/getnyx/influaudit/backend/internal/connector/providerpublic"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// With no wired adapter the connector is honest scaffolding: every fetch is an
// explicit not-implemented error, never an empty-but-successful snapshot.
func TestFetchWithoutAdapterIsNotImplemented(t *testing.T) {
	c := providerpublic.New(connector.PlatformInstagram, nil)
	_, err := c.Fetch(context.Background(), connector.FetchRequest{Handle: "someone"})
	if !errors.Is(err, errs.ErrNotImplemented) {
		t.Fatalf("err = %v, want ErrNotImplemented", err)
	}
}

type fakeAdapter struct {
	snap connector.Snapshot
	err  error
}

func (f fakeAdapter) FetchPublic(context.Context, string) (connector.Snapshot, error) {
	return f.snap, f.err
}

// A wired adapter's snapshot is always stamped as provider-sourced and partial,
// even if the adapter tried to claim otherwise — the connector, not the vendor,
// owns provenance, so public data can never be mislabelled as a verified pull.
func TestFetchStampsProviderProvenance(t *testing.T) {
	adapter := fakeAdapter{snap: connector.Snapshot{
		Source:  connector.SourceYouTubeAPI, // adapter lies; connector must override
		Partial: false,
		Handle:  "someone",
	}}
	c := providerpublic.New(connector.PlatformInstagram, adapter)

	snap, err := c.Fetch(context.Background(), connector.FetchRequest{Handle: "someone"})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if snap.Source != connector.SourceProviderPublic {
		t.Errorf("Source = %q, want provider (connector must override the adapter)", snap.Source)
	}
	if !snap.Partial {
		t.Error("provider snapshot must be Partial (public data is incomplete vs OAuth)")
	}
	if snap.Platform != connector.PlatformInstagram {
		t.Errorf("Platform = %q, want instagram", snap.Platform)
	}
}

// Public-provider data has no per-comment author identities, so the connector
// must not advertise comments or audience breakdown — coordination is honestly
// uncomputable from this path.
func TestCapabilitiesExcludeCommentsAndAudience(t *testing.T) {
	c := providerpublic.New(connector.PlatformInstagram, nil)
	for _, cap := range c.Capabilities() {
		if cap == connector.CapabilityComments || cap == connector.CapabilityAudienceBreakdown {
			t.Fatalf("provider must not advertise %q", cap)
		}
	}
}
