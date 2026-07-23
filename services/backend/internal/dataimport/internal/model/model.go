// Package model holds the dataimport module's request/response DTOs and the
// normalized dataset it persists. A dataset is a creator's own uploaded platform
// data, normalized into the connector's snapshot vocabulary so the csvimport
// connector can serve it back at audit time.
package model

import (
	"time"

	"github.com/getnyx/influaudit/backend/internal/connector"
)

// ImportRequest is a creator's upload of their own Instagram data. The CSV text
// rides in a JSON field rather than as a multipart file so the endpoint stays a
// clean JSON contract; a future structured-JSON format adds its own field beside
// PostsCSV without changing the envelope.
type ImportRequest struct {
	// InfluencerID is the profile the upload is for; the upload is stored against
	// it and the audit of that profile serves it.
	InfluencerID string `json:"influencer_id" binding:"required,uuid"`
	// Handle is the public Instagram handle the data belongs to. The csvimport
	// connector resolves an audit's data by (platform, handle), so this must match
	// the handle the influencer's Instagram connection carries.
	Handle string `json:"handle" binding:"required"`
	// Followers is the current follower count from the export. It seeds the
	// snapshot's reach and a single follower metric point.
	Followers int64 `json:"followers" binding:"gte=0"`
	// PostsCSV is the raw Instagram Insights posts export, as CSV text. Columns
	// are matched by name, so column order does not matter.
	PostsCSV string `json:"posts_csv" binding:"required"`
}

// ImportResponse acknowledges a stored upload.
type ImportResponse struct {
	DatasetID string `json:"dataset_id"`
	Platform  string `json:"platform"`
	Handle    string `json:"handle"`
	// Posts is how many post rows were parsed and stored, so the caller can see
	// their upload was understood.
	Posts int `json:"posts"`
}

// Dataset is a normalized upload: the creator's data expressed in the connector's
// own types, ready to be stored as JSON and read straight back by the connector.
type Dataset struct {
	Handle     string
	Followers  int64
	Source     string
	CapturedAt time.Time
	Posts      []connector.Post
	Metrics    []connector.MetricPoint
	Comments   []connector.Comment
}
