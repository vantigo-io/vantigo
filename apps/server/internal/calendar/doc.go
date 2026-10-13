// Package calendar is the platform's public-holiday calendar: which days are
// Norwegian public holidays, which are business days, and when Easter falls.
// Invoices moves a due date and a reminder letter's deadline by it; Staff
// counts the working days an absence deducts from the holiday account by it.
//
// A day is a civil date at UTC midnight. Deriving it in the installation's
// zone is the caller's job — take the calendar day where the caller lives and
// make it time.Date(y, m, d, 0, 0, 0, 0, time.UTC) — so day arithmetic never
// meets a daylight-saving shift and two callers agree on what "a day" is. A
// time that is not a UTC midnight is read as its civil date in its own
// location — 10:00Z, or midnight in Oslo, on 17 May is 17 May — and the
// functions that return days return them in the form they were given.
//
// PublicHoliday lists the named holidays only; an ordinary Sunday, a
// helligdag in law too, is left to BusinessDay's weekend test.
//
// The calendar is Norway's only; a region beyond it would be a later setting
// that callers pass in. It holds no data, reads no clock and imports the time
// package only (purity_test.go): every day is an input.
package calendar
