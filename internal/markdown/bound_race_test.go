//go:build race

package markdown

import "time"

// linearBound is longer under the race detector, which runs this code several times
// slower; a quadratic run is slower by the same factor and still far past it.
const linearBound = 60 * time.Second

// linearInputScale shrinks the largest linear-time inputs under the race detector. Those
// tests scan one string on one goroutine, so the detector has nothing to find in them and
// only multiplies their cost (44s and 13s, against 2.7s and 0.8s without it). The
// full-size inputs run in every build without -race, where the bound is held.
const linearInputScale = 10
