// Package pdf wraps Gotenberg's Chromium HTML-to-PDF route so the report module
// depends on this small interface rather than on Gotenberg directly.
//
// Isolating the dependency here keeps the wire format (multipart form, the
// mandatory index.html field name, the convert endpoint) in one reviewable
// place and lets callers inject a fake HTTP client in tests instead of standing
// up a renderer.
package pdf

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// maxErrorBody bounds how much of a non-2xx response we read for diagnostics, so
// a misbehaving renderer cannot make us buffer an unbounded body.
const maxErrorBody = 8 << 10

// Doer is the injectable HTTP client seam. It is the subset of *http.Client that
// this package needs, so tests can supply a stub that never touches the network.
// *http.Client satisfies it.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// Client renders HTML to PDF through a Gotenberg instance.
type Client struct {
	baseURL string
	doer    Doer
}

// New builds a Client targeting the Gotenberg instance at baseURL. A nil doer
// falls back to a default *http.Client. Any trailing slash on baseURL is trimmed
// so route joining stays predictable.
func New(baseURL string, doer Doer) *Client {
	if doer == nil {
		doer = &http.Client{}
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		doer:    doer,
	}
}

// RenderHTML converts a single HTML document to PDF and returns the PDF bytes.
//
// The document is sent as a multipart form with one file part whose field name
// and filename are both index.html, which Gotenberg requires as the entrypoint.
// Transport failures and non-2xx responses surface as KindUnavailable errors,
// since the renderer is a retryable dependency.
func (c *Client) RenderHTML(ctx context.Context, html []byte) ([]byte, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	part, err := writer.CreateFormFile("index.html", "index.html")
	if err != nil {
		return nil, errs.Wrap(err, errs.KindInternal, "pdf.form_failed", "the PDF request could not be assembled")
	}
	if _, err := part.Write(html); err != nil {
		return nil, errs.Wrap(err, errs.KindInternal, "pdf.form_failed", "the PDF request could not be assembled")
	}
	if err := writer.Close(); err != nil {
		return nil, errs.Wrap(err, errs.KindInternal, "pdf.form_failed", "the PDF request could not be assembled")
	}

	url := c.baseURL + "/forms/chromium/convert/html"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &body)
	if err != nil {
		return nil, errs.Wrap(err, errs.KindInternal, "pdf.request_failed", "the PDF request could not be assembled")
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := c.doer.Do(req)
	if err != nil {
		return nil, errs.Wrap(err, errs.KindUnavailable, "pdf.request_failed", "the PDF renderer could not be reached")
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Drain a bounded slice of the body so the failure is logged with
		// context, then report a stable, client-safe message.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxErrorBody))
		return nil, errs.New(errs.KindUnavailable, "pdf.render_failed", "the PDF renderer returned status "+resp.Status)
	}

	pdf, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errs.Wrap(err, errs.KindUnavailable, "pdf.read_failed", "the PDF renderer response could not be read")
	}
	return pdf, nil
}
