// Package parser turns a creator's own Instagram Insights CSV export into
// connector.Post values. It is the real-data path for Instagram while the Meta
// API grant is still pending: rather than fabricate metrics, we let a creator
// hand us the numbers Instagram already showed them.
//
// The parser never invents data. A row it cannot fully understand is rejected
// with a row-numbered error, not silently defaulted to zero — a fabricated
// "0 likes" post would corrupt every downstream authenticity signal, so we
// refuse the upload instead.
package parser

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/getnyx/influaudit/backend/internal/connector"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// maxDataRows bounds an upload so a hostile or accidental multi-gigabyte file
// cannot exhaust memory. A creator auditing their own account has at most a few
// thousand posts; 100k is far above any legitimate export.
const maxDataRows = 100000

// Recognized header column names. Real exports differ in column order and in
// which optional metrics they include, so we map by name (case-insensitive,
// trimmed) rather than by position.
const (
	colPostID      = "post_id"
	colPublishedAt = "published_at"
	colLikes       = "likes"
	colComments    = "comments"
	colCaption     = "caption"
	colShares      = "shares"
	colViews       = "views"
	colURL         = "url"
)

// requiredColumns must be present in the header for the upload to be parseable.
// Their cells may still be empty (a post can genuinely have zero likes); what
// we insist on is that the creator's export actually reports these dimensions.
var requiredColumns = []string{colPostID, colPublishedAt, colLikes, colComments}

// dateLayouts are tried in order against each published_at cell. Instagram's
// own exports and the spreadsheet tools creators re-save them through disagree
// on format, so we accept the common variants rather than force one on them.
var dateLayouts = []string{
	time.RFC3339,
	"2006-01-02T15:04:05",
	"2006-01-02",
	"01/02/2006",
}

// ParseInstagramPostsCSV reads a creator-supplied Instagram posts CSV and
// returns the posts in file order. The reader is consumed fully. A header row
// naming the columns is required; column order is irrelevant and unknown
// columns are ignored, so exports from different tools all parse.
//
// An empty or header-only input is a valid, empty upload: it returns an empty
// slice and a nil error. Any structural problem (missing required column,
// unparseable row) returns an errs.KindInvalid error so the transport layer
// renders it as a 400 rather than a 500.
func ParseInstagramPostsCSV(r io.Reader) ([]connector.Post, error) {
	cr := csv.NewReader(r)
	// Column count legitimately varies across rows of real-world exports (a
	// trailing empty metric column, say), so we do not enforce a fixed width;
	// we validate the cells we actually read instead.
	cr.FieldsPerRecord = -1

	header, err := cr.Read()
	if errors.Is(err, io.EOF) {
		// Completely empty input is an empty upload, not an error.
		return []connector.Post{}, nil
	}
	if err != nil {
		return nil, errs.Wrap(err, errs.KindInvalid, "dataimport.csv_bad_row", "row 1: unreadable header")
	}

	// index maps a normalized column name to its position in each row.
	index := make(map[string]int, len(header))
	for i, name := range header {
		index[normalize(name)] = i
	}
	for _, col := range requiredColumns {
		if _, ok := index[col]; !ok {
			return nil, errs.New(errs.KindInvalid, "dataimport.csv_missing_column",
				fmt.Sprintf("missing required column %q", col))
		}
	}

	posts := make([]connector.Post, 0)
	// line tracks the 1-based file line for error messages: the header is line 1,
	// so the first data row is line 2.
	line := 1
	for {
		record, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, errs.Wrap(err, errs.KindInvalid, "dataimport.csv_bad_row",
				fmt.Sprintf("row %d: unreadable", line+1))
		}
		line++

		if len(posts) >= maxDataRows {
			return nil, errs.New(errs.KindInvalid, "dataimport.csv_too_large",
				fmt.Sprintf("upload exceeds the %d-row limit", maxDataRows))
		}

		post, err := parseRow(index, record, line)
		if err != nil {
			return nil, err
		}
		posts = append(posts, post)
	}

	return posts, nil
}

// parseRow builds one Post from a data record. line is the 1-based file line of
// this record, used verbatim in row errors so a creator can find the offending
// line in their spreadsheet.
func parseRow(index map[string]int, record []string, line int) (connector.Post, error) {
	id := field(index, record, colPostID)
	if id == "" {
		return connector.Post{}, rowErr(line, "post_id is empty")
	}

	publishedAt, err := parseTime(field(index, record, colPublishedAt))
	if err != nil {
		return connector.Post{}, rowErr(line, err.Error())
	}

	likes, err := parseCount(field(index, record, colLikes), colLikes)
	if err != nil {
		return connector.Post{}, rowErr(line, err.Error())
	}
	comments, err := parseCount(field(index, record, colComments), colComments)
	if err != nil {
		return connector.Post{}, rowErr(line, err.Error())
	}
	shares, err := parseCount(field(index, record, colShares), colShares)
	if err != nil {
		return connector.Post{}, rowErr(line, err.Error())
	}
	views, err := parseCount(field(index, record, colViews), colViews)
	if err != nil {
		return connector.Post{}, rowErr(line, err.Error())
	}

	return connector.Post{
		ID:          id,
		URL:         field(index, record, colURL),
		PublishedAt: publishedAt,
		Caption:     field(index, record, colCaption),
		Likes:       likes,
		Comments:    comments,
		Shares:      shares,
		Views:       views,
	}, nil
}

// field returns the trimmed cell for a named column, or "" when the column is
// absent from the header or the row is short. Absent-and-empty are treated
// alike because both mean "the export did not report this value for this post".
func field(index map[string]int, record []string, col string) string {
	i, ok := index[col]
	if !ok || i >= len(record) {
		return ""
	}
	return strings.TrimSpace(record[i])
}

// parseTime tries each accepted layout in turn and normalizes to UTC so all
// stored timestamps are comparable regardless of the export's local zone.
func parseTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, fmt.Errorf("published_at is empty")
	}
	for _, layout := range dateLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("published_at %q is not a recognized date", s)
}

// parseCount reads an engagement counter. An empty cell means zero — a post can
// legitimately have no likes — but a present, non-numeric cell is a real defect
// we surface rather than coerce, since coercing would fabricate a metric.
func parseCount(s, col string) (int64, error) {
	if s == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s %q is not a number", col, s)
	}
	return n, nil
}

// normalize canonicalizes a header cell for name-based matching: trimmed and
// lowercased, so "Post_ID" and " likes " match the constants above.
func normalize(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// rowErr builds a row-numbered KindInvalid error with a consistent prefix.
func rowErr(line int, reason string) error {
	return errs.New(errs.KindInvalid, "dataimport.csv_bad_row",
		fmt.Sprintf("row %d: %s", line, reason))
}
