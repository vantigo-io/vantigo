package calendar

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// calendarBusy is the module's contracts.CalendarBusy, the first contract it
// provides (calendar Outlook sync design O9.8): people's busy time, gated by
// the calendar's own colleague rule. Delivery B, task 3b fills it — the
// gate, the bounds, the appointments and the Outlook rows, the merge; until
// then it answers nothing, which a consumer reads as the calendar showing it
// no one's busy time.
type calendarBusy struct{}

var _ contracts.CalendarBusy = calendarBusy{}

// newCalendarBusy builds the module's provider for Compose.
func newCalendarBusy(module.Deps) contracts.CalendarBusy {
	return calendarBusy{}
}

// Busy answers nothing until task 3b fills it.
func (calendarBusy) Busy(context.Context, []uuid.UUID, time.Time, time.Time) (map[uuid.UUID]contracts.BusyTime, error) {
	return map[uuid.UUID]contracts.BusyTime{}, nil
}
