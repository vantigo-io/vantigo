package timetracking

import (
	"fmt"
	"math"
	"math/big"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/time/gen"
)

// This file is design §4.2's static validation — the rules a create body
// can be held to without asking anyone — plus the enumerations and the
// conversions between the wire's numbers and times and the columns'. Each
// rule takes the raw value and answers the normalized one plus the message
// to report, "" when the rule holds; parseEntry runs them all and collects
// every failure into a map keyed by the camelCase JSON path, so a caller sees
// every problem with their body at once. The rules that need another module
// (the project, the line, the task) and the ones that need the database (the
// lock, the day cap) are entries.go's.

// The statuses an entry moves through (D2, D10). Only draft and rejected are
// the owner's to change; submitted onwards the rates are frozen (D3).
const (
	statusDraft     = "draft"
	statusSubmitted = "submitted"
	statusApproved  = "approved"
	statusRejected  = "rejected"
	statusInvoiced  = "invoiced"
)

// The steps of the rate chain an entry's bill rate can come from (D3), as
// rate_source records them.
const (
	sourceLine    = "line"
	sourceProject = "project"
	sourcePerson  = "person"
	sourceNone    = "none"
)

// The billing-line pricing modes and the one project billing type the rules
// here key on, as projects names them (contracts.BillingLineEntry,
// contracts.ProjectEntry).
const (
	pricingFixed       = "fixed"
	pricingList        = "list"
	pricingDiscount    = "discount"
	billingNonBillable = "non-billable"
)

// The roles contracts.ProjectDirectory.Role answers that decide anything
// here. A member logs time and sees their own entries; a manager also sees
// and approves everyone's on the project (D7, §6). "viewer" grants nothing
// in time.
const (
	roleManager = "manager"
)

// settingLockedBefore is the time.settings key of the period lock (D9),
// stored as YYYY-MM-DD.
const settingLockedBefore = "locked_before"

// maxDayCents is D1's ceiling in hundredths of an hour: one entry, and one
// person's whole day, hold at most 24 hours.
const maxDayCents = 24 * 100

// noteMaxLength is the note column's width.
const noteMaxLength = 2000

// clockPattern is the contract's HH:MM: two-digit hours 00–23 and minutes.
var clockPattern = regexp.MustCompile(`^([01][0-9]|2[0-3]):([0-5][0-9])$`)

// cannotLogTime is the one message a project the caller may not log time on
// answers with, whatever the reason — unknown, not active, or no member or
// manager role — so the refusal says nothing about a project the caller
// cannot see (§4.2).
const cannotLogTime = "You cannot log time on this project"

// parsedEntry is one statically validated create body, in the shape the
// references check and the insert want: the date as a UTC midnight, the hours
// in hundredths (exact, never a float), the clock times in minutes after
// midnight.
type parsedEntry struct {
	ProjectID  int32
	LineID     *int32
	TaskID     *int32
	Date       time.Time
	HoursCents int64
	Start, End *int
	Note       *string
	Billable   *bool
}

// parseEntry runs every static §4.2 rule over a body and answers the parsed
// entry and the field errors (nil when there are none). Every rule runs
// regardless of the others; the one that compares hours with the clock times
// is skipped when either side already failed on its own, since a second
// message about a value already refused only adds noise. An absent entryDate
// decodes as the zero date rather than failing to decode, so "required" is
// this function's rule too.
func parseEntry(body gen.TimeEntryRequest) (parsedEntry, map[string][]string) {
	var errs map[string][]string
	add := func(field, msg string) {
		if msg != "" {
			errs = withFieldError(errs, field, msg)
		}
	}

	if body.EntryDate.IsZero() {
		add("entryDate", "An entry date is required")
	}
	cents, hoursMsg := validateHours(body.Hours)
	add("hours", hoursMsg)
	start, end, timeErrs := validateClockTimes(body.StartTime, body.EndTime)
	for field, msg := range timeErrs {
		add(field, msg)
	}
	if hoursMsg == "" && start != nil && end != nil {
		add("hours", validateHoursMatchTimes(cents, *start, *end))
	}
	note, msg := validateNote(body.Note)
	add("note", msg)

	return parsedEntry{
		ProjectID:  body.ProjectId,
		LineID:     body.BillingLineId,
		TaskID:     body.TaskId,
		Date:       body.EntryDate.Time,
		HoursCents: cents,
		Start:      start,
		End:        end,
		Note:       note,
		Billable:   body.Billable,
	}, errs
}

// withFieldError adds one message to a map another rule may already have put
// something in. A nil map is the "nothing failed yet" case, so it is grown
// rather than written to.
func withFieldError(errs map[string][]string, field, message string) map[string][]string {
	if errs == nil {
		errs = map[string][]string{}
	}
	errs[field] = append(errs[field], message)
	return errs
}

