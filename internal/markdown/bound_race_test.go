//go:build race

package markdown

import "time"

// linearBound is longer under the race detector, which runs this code several times
// slower; a quadratic run is slower by the same factor and still far past it.
const linearBound = 20 * time.Second
