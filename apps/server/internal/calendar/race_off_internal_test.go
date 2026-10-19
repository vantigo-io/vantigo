//go:build !race

package calendar

import "time"

// parseBudgetWallClock is how long the parse's bounded worst case may take:
// a second, as the step budget is sized for.
const parseBudgetWallClock = time.Second
