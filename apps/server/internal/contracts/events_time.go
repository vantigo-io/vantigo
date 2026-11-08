package contracts

import (
	"github.com/google/uuid"
)

// The events the time module publishes onto the platform outbox (virtual
// employees design V30). A payload carries ids, dates and figures and never
// personal data (O3).

// WeekUnsubmitted is time.week.unsubmitted v1: a person did not submit a week
// that has ended. WeekStart is the week's Monday (a date); Hours is what they
// registered in it, as decimal text. It is published once for each person and
// week, by the time-weeks worker, from the Tuesday after the week, in the business time zone.
type WeekUnsubmitted struct {
	UserID    uuid.UUID `json:"userId"`
	WeekStart string    `json:"weekStart"`
	Hours     string    `json:"hours"`
}

func (WeekUnsubmitted) EventName() string { return "time.week.unsubmitted" }
func (WeekUnsubmitted) EventVersion() int { return 1 }

// AggregateID is the person and the week: the aggregate time.week is one
// person's week.
func (e WeekUnsubmitted) AggregateID() string { return e.UserID.String() + "|" + e.WeekStart }
