package calendar

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/calendar/store"
)

// The bounds of an appointment (calendar design D2, D3) and of a window (D5).
// The database holds the same widths as varchar lengths; these are what the
// module refuses per field before it gets that far.
const (
	maxTitle       = 200
	maxDescription = 10000
	maxLocation    = 200
	maxAttendees   = 50
	// maxTimedSpan is the longest a timed appointment may be, and
	// maxAllDayDays the most days an all-day one may cover, both ends
	// counted.
	maxTimedSpan  = 31 * 24 * time.Hour
	maxAllDayDays = 366
	// maxWindowDays is the longest window GET /items answers, both ends
	// counted: the month grid's six weeks and a quarter's list both fit, and
	// it is Staff's calendar limit too.
	maxWindowDays = 93
)

// The vocabulary of visibility and transparency (calendar design D2):
// iCalendar's CLASS and TRANSP in this module's words, stored as such and
// translated at the edge by the feed and the invitations.
const (
	visibilityShared   = "shared"  // CLASS:PUBLIC — a colleague sees the title
	visibilityPrivate  = "private" // CLASS:PRIVATE — a colleague sees "busy"
	transparencyBusy   = "busy"    // TRANSP:OPAQUE — blocks the time
	transparencyFree   = "free"    // TRANSP:TRANSPARENT — does not
	statusNeedsAction  = "needs-action"
	appointmentKind    = "calendar.appointment"
	invalidParameterPG = "22023" // invalid_parameter_value: a zone Postgres does not know
)

// civil is t's calendar date at midnight UTC — the form every date column
// is compared at (holidays.DayIn).
func civil(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// addDays is the civil date d moved n days. A UTC midnight never meets a
// daylight-saving shift, so a day is always 24 hours.
func addDays(d time.Time, n int) time.Time { return d.AddDate(0, 0, n) }

// daysBetween is how many days from from to to, to's own not counted.
func daysBetween(from, to time.Time) int { return int(to.Sub(from).Hours() / 24) }

// pgDate is a civil date as a date column takes it.
func pgDate(d time.Time) pgtype.Date { return pgtype.Date{Time: civil(d), Valid: true} }

// fromPgDate is a date column as a civil date; ok is false for NULL.
func fromPgDate(d pgtype.Date) (time.Time, bool) {
	if !d.Valid {
		return time.Time{}, false
	}
	return civil(d.Time), true
}

// apiDate is a civil date on the wire.
func apiDate(d time.Time) openapi_types.Date { return openapi_types.Date{Time: civil(d)} }

// apiDatePtr is a date column on the wire, nil for NULL.
func apiDatePtr(d pgtype.Date) *openapi_types.Date {
	v, ok := fromPgDate(d)
	if !ok {
		return nil
	}
	out := apiDate(v)
	return &out
}

// utcPtr is an instant column on the wire in UTC, nil for NULL.
func utcPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	v := t.UTC()
	return &v
}

// optionalText is an optional free-text field trimmed: nil when absent or
// blank, and the message when it is longer than limit characters.
func optionalText(raw *string, limit int, what string) (*string, string) {
	if raw == nil {
		return nil, ""
	}
	v := strings.TrimSpace(*raw)
	if v == "" {
		return nil, ""
	}
	if utf8.RuneCountInString(v) > limit {
		return nil, fmt.Sprintf("%s is at most %d characters", what, limit)
	}
	return &v, ""
}

// sameText reports whether two optional texts are the same, nil and nil
// included.
func sameText(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// parseIfMatch reads the revision an If-Match header carries: the bare number
// the response's revision gave, or that number quoted — and weakly tagged — as
// a client that treats the header as an ETag sends it back (Staff's reading).
// A revision is 1 or more; anything else is no revision at all, which the
// caller answers as a 400.
func parseIfMatch(raw string) (int32, bool) {
	v := strings.TrimSpace(raw)
	v = strings.TrimPrefix(v, "W/")
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		v = v[1 : len(v)-1]
	}
	n, err := strconv.ParseInt(v, 10, 32)
	if err != nil || n < 1 {
		return 0, false
	}
	return int32(n), true
}

// zoneUnknownToGo is why Go refuses a zone name, "" when it loads: an empty
// name and "Local" say nothing about where an appointment was made (Go's
// LoadLocation answers UTC for "" without an error, and "Local" is whatever
// zone the server process runs in), and a name Go's tzdata lacks is not one
// an occurrence can be derived in. The words are identity's workspace zone
// check's.
func zoneUnknownToGo(name string) string {
	switch _, err := time.LoadLocation(name); {
	case name == "" || name == "Local":
		return "A time zone is an IANA name, such as 'Europe/Oslo'"
	case err != nil:
		return fmt.Sprintf("'%s' is not a time zone this installation knows", name)
	}
	return ""
}

