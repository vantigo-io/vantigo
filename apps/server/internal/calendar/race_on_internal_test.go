//go:build race

package calendar

import "time"

// parseBudgetWallClock is how long the parse's bounded worst case may take
// under the race detector, which slows the parse several times over: the
// bound is the step budget, and the clock only shows it holds.
const parseBudgetWallClock = 10 * time.Second
