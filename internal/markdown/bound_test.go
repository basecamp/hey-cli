//go:build !race

package markdown

import "time"

// linearBound is how long a linear-time test may take before it is taken for the
// quadratic run it guards against: in one pass each takes well under a second, and the
// quadratic runs took tens of seconds.
const linearBound = 5 * time.Second
