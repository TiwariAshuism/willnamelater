// Package ratelimit meters a connector's outbound platform API usage against
// the budget declared in its connector.RateLimit config.
//
// Two metering models exist, matching the two ways the supported platforms bill
// usage:
//
//   - quota_units (YouTube Data API v3): a daily budget of abstract units where
//     each API call costs a number of units — most reads cost 1, search.list
//     costs 100. This is enforced by Ledger, a PERSISTENT accounting store
//     (table api_quota_ledger) so the budget survives restarts and is shared by
//     every worker in the fleet. The debit is a single conditional UPDATE so two
//     workers can never jointly overrun the day's budget.
//
//   - bucketed_calls (Meta Graph API): a fixed number of calls per rolling
//     window. This is enforced by Buckets, an IN-PROCESS token bucket per
//     platform. It is per-process, not shared, because the Graph API's bucket is
//     itself per app+user and the fleet fans out behind one app identity; a
//     process-local bucket is the correct granularity and avoids a database hop
//     on the hot path.
//
// Both are constructed from the same []connector.PlatformConfig and pick out the
// blocks whose Model matches. Time is injected through Clock so tests exercise
// window rollover and refill deterministically without sleeping.
package ratelimit
