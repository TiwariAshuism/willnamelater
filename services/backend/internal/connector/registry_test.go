package connector_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/getnyx/influaudit/backend/internal/connector"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// stubConnector is a minimal Connector used to exercise the Registry without
// pulling in a real platform implementation.
type stubConnector struct {
	platform connector.Platform
	caps     []connector.Capability
}

func (s stubConnector) Platform() connector.Platform         { return s.platform }
func (s stubConnector) Capabilities() []connector.Capability { return s.caps }
func (s stubConnector) Fetch(context.Context, connector.FetchRequest) (connector.Snapshot, error) {
	return connector.Snapshot{Platform: s.platform}, nil
}

func TestRegistryRegisterAndGet(t *testing.T) {
	r := connector.NewRegistry()
	yt := stubConnector{platform: connector.PlatformYouTube, caps: []connector.Capability{connector.CapabilityProfile}}

	if err := r.Register(yt); err != nil {
		t.Fatalf("Register: unexpected error: %v", err)
	}

	got, ok := r.Get(connector.PlatformYouTube)
	if !ok {
		t.Fatal("Get(youtube): ok = false, want true")
	}
	if got.Platform() != connector.PlatformYouTube {
		t.Fatalf("Get(youtube): platform = %q, want youtube", got.Platform())
	}
}

func TestRegistryGetUnknown(t *testing.T) {
	r := connector.NewRegistry()
	if _, ok := r.Get(connector.PlatformTikTok); ok {
		t.Fatal("Get(tiktok) on empty registry: ok = true, want false")
	}
}

func TestRegistryDuplicateIsError(t *testing.T) {
	r := connector.NewRegistry()
	first := stubConnector{platform: connector.PlatformInstagram}
	if err := r.Register(first); err != nil {
		t.Fatalf("first Register: unexpected error: %v", err)
	}

	err := r.Register(stubConnector{platform: connector.PlatformInstagram})
	if err == nil {
		t.Fatal("duplicate Register: err = nil, want conflict error")
	}
	if errs.KindOf(err) != errs.KindConflict {
		t.Fatalf("duplicate Register: kind = %v, want KindConflict", errs.KindOf(err))
	}

	// The original registration must survive; duplicate registration must not
	// overwrite it.
	got, ok := r.Get(connector.PlatformInstagram)
	if !ok || got.Platform() != connector.PlatformInstagram {
		t.Fatal("original connector was lost after a rejected duplicate registration")
	}
}

func TestRegisterNilIsError(t *testing.T) {
	r := connector.NewRegistry()
	err := r.Register(nil)
	if err == nil {
		t.Fatal("Register(nil): err = nil, want invalid error")
	}
	if errs.KindOf(err) != errs.KindInvalid {
		t.Fatalf("Register(nil): kind = %v, want KindInvalid", errs.KindOf(err))
	}
}

func TestRegistryListDeterministic(t *testing.T) {
	r := connector.NewRegistry()
	// Register out of sorted order to prove List sorts rather than preserving
	// insertion order.
	for _, p := range []connector.Platform{
		connector.PlatformYouTube,
		connector.PlatformFacebook,
		connector.PlatformInstagram,
	} {
		if err := r.Register(stubConnector{platform: p}); err != nil {
			t.Fatalf("Register(%q): %v", p, err)
		}
	}

	want := []connector.Platform{
		connector.PlatformFacebook,
		connector.PlatformInstagram,
		connector.PlatformYouTube,
	}
	if got := r.List(); !reflect.DeepEqual(got, want) {
		t.Fatalf("List() = %v, want %v", got, want)
	}
}

func TestRegistryConcurrentReads(t *testing.T) {
	r := connector.NewRegistry()
	if err := r.Register(stubConnector{platform: connector.PlatformYouTube}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	// Concurrent reads after construction must be data-race free; run under
	// `go test -race` to make this assertion meaningful.
	const readers = 16
	done := make(chan struct{})
	for i := 0; i < readers; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			if _, ok := r.Get(connector.PlatformYouTube); !ok {
				t.Error("concurrent Get(youtube): ok = false")
			}
			_ = r.List()
		}()
	}
	for i := 0; i < readers; i++ {
		<-done
	}
}

// TestErrorsClassifyThroughErrs pins the contract that both connector error
// types map onto the shared error vocabulary and remain recoverable via
// errors.As so the orchestrator can read RetryAfter / ResetAt.
func TestErrorsClassifyThroughErrs(t *testing.T) {
	rl := connector.NewRateLimitError(connector.PlatformYouTube, 42, errors.New("429 from upstream"))
	if errs.KindOf(rl) != errs.KindRateLimited {
		t.Fatalf("RateLimitError kind = %v, want KindRateLimited", errs.KindOf(rl))
	}
	var asRL *connector.RateLimitError
	if !errors.As(error(rl), &asRL) || asRL.RetryAfter != 42 {
		t.Fatalf("errors.As(*RateLimitError) failed or RetryAfter lost: %+v", asRL)
	}

	q := connector.NewQuotaExhaustedError(connector.PlatformInstagram, time.Time{}, nil)
	if errs.KindOf(q) != errs.KindQuotaExceeded {
		t.Fatalf("QuotaExhaustedError kind = %v, want KindQuotaExceeded", errs.KindOf(q))
	}
	var asQ *connector.QuotaExhaustedError
	if !errors.As(error(q), &asQ) {
		t.Fatal("errors.As(*QuotaExhaustedError) failed")
	}
}
