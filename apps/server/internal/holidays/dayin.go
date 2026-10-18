package holidays

import "time"

// DayIn is the calendar day t falls on in loc, as a date at midnight UTC —
// the form every date column in the schema is compared at, and the form the
// functions of this package take and return. It is the one derivation of "a
// day" from an instant: Expenses and Staff each wrote it for themselves (as
// businessDay and civilToday) until the business time zone became one
// setting (contracts.BusinessCalendar), and both now mean this.
//
// The day is taken in loc and rebuilt at UTC midnight on purpose: day
// arithmetic on a UTC midnight never meets a daylight-saving shift, and two
// callers that compare such days agree about what a day is. A trip departing
// at 00:30 on 1 July in Oslo is 22:30 on 30 June in UTC, and this answers
// 1 July — the day the period lock, the per-diem window and the a-melding
// are about.
func DayIn(t time.Time, loc *time.Location) time.Time {
	y, m, d := t.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}
