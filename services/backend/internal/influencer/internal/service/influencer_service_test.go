package service

import (
	"context"
	"testing"

	"github.com/getnyx/influaudit/backend/internal/influencer/internal/model"
	"github.com/getnyx/influaudit/backend/internal/influencer/internal/repository"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// fakeRepo is an in-memory InfluencerRepository that records the last call it
// received and returns preconfigured values, so a service test can assert both
// what the service forwarded and how it maps repository errors.
type fakeRepo struct {
	called bool

	createReq model.CreateInfluencerRequest
	getID     string
	listReq   model.ListInfluencersRequest
	updateID  string
	updateReq model.UpdateInfluencerRequest
	addID     string
	addReq    model.AddHandleRequest
	delID     string
	delHandle string

	err        error
	infResp    model.InfluencerResponse
	listResp   model.ListInfluencersResponse
	handleResp model.HandleResponse
}

var _ repository.InfluencerRepository = (*fakeRepo)(nil)

func (f *fakeRepo) CreateInfluencer(_ context.Context, req model.CreateInfluencerRequest) (model.InfluencerResponse, error) {
	f.called, f.createReq = true, req
	return f.infResp, f.err
}

func (f *fakeRepo) GetInfluencer(_ context.Context, id string) (model.InfluencerResponse, error) {
	f.called, f.getID = true, id
	return f.infResp, f.err
}

func (f *fakeRepo) ListInfluencers(_ context.Context, req model.ListInfluencersRequest) (model.ListInfluencersResponse, error) {
	f.called, f.listReq = true, req
	return f.listResp, f.err
}

func (f *fakeRepo) UpdateInfluencer(_ context.Context, id string, req model.UpdateInfluencerRequest) (model.InfluencerResponse, error) {
	f.called, f.updateID, f.updateReq = true, id, req
	return f.infResp, f.err
}

func (f *fakeRepo) AddHandle(_ context.Context, id string, req model.AddHandleRequest) (model.HandleResponse, error) {
	f.called, f.addID, f.addReq = true, id, req
	return f.handleResp, f.err
}

func (f *fakeRepo) DeleteHandle(_ context.Context, id string, handleID string) error {
	f.called, f.delID, f.delHandle = true, id, handleID
	return f.err
}

const validID = "6f9619ff-8b86-d011-b42d-00cf4fc964ff"

func strptr(s string) *string { return &s }

func TestCreateInfluencerValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		req        model.CreateInfluencerRequest
		wantErr    errs.Kind
		wantCalled bool
	}{
		{
			name:       "valid with normalized country",
			req:        model.CreateInfluencerRequest{Niche: strptr("beauty"), Country: strptr("us")},
			wantCalled: true,
		},
		{
			name:    "unknown niche rejected before repo",
			req:     model.CreateInfluencerRequest{Niche: strptr("crypto")},
			wantErr: errs.KindInvalid,
		},
		{
			name:    "bad country rejected",
			req:     model.CreateInfluencerRequest{Country: strptr("usa")},
			wantErr: errs.KindInvalid,
		},
		{
			name:       "all fields omitted is allowed",
			req:        model.CreateInfluencerRequest{},
			wantCalled: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			repo := &fakeRepo{}
			svc := New(repo)

			_, err := svc.CreateInfluencer(context.Background(), tt.req)

			if tt.wantErr == errs.KindInvalid {
				if errs.KindOf(err) != errs.KindInvalid {
					t.Fatalf("kind = %v, want KindInvalid", errs.KindOf(err))
				}
				if repo.called {
					t.Fatal("repository was called despite invalid input")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if repo.called != tt.wantCalled {
				t.Fatalf("repo called = %v, want %v", repo.called, tt.wantCalled)
			}
			if tt.req.Country != nil && repo.createReq.Country != nil && *repo.createReq.Country != "US" {
				t.Fatalf("country not upper-cased: got %q", *repo.createReq.Country)
			}
		})
	}
}

