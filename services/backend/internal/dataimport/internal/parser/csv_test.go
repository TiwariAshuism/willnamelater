package parser

import (
	"strings"
	"testing"
	"time"

	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

func TestParseInstagramPostsCSV_HappyPath(t *testing.T) {
	// Header is deliberately in a NON-default order with an extra, ignored
	// column ("impressions") to prove name-based mapping.
	in := strings.NewReader(strings.Join([]string{
		"caption,comments,post_id,impressions,likes,published_at,shares,views,url",
		"Hello world,3,p1,999,10,2024-01-02T15:04:05Z,1,100,https://insta/p1",
		"Second post,0,p2,0,20,2024-03-04,2,200,https://insta/p2",
	}, "\n"))

	posts, err := ParseInstagramPostsCSV(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(posts) != 2 {
		t.Fatalf("got %d posts, want 2", len(posts))
	}

	p0 := posts[0]
	if p0.ID != "p1" {
		t.Errorf("ID = %q, want p1", p0.ID)
	}
	if p0.Caption != "Hello world" {
		t.Errorf("Caption = %q, want %q", p0.Caption, "Hello world")
	}
	if p0.URL != "https://insta/p1" {
		t.Errorf("URL = %q", p0.URL)
	}
	if p0.Likes != 10 || p0.Comments != 3 || p0.Shares != 1 || p0.Views != 100 {
		t.Errorf("counters = %d/%d/%d/%d, want 10/3/1/100",
			p0.Likes, p0.Comments, p0.Shares, p0.Views)
	}
	want := time.Date(2024, 1, 2, 15, 4, 5, 0, time.UTC)
	if !p0.PublishedAt.Equal(want) {
		t.Errorf("PublishedAt = %v, want %v", p0.PublishedAt, want)
	}
	if p0.PublishedAt.Location() != time.UTC {
		t.Errorf("PublishedAt not UTC: %v", p0.PublishedAt.Location())
	}

	p1 := posts[1]
	if p1.ID != "p2" || p1.Likes != 20 || p1.Comments != 0 {
		t.Errorf("row 2 mismatch: %+v", p1)
	}
	if got := p1.PublishedAt; !got.Equal(time.Date(2024, 3, 4, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("row 2 PublishedAt = %v", got)
	}
}

func TestParseInstagramPostsCSV_HeaderOnly(t *testing.T) {
	posts, err := ParseInstagramPostsCSV(strings.NewReader("post_id,published_at,likes,comments\n"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(posts) != 0 {
		t.Fatalf("got %d posts, want 0", len(posts))
	}
}

func TestParseInstagramPostsCSV_EmptyInput(t *testing.T) {
	posts, err := ParseInstagramPostsCSV(strings.NewReader(""))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(posts) != 0 {
		t.Fatalf("got %d posts, want 0", len(posts))
	}
}

func TestParseInstagramPostsCSV_MissingRequiredColumn(t *testing.T) {
	// "likes" is required but absent.
	_, err := ParseInstagramPostsCSV(strings.NewReader("post_id,published_at,comments\np1,2024-01-01,3\n"))
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if errs.KindOf(err) != errs.KindInvalid {
		t.Errorf("KindOf = %v, want KindInvalid", errs.KindOf(err))
	}
	if !strings.Contains(err.Error(), "likes") {
		t.Errorf("error should name the missing column: %v", err)
	}
}

func TestParseInstagramPostsCSV_BadNumericLikes(t *testing.T) {
	_, err := ParseInstagramPostsCSV(strings.NewReader(
		"post_id,published_at,likes,comments\np1,2024-01-01,not-a-number,3\n"))
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if errs.KindOf(err) != errs.KindInvalid {
		t.Errorf("KindOf = %v, want KindInvalid", errs.KindOf(err))
	}
	// First data row is line 2.
	if !strings.Contains(err.Error(), "row 2") {
		t.Errorf("error should cite the row number: %v", err)
	}
	if !strings.Contains(err.Error(), "likes") {
		t.Errorf("error should mention the offending column: %v", err)
	}
}

func TestParseInstagramPostsCSV_BadDate(t *testing.T) {
	_, err := ParseInstagramPostsCSV(strings.NewReader(
		"post_id,published_at,likes,comments\np1,nonsense-date,10,3\n"))
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if errs.KindOf(err) != errs.KindInvalid {
		t.Errorf("KindOf = %v, want KindInvalid", errs.KindOf(err))
	}
	if !strings.Contains(err.Error(), "row 2") {
		t.Errorf("error should cite the row number: %v", err)
	}
}

func TestParseInstagramPostsCSV_EmptyPostID(t *testing.T) {
	_, err := ParseInstagramPostsCSV(strings.NewReader(
		"post_id,published_at,likes,comments\n,2024-01-01,10,3\n"))
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if errs.KindOf(err) != errs.KindInvalid {
		t.Errorf("KindOf = %v, want KindInvalid", errs.KindOf(err))
	}
}

func TestParseInstagramPostsCSV_DateFormats(t *testing.T) {
	cases := []struct {
		name string
		cell string
		want time.Time
	}{
		{"RFC3339", "2024-05-06T07:08:09Z", time.Date(2024, 5, 6, 7, 8, 9, 0, time.UTC)},
		{"NoZone", "2024-05-06T07:08:09", time.Date(2024, 5, 6, 7, 8, 9, 0, time.UTC)},
		{"DateOnly", "2024-05-06", time.Date(2024, 5, 6, 0, 0, 0, 0, time.UTC)},
		{"USSlash", "05/06/2024", time.Date(2024, 5, 6, 0, 0, 0, 0, time.UTC)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := "post_id,published_at,likes,comments\np1," + tc.cell + ",1,1\n"
			posts, err := ParseInstagramPostsCSV(strings.NewReader(in))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(posts) != 1 {
				t.Fatalf("got %d posts, want 1", len(posts))
			}
			if !posts[0].PublishedAt.Equal(tc.want) {
				t.Errorf("PublishedAt = %v, want %v", posts[0].PublishedAt, tc.want)
			}
		})
	}
}

func TestParseInstagramPostsCSV_EmptyOptionalNumericIsZero(t *testing.T) {
	// shares and views columns present but with empty cells → 0; likes cell also
	// empty is allowed and means 0.
	in := "post_id,published_at,likes,comments,shares,views\np1,2024-01-01,,5,,\n"
	posts, err := ParseInstagramPostsCSV(strings.NewReader(in))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(posts) != 1 {
		t.Fatalf("got %d posts, want 1", len(posts))
	}
	p := posts[0]
	if p.Likes != 0 || p.Shares != 0 || p.Views != 0 {
		t.Errorf("empty numeric cells should be 0, got likes=%d shares=%d views=%d",
			p.Likes, p.Shares, p.Views)
	}
	if p.Comments != 5 {
		t.Errorf("Comments = %d, want 5", p.Comments)
	}
}
