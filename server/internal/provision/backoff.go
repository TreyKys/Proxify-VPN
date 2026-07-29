package provision

import (
	"math/rand/v2"
	"time"
)

const (
	backoffBase = 2 * time.Second
	backoffMax  = 5 * time.Minute
)

// Backoff returns the delay before retry number `attempt` (1-based).
//
// The cap is 5 minutes rather than something longer because the thing we are
// retrying is a user's tunnel not existing. Jitter is +/-20% so a rack of boxes
// coming back after a network partition doesn't get a synchronized stampede
// from every pending assignment at once.
func Backoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	d := backoffBase
	for i := 1; i < attempt && d < backoffMax; i++ {
		d *= 2
	}
	if d > backoffMax {
		d = backoffMax
	}
	jitter := 1 + (rand.Float64()*0.4 - 0.2)
	return time.Duration(float64(d) * jitter)
}
