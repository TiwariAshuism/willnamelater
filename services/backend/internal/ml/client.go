// Package ml is the backend's typed client to services/ml, the Python FastAPI
// service that produces cold-start fraud, engagement-pod, and comment-quality
// estimates. The audit orchestrator calls this client; it is never exposed over
// HTTP itself, so the module has no api/routes.go and no handler.
//
// The request and response shapes in types.go mirror the pydantic contract in
// services/ml/app/schemas.py exactly. The HTTP transport is an injected Doer so
// tests use a fake and never touch the network.
//
// Failure policy is deliberate: a timeout or connection failure maps to
// errs.KindUnavailable so the orchestrator degrades to a PARTIAL audit rather
// than failing the whole job, while a non-2xx response is classified from the
// service's {code, message} envelope onto the shared errs vocabulary.
package ml

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// Endpoint paths on the ML service, matching the FastAPI routers in
// services/ml/app/api. They are joined to the client's base URL.
const (
	pathFraudScore       = "/v1/fraud/score"
	pathFraudRefine      = "/v1/fraud/refine"
	pathPodsDetect       = "/v1/pods/detect"
	pathCommentsClassify = "/v1/comments/classify"
)

// contentTypeJSON is the media type sent and expected on every call.
const contentTypeJSON = "application/json"

// Doer is the minimal HTTP contract the client depends on. The standard
// *http.Client satisfies it; tests inject a fake so no test reaches the network.
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Client is the typed ML-service client. It holds only immutable configuration
// and issues no shared mutable state, so it is safe for concurrent use by the
// orchestrator's worker pool.
type Client struct {
	baseURL string
	http    Doer
}

// New builds a Client for the ML service at baseURL, using doer for transport.
// A trailing slash on baseURL is trimmed so endpoint joins stay well-formed.
func New(baseURL string, doer Doer) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    doer,
	}
}

// ScoreFraud requests an authenticity-risk estimate for one account. Build the
// request from a connector.Snapshot with BuildFraudRequest.
func (c *Client) ScoreFraud(ctx context.Context, req FraudScoreRequest) (FraudScoreResponse, error) {
	var resp FraudScoreResponse
	err := c.do(ctx, pathFraudScore, req, &resp)
	return resp, err
}

// RefineFraud asks the fraud champion to score the FULL assembled feature vector
// (all six FEATURE_ORDER signals the scoring layer aggregated across the fraud and
// clique models), matching what the champion trained on. In cold start the ML
// service returns Refined=false and the caller keeps its heuristic aggregate.
func (c *Client) RefineFraud(ctx context.Context, req FraudRefineRequest) (FraudRefineResponse, error) {
	var resp FraudRefineResponse
	err := c.do(ctx, pathFraudRefine, req, &resp)
	return resp, err
}

// DetectPods requests engagement-pod clusters from a set of comment events.
// Build the request from a connector.Snapshot with BuildPodsRequest, which
// preserves each comment's PostID so the co-commenter graph is reconstructable.
func (c *Client) DetectPods(ctx context.Context, req PodsDetectRequest) (PodsDetectResponse, error) {
	var resp PodsDetectResponse
	err := c.do(ctx, pathPodsDetect, req, &resp)
	return resp, err
}

// ClassifyComments requests rule-based quality buckets for a batch of comments.
func (c *Client) ClassifyComments(ctx context.Context, req CommentsClassifyRequest) (CommentsClassifyResponse, error) {
	var resp CommentsClassifyResponse
	err := c.do(ctx, pathCommentsClassify, req, &resp)
	return resp, err
}

// do performs one JSON POST against the ML service, decoding a 2xx body into
// out. A transport failure (timeout, connection refused) maps to
// KindUnavailable so the audit degrades to partial. A non-2xx response is
// classified from the {code, message} envelope. The envelope's raw message is
// carried only in the wrapped cause (for logs), never as the client-facing
// Message, so an internal detail from the ML service cannot reach a response
// body.
func (c *Client) do(ctx context.Context, path string, in, out any) error {
	payload, err := json.Marshal(in)
	if err != nil {
		return errs.Wrap(err, errs.KindInternal, "ml.encode",
			"could not encode the ml request")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return errs.Wrap(err, errs.KindInternal, "ml.request_build",
			"could not build the ml request")
	}
	req.Header.Set("Content-Type", contentTypeJSON)
	req.Header.Set("Accept", contentTypeJSON)

	resp, err := c.http.Do(req)
	if err != nil {
		// A timeout or connection failure is not a permanent error: report the
		// dependency as unavailable so the orchestrator produces a partial audit
		// instead of failing outright.
		return errs.Wrap(err, errs.KindUnavailable, "ml.transport",
			"the ml service is unavailable")
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return errs.Wrap(err, errs.KindUnavailable, "ml.read",
			"could not read the ml response")
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return classifyResponse(path, resp.StatusCode, body)
	}

	if err := json.Unmarshal(body, out); err != nil {
		return errs.Wrap(err, errs.KindInternal, "ml.decode",
			"could not decode the ml response")
	}
	return nil
}

// classifyResponse maps a non-2xx ML response onto the shared errs vocabulary.
// It best-effort decodes the {code, message} envelope and folds it into the
// cause (never the client-facing Message). The status code drives the Kind; a
// 5xx maps to KindUnavailable so the orchestrator degrades to a partial audit
// rather than failing on a transient ML-service fault.
func classifyResponse(path string, status int, body []byte) error {
	var env errorEnvelope
	_ = json.Unmarshal(body, &env) // best effort; body may not be JSON

	cause := fmt.Errorf("ml %s: http %d code=%q message=%q", path, status, env.Code, env.Message)

	kind, code, message := classifyStatus(status)
	return errs.Wrap(cause, kind, code, message)
}

// classifyStatus resolves an HTTP status code to a Kind, a stable machine code,
// and a safe client-facing message. Unmapped statuses default to KindUnavailable
// so an unexpected ML fault degrades the audit rather than surfacing an opaque
// 500 to the caller.
func classifyStatus(status int) (errs.Kind, string, string) {
	switch status {
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return errs.KindInvalid, "ml.invalid", "the ml service rejected the request"
	case http.StatusUnauthorized:
		return errs.KindUnauthorized, "ml.unauthorized", "the ml service rejected the credentials"
	case http.StatusForbidden:
		return errs.KindForbidden, "ml.forbidden", "the ml service denied the request"
	case http.StatusNotFound:
		return errs.KindNotFound, "ml.not_found", "the ml endpoint was not found"
	case http.StatusConflict:
		return errs.KindConflict, "ml.conflict", "the ml request conflicts with current state"
	case http.StatusPaymentRequired:
		return errs.KindQuotaExceeded, "ml.quota_exceeded", "the ml service quota is exhausted"
	case http.StatusTooManyRequests:
		return errs.KindRateLimited, "ml.rate_limited", "the ml service is rate limiting requests"
	default:
		return errs.KindUnavailable, "ml.unavailable", "the ml service is unavailable"
	}
}
