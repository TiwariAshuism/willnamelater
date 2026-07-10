package repository

import (
	"encoding/base64"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// cursor is a keyset position: the (created_at, id) of the last row a client
// has already seen. The next page is everything ordering strictly after it.
//
// Keyset pagination is used instead of LIMIT/OFFSET because OFFSET makes the
// database walk and discard every skipped row (cost grows with page depth) and
// silently shifts rows when concurrent inserts change earlier pages. A keyset
// seeks directly to the position with a single range predicate on
// (created_at, id), giving stable pages and depth-independent cost.
type cursor struct {
	createdAt time.Time
	id        uuid.UUID
}

// encodeCursor renders c as an opaque, URL-safe token. The token is not signed:
// it encodes only a public ordering position, so tampering can at worst request
// a differently-positioned page of the same data.
func encodeCursor(c cursor) string {
	raw := strconv.FormatInt(c.createdAt.UnixNano(), 10) + "|" + c.id.String()
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// decodeCursor parses a token produced by encodeCursor. A malformed token is a
// client mistake, so it maps to errs.KindInvalid rather than a 500.
func decodeCursor(token string) (cursor, error) {
	invalid := errs.New(errs.KindInvalid, "influencer.cursor_invalid", "pagination cursor is malformed")

	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return cursor{}, invalid
	}

	nanos, idPart, ok := strings.Cut(string(decoded), "|")
	if !ok {
		return cursor{}, invalid
	}

	unixNano, err := strconv.ParseInt(nanos, 10, 64)
	if err != nil {
		return cursor{}, invalid
	}

	id, err := uuid.Parse(idPart)
	if err != nil {
		return cursor{}, invalid
	}

	return cursor{createdAt: time.Unix(0, unixNano).UTC(), id: id}, nil
}
