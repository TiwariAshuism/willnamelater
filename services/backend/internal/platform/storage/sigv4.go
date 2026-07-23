package storage

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"time"
)

// header is a single canonical header or query parameter (name, value) pair.
type header struct {
	name  string
	value string
}

// hmacSHA256 returns HMAC-SHA256(key, data).
func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

// sha256Hex returns the lowercase hex SHA-256 of b.
func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// deriveSigningKey computes the SigV4 signing key by the documented HMAC chain:
// kSecret -> kDate -> kRegion -> kService -> kSigning.
func deriveSigningKey(secret, dateStamp, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secret), []byte(dateStamp))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte(service))
	return hmacSHA256(kService, []byte(terminator))
}

// sign derives the signing key for the given date and returns the hex signature
// of stringToSign. It is the single place the secret key is used.
func (c *Client) sign(dateStamp, stringToSign string) string {
	key := deriveSigningKey(c.secretKey, dateStamp, c.region, service)
	return hex.EncodeToString(hmacSHA256(key, []byte(stringToSign)))
}

// authorization builds a complete SigV4 Authorization header value. headers
// must already be sorted by lowercase name; payloadHash is the hex body hash.
func (c *Client) authorization(method, canonicalURI, query string, headers []header, payloadHash string, now time.Time) string {
	amzDate := now.Format(iso8601)
	dateStamp := now.Format(yyyymmdd)

	signed := signedHeaders(headers)
	canonicalRequest := method + "\n" +
		canonicalURI + "\n" +
		query + "\n" +
		canonicalHeaders(headers) + "\n" +
		signed + "\n" +
		payloadHash

	scope := dateStamp + "/" + c.region + "/" + service + "/" + terminator
	stringToSign := algorithm + "\n" + amzDate + "\n" + scope + "\n" + sha256Hex([]byte(canonicalRequest))
	signature := c.sign(dateStamp, stringToSign)

	return algorithm +
		" Credential=" + c.accessKey + "/" + scope +
		", SignedHeaders=" + signed +
		", Signature=" + signature
}

// canonicalHeaders renders headers as "name:value\n" lines. Each name is
// assumed lowercase and each value already trimmed.
func canonicalHeaders(headers []header) string {
	var b strings.Builder
	for _, h := range headers {
		b.WriteString(h.name)
		b.WriteByte(':')
		b.WriteString(h.value)
		b.WriteByte('\n')
	}
	return b.String()
}

// signedHeaders renders the semicolon-joined list of header names.
func signedHeaders(headers []header) string {
	names := make([]string, len(headers))
	for i, h := range headers {
		names[i] = h.name
	}
	return strings.Join(names, ";")
}

// canonicalQuery renders params as an ampersand-joined, key-sorted query string
// with both keys and values fully URI-encoded (slashes escaped).
func canonicalQuery(params []header) string {
	encoded := make([]string, len(params))
	for i, p := range params {
		encoded[i] = uriEncode(p.name, true) + "=" + uriEncode(p.value, true)
	}
	sort.Strings(encoded)
	return strings.Join(encoded, "&")
}

// uriEncode percent-encodes s per the SigV4 rules: every byte is escaped except
// the unreserved set A-Z a-z 0-9 - . _ ~. When encodeSlash is false, '/' is
// left literal so it can separate path segments.
func uriEncode(s string, encodeSlash bool) string {
	const upperhex = "0123456789ABCDEF"
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case ch >= 'A' && ch <= 'Z',
			ch >= 'a' && ch <= 'z',
			ch >= '0' && ch <= '9',
			ch == '-', ch == '.', ch == '_', ch == '~':
			b.WriteByte(ch)
		case ch == '/' && !encodeSlash:
			b.WriteByte(ch)
		default:
			b.WriteByte('%')
			b.WriteByte(upperhex[ch>>4])
			b.WriteByte(upperhex[ch&0x0f])
		}
	}
	return b.String()
}