func TestListInfluencersLimitClamp(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		requested int
		want      int
	}{
		{name: "zero defaults", requested: 0, want: defaultListLimit},
		{name: "negative defaults", requested: -5, want: defaultListLimit},
		{name: "within range preserved", requested: 50, want: 50},
		{name: "over cap clamped", requested: 5_000, want: maxListLimit},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			repo := &fakeRepo{}
			svc := New(repo)

			if _, err := svc.ListInfluencers(context.Background(), model.ListInfluencersRequest{Limit: tt.requested}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if repo.listReq.Limit != tt.want {
				t.Fatalf("forwarded limit = %d, want %d", repo.listReq.Limit, tt.want)
			}
		})
	}
}

func TestListInfluencersFilterValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		req  model.ListInfluencersRequest
	}{
		{name: "bad niche filter", req: model.ListInfluencersRequest{Niche: strptr("crypto")}},
		{name: "bad tier filter", req: model.ListInfluencersRequest{Tier: strptr("gigantic")}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			repo := &fakeRepo{}
			svc := New(repo)

			_, err := svc.ListInfluencers(context.Background(), tt.req)
			if errs.KindOf(err) != errs.KindInvalid {
				t.Fatalf("kind = %v, want KindInvalid", errs.KindOf(err))
			}
			if repo.called {
				t.Fatal("repository was called despite invalid filter")
			}
		})
	}
}

func TestAddHandleValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		id         string
		req        model.AddHandleRequest
		wantCalled bool
	}{
		{
			name:       "valid trims handle",
			id:         validID,
			req:        model.AddHandleRequest{Platform: "youtube", Handle: "  creator  "},
			wantCalled: true,
		},
		{
			name: "invalid influencer id",
			id:   "not-a-uuid",
			req:  model.AddHandleRequest{Platform: "youtube", Handle: "creator"},
		},
		{
			name: "unknown platform",
			id:   validID,
			req:  model.AddHandleRequest{Platform: "myspace", Handle: "creator"},
		},
		{
			name: "blank handle",
			id:   validID,
			req:  model.AddHandleRequest{Platform: "youtube", Handle: "   "},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			repo := &fakeRepo{}
			svc := New(repo)

			_, err := svc.AddHandle(context.Background(), tt.id, tt.req)

			if !tt.wantCalled {
				if errs.KindOf(err) != errs.KindInvalid {
					t.Fatalf("kind = %v, want KindInvalid", errs.KindOf(err))
				}
				if repo.called {
					t.Fatal("repository was called despite invalid input")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if repo.addReq.Handle != "creator" {
				t.Fatalf("handle not trimmed: got %q", repo.addReq.Handle)
			}
		})
	}
}

func TestIDValidation(t *testing.T) {
	t.Parallel()

	repo := &fakeRepo{}
	svc := New(repo)

	if _, err := svc.GetInfluencer(context.Background(), "bad"); errs.KindOf(err) != errs.KindInvalid {
		t.Fatalf("GetInfluencer kind = %v, want KindInvalid", errs.KindOf(err))
	}
	if err := svc.DeleteHandle(context.Background(), validID, "bad"); errs.KindOf(err) != errs.KindInvalid {
		t.Fatalf("DeleteHandle handle-id kind = %v, want KindInvalid", errs.KindOf(err))
	}
	if repo.called {
		t.Fatal("repository was called despite invalid identifiers")
	}
}

func TestServicePropagatesRepositoryError(t *testing.T) {
	t.Parallel()

	sentinel := errs.New(errs.KindConflict, "influencer.handle_conflict", "already exists")
	repo := &fakeRepo{err: sentinel}
	svc := New(repo)

	_, err := svc.AddHandle(context.Background(), validID, model.AddHandleRequest{Platform: "youtube", Handle: "creator"})
	if errs.KindOf(err) != errs.KindConflict {
		t.Fatalf("kind = %v, want KindConflict", errs.KindOf(err))
	}
}
