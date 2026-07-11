// Package storage is a minimal, stdlib-only client for S3-compatible object
// stores. It targets LocalStack in dev (path-style addressing against an
// explicit endpoint) and any S3-compatible store in prod.
//
// The report module depends on this small surface rather than on an AWS SDK:
// the wire format (SigV4-signed PUT, presigned GET URLs) lives here in one
// reviewable place, and AWS Signature Version 4 is implemented by hand over the
// standard library — matching how the rest of this codebase hand-rolls its
// crypto (internal/platform/crypto) and HTTP clients (internal/platform/pdf).
//
// Signing is a pure function of its inputs: the Client takes an injectable
// clock so a request signed at a fixed time is byte-for-byte reproducible,
// which is what makes the SigV4 known-answer tests possible.
package storage

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// maxErrorBody bounds how much of a non-2xx response we read for diagnostics, so
// a misbehaving store cannot make us buffer an unbounded body.
const maxErrorBody = 8 << 10

const (
	algorithm     = "AWS4-HMAC-SHA256"
	service       = "s3"
	terminator    = "aws4_request"
	unsignedPayer = "UNSIGNED-PAYLOAD"
	iso8601       = "20060102T150405Z"
	yyyymmdd      = "20060102"
	emptyBodyHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
)

// Doer is the injectable HTTP client seam: the subset of *http.Client this
// package needs, so tests can supply a stub that never touches the network.
// *http.Client satisfies it.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// Config carries the plain fields needed to address and sign against a bucket.
// It deliberately does not import config.StorageConfig: the composition root
// translates that struct into these fields, so this package stays free of
// configuration concerns.
type Config struct {
	Endpoint  string // e.g. "http://localhost:4566" (no trailing slash). Required.
	Region    string // e.g. "us-east-1". Required.
	Bucket    string // Required.
	AccessKey string // Required.
	SecretKey string // Required.
	HTTP      Doer   // injected; required.
	// PathStyle forces path-style addressing (http://endpoint/bucket/key).
	// Default true (LocalStack/MinIO need it); virtual-host style otherwise.
	PathStyle bool
}

// Client signs and issues object-storage requests for a single bucket.
type Client struct {
	scheme    string
	host      string
	region    string
	bucket    string
	accessKey string
	secretKey string
	pathStyle bool
	http      Doer
	now       func() time.Time
}

// New validates cfg and builds a Client. All string fields and the HTTP Doer
// are required; a missing or malformed field is a KindInvalid error. The clock
// defaults to time.Now and is overridable by tests via the returned Client.
func New(cfg Config) (*Client, error) {
	switch {
	case cfg.Endpoint == "":
		return nil, errs.New(errs.KindInvalid, "storage.config", "storage endpoint is required")
	case cfg.Region == "":
		return nil, errs.New(errs.KindInvalid, "storage.config", "storage region is required")
	case cfg.Bucket == "":
		return nil, errs.New(errs.KindInvalid, "storage.config", "storage bucket is required")
	case cfg.AccessKey == "":
		return nil, errs.New(errs.KindInvalid, "storage.config", "storage access key is required")
	case cfg.SecretKey == "":
		return nil, errs.New(errs.KindInvalid, "storage.config", "storage secret key is required")
	case cfg.HTTP == nil:
		return nil, errs.New(errs.KindInvalid, "storage.config", "storage HTTP client is required")
	}

	u, err := url.Parse(cfg.Endpoint)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, errs.New(errs.KindInvalid, "storage.config", "storage endpoint must be an absolute URL")
	}

	return &Client{
		scheme:    u.Scheme,
		host:      u.Host,
		region:    cfg.Region,
		bucket:    cfg.Bucket,
		accessKey: cfg.AccessKey,
		secretKey: cfg.SecretKey,
		pathStyle: cfg.PathStyle,
		http:      cfg.HTTP,
		now:       time.Now,
	}, nil
}

// hostFor returns the Host header value for a request, which differs between
// path-style (endpoint host) and virtual-host style (bucket-prefixed host).
func (c *Client) hostFor() string {
	if c.pathStyle {
		return c.host
	}
	return c.bucket + "." + c.host
}

// canonicalURIFor returns the SigV4 canonical (percent-encoded) path for an
// object key. Path-style prepends the bucket segment; virtual-host style does
// not, since the bucket is carried in the host.
func (c *Client) canonicalURIFor(key string) string {
	if c.pathStyle {
		return "/" + uriEncode(c.bucket, true) + "/" + uriEncode(key, false)
	}
	return "/" + uriEncode(key, false)
}