// validateHours is D1's duration rule: greater than zero, at most 24, and at
// most two decimals. It answers the hours in hundredths, which is how every
// comparison and sum here is done, so no float rounding ever decides a cap.
// "At most two decimals" is judged on the number's shortest decimal text —
// the same text numericFromFloatPtr stores — not on float arithmetic.
func validateHours(h float64) (int64, string) {
	switch {
	case math.IsNaN(h) || h <= 0:
		return 0, "Hours must be greater than zero"
	case h > 24:
		return 0, fmt.Sprintf("Hours cannot be more than 24, but was %s", formatNumber(h))
	}
	text := strconv.FormatFloat(h, 'f', -1, 64)
	if _, decimals, ok := strings.Cut(text, "."); ok && len(decimals) > 2 {
		return 0, fmt.Sprintf("Hours can have at most two decimals, but was %s", text)
	}
	return int64(math.Round(h * 100)), ""
}

// validateClockTimes is D1's start/end rule: both or neither, each HH:MM, and
// the end after the start on the same day — an entry crossing midnight is two
// entries, one on each date. It answers the times in minutes after midnight,
// both nil when neither was given, and the field errors (nil when none).
func validateClockTimes(rawStart, rawEnd *string) (*int, *int, map[string]string) {
	errs := map[string]string{}
	start, startMsg := parseClock("A start time", rawStart)
	if startMsg != "" {
		errs["startTime"] = startMsg
	}
	end, endMsg := parseClock("An end time", rawEnd)
	if endMsg != "" {
		errs["endTime"] = endMsg
	}
	switch {
	case rawStart != nil && rawEnd == nil:
		errs["endTime"] = "An end time is required when a start time is given"
	case rawStart == nil && rawEnd != nil:
		errs["startTime"] = "A start time is required when an end time is given"
	case start != nil && end != nil && *end <= *start:
		errs["endTime"] = "An end time must be after the start time on the same day"
	}
	if len(errs) > 0 {
		return nil, nil, errs
	}
	return start, end, nil
}

// parseClock reads one optional HH:MM into minutes after midnight. label
// names the field in the message ("A start time").
func parseClock(label string, raw *string) (*int, string) {
	if raw == nil {
		return nil, ""
	}
	m := clockPattern.FindStringSubmatch(*raw)
	if m == nil {
		return nil, fmt.Sprintf("%s must be HH:MM, but was '%s'", label, *raw)
	}
	hours, _ := strconv.Atoi(m[1])
	minutes, _ := strconv.Atoi(m[2])
	total := hours*60 + minutes
	return &total, ""
}

// validateHoursMatchTimes is D1's derivation: when both times are given the
// server recomputes end − start in hours to two decimals, and the hours the
// client sent must be exactly that. The refusal is on hours, the field the
// client derived.
func validateHoursMatchTimes(cents int64, start, end int) string {
	derived := int64(math.Round(float64(end-start) * 100 / 60))
	if derived == cents {
		return ""
	}
	return fmt.Sprintf("Hours must equal the time between the start and end time, %s, but was %s",
		formatCents(derived), formatCents(cents))
}

// validateNote is the note rule: optional, at most 2000 characters once
// trimmed. A blank note is stored as none, so "  " and an absent field mean
// the same thing.
func validateNote(raw *string) (*string, string) {
	if raw == nil {
		return nil, ""
	}
	trimmed := strings.TrimSpace(*raw)
	if trimmed == "" {
		return nil, ""
	}
	if n := utf8.RuneCountInString(trimmed); n > noteMaxLength {
		return nil, fmt.Sprintf("A note cannot be longer than %d characters, the given value was %d characters", noteMaxLength, n)
	}
	return &trimmed, ""
}

// resolveBillable is §4.2's billable default: a non-billable project's
// entries are never billable, whatever was sent; otherwise the caller's
// choice, and billable when they made none.
func resolveBillable(billingType string, requested *bool) bool {
	if billingType == billingNonBillable {
		return false
	}
	if requested == nil {
		return true
	}
	return *requested
}

// dayCapExceeded is the message the hours field carries when the caller's
// day would hold more than 24 hours with this entry in it.
func dayCapExceeded(date time.Time, totalCents int64) string {
	return fmt.Sprintf("Your hours on %s would total %s, and a day holds at most 24",
		date.Format(time.DateOnly), formatCents(totalCents))
}

// lockedBeforeMessage is the message the entryDate field carries when the
// entry is dated before the period lock (D9).
func lockedBeforeMessage(lock time.Time) string {
	return fmt.Sprintf("Time before %s is locked", lock.Format(time.DateOnly))
}

// formatCents renders hundredths of an hour the way a message quotes hours:
// "24", "7.5", "0.33".
func formatCents(cents int64) string { return formatNumber(float64(cents) / 100) }

