package ratelimit

import "time"

// Clock is the injectable time source. The token bucket advances by the
// difference between successive Now readings and the ledger derives its UTC day
// boundary from Now, so a fake Clock lets tests drive refill and day rollover
// without sleeping. Production uses SystemClock, whose readings carry the
// monotonic component of time.Now so elapsed-time arithmetic is immune to wall-
// clock adjustments.
type Clock interface {
	Now() time.Time
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

// SystemClock is the production Clock, backed by time.Now.
var SystemClock Clock = systemClock{}