// zoneDisagreement is why a zone name may not be used, "" when it may — the
// second of the two checks, identity's own restated (calendar design D2: "Go
// loads it; Postgres agrees"). It asks not "does Postgres know this name" but
// "do Postgres and Go mean the same thing by it": Postgres resolves
// pg_timezone_abbrevs before pg_timezone_names, so 'CET' is a fixed +01:00 to
// it and a zone with summer time to Go. The comparison is of UTC offsets at a
// winter instant and a summer one of now's year. It reads the pool, never
// inside one of the module's transactions.
func (s *server) zoneDisagreement(ctx context.Context, name string, now time.Time) (string, error) {
	winter := time.Date(now.Year(), time.January, 15, 12, 0, 0, 0, time.UTC)
	summer := time.Date(now.Year(), time.July, 15, 12, 0, 0, 0, time.UTC)
	offsets, err := s.queries().ResolveTimeZone(ctx, store.ResolveTimeZoneParams{Name: name, Winter: winter, Summer: summer})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == invalidParameterPG {
			return fmt.Sprintf("'%s' is not a time zone this installation's database knows", name), nil
		}
		return "", fmt.Errorf("calendar: check a time zone: %w", err)
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		// zoneUnknownToGo already refused this; answered rather than asserted.
		return fmt.Sprintf("'%s' is not a time zone this installation knows", name), nil
	}
	for _, probe := range []struct {
		at     time.Time
		inSQL  int64
		season string
	}{
		{winter, offsets.WinterOffset, "January"},
		{summer, offsets.SummerOffset, "July"},
	} {
		if _, inGo := probe.at.In(loc).Zone(); int64(inGo) != probe.inSQL {
			return fmt.Sprintf(
				"'%s' means one thing to this installation and another to its database in %s, "+
					"so the two would not agree which day a date falls on — use a city zone such as 'Europe/Oslo'",
				name, probe.season), nil
		}
	}
	return "", nil
}

// zone is a zone name held to both checks: the location, or the message
// that refuses it.
func (s *server) zone(ctx context.Context, name string) (*time.Location, string, error) {
	if msg := zoneUnknownToGo(name); msg != "" {
		return nil, msg, nil
	}
	msg, err := s.zoneDisagreement(ctx, name, s.deps.Clock())
	if err != nil || msg != "" {
		return nil, msg, err
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, "", fmt.Errorf("calendar: load the time zone %q: %w", name, err)
	}
	return loc, "", nil
}

// window is the span GET /items and a colleague's calendar answer (calendar
// design D5): two civil dates, both counted, judged in the viewer's zone —
// a timed item is in it when it overlaps [fromAt, toAt), from's midnight to
// the midnight after to in loc; an all-day one when its dates overlap
// [from, to].
type window struct {
	from, to     time.Time
	loc          *time.Location
	fromAt, toAt time.Time
}

// parseWindow is the window a request names: from and to, at most
// maxWindowDays apart, and timeZone held to the two checks — the business
// zone when absent. The field errors are keyed by the parameter.
func (s *server) parseWindow(ctx context.Context, fromRaw, toRaw openapi_types.Date, zoneRaw *string, business *time.Location) (window, fieldErrors, error) {
	from, to := civil(fromRaw.Time), civil(toRaw.Time)
	var f fieldErrors
	switch {
	case to.Before(from):
		f.add("to", "to cannot be before from")
	case daysBetween(from, to)+1 > maxWindowDays:
		f.add("to", fmt.Sprintf("At most %d days from from", maxWindowDays))
	}
	loc := business
	if zoneRaw != nil {
		l, msg, err := s.zone(ctx, strings.TrimSpace(*zoneRaw))
		if err != nil {
			return window{}, f, err
		}
		f.add("timeZone", msg)
		loc = l
	}
	if f.failed() {
		return window{}, f, nil
	}
	return window{
		from: from, to: to, loc: loc,
		fromAt: wallClock(from, loc),
		toAt:   wallClock(addDays(to, 1), loc),
	}, f, nil
}

// wallClock is the instant midnight starts the civil date d in loc.
func wallClock(d time.Time, loc *time.Location) time.Time {
	y, m, day := d.Date()
	return time.Date(y, m, day, 0, 0, 0, 0, loc)
}
