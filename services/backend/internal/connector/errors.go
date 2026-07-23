package connector

import (
	"fmt"
	"time"

	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// RateLimitError is returned by Connector.Fetch when a platform reports that
// the caller is being rate limited. It carries the platform and, when the
// platform supplies one, the RetryAfter hint the orchestrator should wait
// before retrying.
//
// It embeds an *errs.Error so that errs.Status classifies it as
// KindRateLimited (HTTP 429) through the shared error vocabulary, while
// remaining independently recoverable via errors.As:
//
//	var rl *connector.RateLimitError
//	if errors.As(err, &rl) { reschedule(after: rl.RetryAfter) }
type RateLimitError struct {
	Platform   Platform
	RetryAfter time.Duration
	// err carries the shared classification (KindRateLimited) and wraps the
	// originating upstream cause for errors.Is/logging.
	err *errs.Error
}

// NewRateLimitError builds a *RateLimitError classified as KindRateLimited.
// retryAfter may be zero when the platform gave no hint; cause may be nil.
func NewRateLimitError(platform Platform, retryAfter time.Duration, cause error) *RateLimitError {
	return &RateLimitError{
		Platform:   platform,
		RetryAfter: retryAfter,
		err: errs.Wrap(cause, errs.KindRateLimited, "connector.rate_limited",
			"platform rate limit exceeded"),
	}
}

func (e *RateLimitError) Error() string {
	if e.RetryAfter > 0 {
		return fmt.Sprintf("connector %s: rate limited, retry after %s", e.Platform, e.RetryAfter)
	}
	return fmt.Sprintf("connector %s: rate limited", e.Platform)
}

// Unwrap exposes the embedded *errs.Error so both errors.As(*errs.Error) (used
// by errs.Status) and errors.Is against the upstream cause succeed.
func (e *RateLimitError) Unwrap() error { return e.err }

// QuotaExhaustedError is returned by Connector.Fetch when the platform's
// periodic quota (e.g. YouTube's daily unit budget) is spent. Unlike a rate
// limit it is not retryable within the current window, so the orchestrator
// records a partial audit and reschedules for the next window.
//
// It embeds an *errs.Error classified as KindQuotaExceeded (HTTP 402) and is
// recoverable via errors.As.
type QuotaExhaustedError struct {
	Platform Platform
	// ResetAt is when the quota window rolls over, when the platform reports
	// it; otherwise zero.
	ResetAt time.Time
	err     *errs.Error
}

// NewQuotaExhaustedError builds a *QuotaExhaustedError classified as
// KindQuotaExceeded. resetAt may be zero; cause may be nil.
func NewQuotaExhaustedError(platform Platform, resetAt time.Time, cause error) *QuotaExhaustedError {
	return &QuotaExhaustedError{
		Platform: platform,
		ResetAt:  resetAt,
		err: errs.Wrap(cause, errs.KindQuotaExceeded, "connector.quota_exhausted",
			"platform quota exhausted"),
	}
}

func (e *QuotaExhaustedError) Error() string {
	if !e.ResetAt.IsZero() {
		return fmt.Sprintf("connector %s: quota exhausted, resets at %s", e.Platform, e.ResetAt.Format(time.RFC3339))
	}
	return fmt.Sprintf("connector %s: quota exhausted", e.Platform)
}

// Unwrap exposes the embedded *errs.Error so errs.Status classifies this error
// and errors.Is against the upstream cause succeeds.
func (e *QuotaExhaustedError) Unwrap() error { return e.err }