// PutObject stores data under key with the given content type and returns the
// object's ETag. The request is SigV4-signed with the hex SHA-256 of the body
// as its payload hash. Transport failures and non-2xx responses surface as
// KindUnavailable (the store is a retryable dependency); the S3 error <Code> is
// included when present. The access key never appears in a returned error.
func (c *Client) PutObject(ctx context.Context, key, contentType string, data []byte) (string, error) {
	now := c.now().UTC()
	host := c.hostFor()
	canonicalURI := c.canonicalURIFor(key)
	payloadHash := sha256Hex(data)
	length := strconv.Itoa(len(data))

	// Canonical headers must be sorted by lowercase name.
	headers := []header{
		{"content-length", length},
		{"content-type", contentType},
		{"host", host},
		{"x-amz-content-sha256", payloadHash},
		{"x-amz-date", now.Format(iso8601)},
	}
	authorization := c.authorization(http.MethodPut, canonicalURI, "", headers, payloadHash, now)

	endpoint := c.scheme + "://" + host + canonicalURI
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, bytes.NewReader(data))
	if err != nil {
		return "", errs.Wrap(err, errs.KindInternal, "storage.request", "the storage request could not be assembled")
	}
	req.ContentLength = int64(len(data))
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	req.Header.Set("X-Amz-Date", now.Format(iso8601))
	req.Header.Set("Authorization", authorization)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", errs.Wrap(err, errs.KindUnavailable, "storage.put", "the object store could not be reached")
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		return "", errs.New(errs.KindUnavailable, "storage.put", "the object store rejected the upload: "+describe(resp.StatusCode, body))
	}
	return strings.Trim(resp.Header.Get("ETag"), `"`), nil
}

// EnsureBucket idempotently creates the bucket. It is called once at boot so a
// dev store does not 404 on first upload. A 2xx (created) or 409 (already
// exists / already owned) is success; anything else is KindUnavailable.
func (c *Client) EnsureBucket(ctx context.Context) error {
	now := c.now().UTC()
	host := c.hostFor()
	canonicalURI := "/" + uriEncode(c.bucket, true)
	if !c.pathStyle {
		// Virtual-host style addresses the bucket at the root of its host.
		canonicalURI = "/"
	}

	headers := []header{
		{"host", host},
		{"x-amz-content-sha256", emptyBodyHash},
		{"x-amz-date", now.Format(iso8601)},
	}
	authorization := c.authorization(http.MethodPut, canonicalURI, "", headers, emptyBodyHash, now)

	endpoint := c.scheme + "://" + host + canonicalURI
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, http.NoBody)
	if err != nil {
		return errs.Wrap(err, errs.KindInternal, "storage.request", "the storage request could not be assembled")
	}
	req.ContentLength = 0
	req.Header.Set("X-Amz-Content-Sha256", emptyBodyHash)
	req.Header.Set("X-Amz-Date", now.Format(iso8601))
	req.Header.Set("Authorization", authorization)

	resp, err := c.http.Do(req)
	if err != nil {
		return errs.Wrap(err, errs.KindUnavailable, "storage.ensure_bucket", "the object store could not be reached")
	}
	defer func() { _ = resp.Body.Close() }()

	if (resp.StatusCode >= 200 && resp.StatusCode < 300) || resp.StatusCode == http.StatusConflict {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	return errs.New(errs.KindUnavailable, "storage.ensure_bucket", "the object store rejected bucket creation: "+describe(resp.StatusCode, body))
}

// PresignGetURL returns a SigV4 query-string-signed GET URL for key that is
// valid for ttl. It performs no I/O: the URL is a pure function of the client's
// credentials, the key, ttl, and the injected clock, so it is deterministic for
// a fixed clock. The payload hash is the literal UNSIGNED-PAYLOAD, as required
// for presigned URLs.
func (c *Client) PresignGetURL(key string, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		return "", errs.New(errs.KindInvalid, "storage.presign", "presign ttl must be positive")
	}
	now := c.now().UTC()
	host := c.hostFor()
	canonicalURI := c.canonicalURIFor(key)
	amzDate := now.Format(iso8601)
	dateStamp := now.Format(yyyymmdd)
	scope := dateStamp + "/" + c.region + "/" + service + "/" + terminator

	// Query parameters are canonicalized sorted by encoded key; every value is
	// fully encoded, so the slashes in the credential scope become %2F.
	query := canonicalQuery([]header{
		{"X-Amz-Algorithm", algorithm},
		{"X-Amz-Credential", c.accessKey + "/" + scope},
		{"X-Amz-Date", amzDate},
		{"X-Amz-Expires", strconv.FormatInt(int64(ttl/time.Second), 10)},
		{"X-Amz-SignedHeaders", "host"},
	})

	headers := []header{{"host", host}}
	canonicalRequest := http.MethodGet + "\n" +
		canonicalURI + "\n" +
		query + "\n" +
		canonicalHeaders(headers) + "\n" +
		"host" + "\n" +
		unsignedPayer
	stringToSign := algorithm + "\n" + amzDate + "\n" + scope + "\n" + sha256Hex([]byte(canonicalRequest))
	signature := c.sign(dateStamp, stringToSign)

	return c.scheme + "://" + host + canonicalURI + "?" + query + "&X-Amz-Signature=" + signature, nil
}

// describe renders a status code plus any S3 <Code> from the body into a
// client-safe fragment. It never includes credentials.
func describe(status int, body []byte) string {
	if code := s3ErrorCode(body); code != "" {
		return "status " + strconv.Itoa(status) + " (" + code + ")"
	}
	return "status " + strconv.Itoa(status)
}

// s3ErrorCode extracts the contents of the first <Code> element in an S3 XML
// error body, or "" if absent.
func s3ErrorCode(body []byte) string {
	s := string(body)
	start := strings.Index(s, "<Code>")
	if start < 0 {
		return ""
	}
	start += len("<Code>")
	end := strings.Index(s[start:], "</Code>")
	if end < 0 {
		return ""
	}
	return s[start : start+end]
}