// formatNumber renders a number in its shortest decimal form.
func formatNumber(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// numericFromCents stores hundredths exactly: an integer with a scale of two,
// never through a float.
func numericFromCents(cents int64) pgtype.Numeric {
	return pgtype.Numeric{Int: big.NewInt(cents), Exp: -2, Valid: true}
}

// centsFromNumeric reads a numeric(_,2) column back as hundredths. A NULL
// reads as 0, which is what a SUM over no rows is coalesced to anyway.
func centsFromNumeric(n pgtype.Numeric) (int64, error) {
	f, err := floatPtrFromNumeric(n)
	if err != nil || f == nil {
		return 0, err
	}
	return int64(math.Round(*f * 100)), nil
}

// numericFromFloatPtr converts an optional JSON number into a numeric column
// value through its shortest round-tripping decimal text, so the column's own
// scale does the rounding rather than Go — the conversion projects and
// products make for their prices. nil stays an invalid (SQL NULL)
// pgtype.Numeric.
//
// Scan's error is returned rather than discarded: it can only fire for an
// infinity, which nothing reachable produces, and discarding it would store a
// silent NULL for a rate that was actually resolved.
func numericFromFloatPtr(v *float64) (pgtype.Numeric, error) {
	if v == nil {
		return pgtype.Numeric{}, nil
	}
	var n pgtype.Numeric
	if err := n.Scan(strconv.FormatFloat(*v, 'f', -1, 64)); err != nil {
		return pgtype.Numeric{}, fmt.Errorf("time: %v is not a storable decimal: %w", *v, err)
	}
	return n, nil
}

// floatPtrFromNumeric reads an optional numeric column back onto the wire as
// a JSON number, nil for a SQL NULL — never 0, which is a rate somebody
// actually set to nothing. A number the column holds but Go cannot read is an
// infrastructure failure, returned rather than rendered as absent.
func floatPtrFromNumeric(n pgtype.Numeric) (*float64, error) {
	if !n.Valid {
		return nil, nil
	}
	f, err := n.Float64Value()
	if err != nil {
		return nil, fmt.Errorf("time: read a stored decimal: %w", err)
	}
	if !f.Valid {
		return nil, nil
	}
	return &f.Float64, nil
}

// timeFromMinutes stores minutes after midnight in a time column.
func timeFromMinutes(minutes *int) pgtype.Time {
	if minutes == nil {
		return pgtype.Time{}
	}
	return pgtype.Time{Microseconds: int64(*minutes) * int64(time.Minute/time.Microsecond), Valid: true}
}

// clockFromTime renders a time column as the contract's HH:MM, nil for NULL.
// Seconds never occur: every stored time came in as HH:MM.
func clockFromTime(t pgtype.Time) *string {
	if !t.Valid {
		return nil
	}
	minutes := t.Microseconds / int64(time.Minute/time.Microsecond)
	s := fmt.Sprintf("%02d:%02d", minutes/60, minutes%60)
	return &s
}

// pgDate stores a date as the date column wants it.
func pgDate(d time.Time) pgtype.Date { return pgtype.Date{Time: d, Valid: true} }

// statuses is every status an entry can be in, in the order they are named.
var statuses = []string{statusDraft, statusSubmitted, statusApproved, statusRejected, statusInvoiced}

// validStatus reports whether s is one of statuses.
func validStatus(s string) bool { return slices.Contains(statuses, s) }

// statusList renders statuses the way a message quotes them: 'draft',
// 'submitted', 'approved', 'rejected' or 'invoiced'.
func statusList() string {
	quoted := make([]string, len(statuses))
	for i, s := range statuses {
		quoted[i] = "'" + s + "'"
	}
	return strings.Join(quoted[:len(quoted)-1], ", ") + " or " + quoted[len(quoted)-1]
}

// listDefaultPageSize and listMaxPageSize are customers' and projects' list
// defaults, so every list in the product pages the same way.
const (
	listDefaultPageSize = 25
	listMaxPageSize     = 100
)

// validatePageParams is the paging rule every paged operation shares, in
// customers' and projects' own words: every failure is collected rather than
// the first one reported.
func validatePageParams(page, pageSize *int32) []string {
	var errs []string
	if page != nil && *page < 1 {
		errs = append(errs, fmt.Sprintf("'page' must be 1 or greater, but was %d.", *page))
	}
	if pageSize != nil && (*pageSize < 1 || *pageSize > listMaxPageSize) {
		errs = append(errs, fmt.Sprintf("'pageSize' must be between 1 and %d, but was %d.", listMaxPageSize, *pageSize))
	}
	return errs
}

// pageParams is the validated paging as numbers: page 1 and
// listDefaultPageSize rows unless the caller asked otherwise.
func pageParams(page, pageSize *int32) (int32, int32) {
	p, size := int32(1), int32(listDefaultPageSize)
	if page != nil {
		p = *page
	}
	if pageSize != nil {
		size = *pageSize
	}
	return p, size
}

// daysInWeek is a week's length; a week starts on a Monday (D2).
const daysInWeek = 7

// notAMonday is the message a weekStart that is not a Monday is refused
// with, "" when it is one.
func notAMonday(weekStart time.Time) string {
	if weekStart.Weekday() == time.Monday {
		return ""
	}
	return fmt.Sprintf("A week starts on a Monday, but %s is a %s", weekStart.Format(time.DateOnly), weekStart.Weekday())
}

// mondayOf is the Monday of the week date falls in.
func mondayOf(date time.Time) time.Time {
	return date.AddDate(0, 0, -((int(date.Weekday()) + daysInWeek - 1) % daysInWeek))
}

// weekEnd is the Sunday of the week starting on the Monday weekStart.
func weekEnd(weekStart time.Time) time.Time { return weekStart.AddDate(0, 0, daysInWeek-1) }
