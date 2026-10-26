package contracts

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// Workforce is Staff's register read by another module (staff competence
// design S15, planning design S8): the competence catalogue, who holds what
// valid on a date, the people in employment and their capacity over a window,
// and the caller's own person. Staff is its only provider
// (module.Module.Workforce) and Planning its consumer, which reads it through
// module.Deps.Workforce and never Staff's tables.
//
// Staff is its own gatekeeper (module-boundaries rule 12): it applies the
// Colleague rule to the principal in ctx — staff:view, and the person in
// employment on today or on a later day of the window asked (the date on,
// when it is today or later; today itself for a past date) — and answers
// nothing, not an error, to anyone else; a context with neither a request
// nor contracts.AsUser answers nothing too. Self is the one read under
// Staff's Self rule instead (staff:access and a self-service link that gives
// the caller Self). Every figure is derived: never a terms row, an absence
// type, a share of a day or a status. The consumer never re-states the rule,
// and calls with the request's context, never inside a transaction of its
// own.
//
// People and Capacity take a window bounded to the Monday thirteen weeks
// before today and three years after it; a window outside answers
// ErrWindowOutOfBounds, so a past window never lists leavers.
type Workforce interface {
	// Competences is the catalogue: every competence not archived, hidden ones
	// included, with its category, kind, hasLevel and validityMonths. It needs
	// staff:view or staff:manage and answers an empty list otherwise.
	Competences(ctx context.Context) ([]CompetenceEntry, error)
	// HeldCompetences answers, for each person id, the competences they hold
	// valid on the date — on, a calendar date at midnight UTC: the current
	// row, valid_from on or before it and valid_to absent or on or after it.
	// A declared certificate is not held; a declared skill is, with Verified
	// false. A person the caller may not see — not in employment on the date
	// asked (today, for a past date) — is simply absent from the map.
	HeldCompetences(ctx context.Context, on time.Time, personIDs []int64) (map[int64][]HeldCompetence, error)
	// People answers the people in employment on today or on a later day of
	// from..to (civil dates at midnight UTC, inclusive), as the caller may see
	// them: the register's work group, never a private field. A person
	// employed today whose employments miss the window is left out: only
	// people with an employed day inside from..to are answered. It needs
	// staff:view and answers nothing otherwise.
	People(ctx context.Context, from, to time.Time) ([]PersonEntry, error)
	// Capacity answers, per person id, the hours a week in force over from..to
	// and the days away — the approved and registered absences coalesced into
	// whole-day spans and clipped to the window, as Staff's calendar answers a
	// colleague: no share, no hours, no type, no status. A person the caller
	// may not see is absent from the map. It needs staff:view.
	Capacity(ctx context.Context, from, to time.Time, personIDs []int64) (map[int64]PersonCapacity, error)
	// Self answers the caller's own person — the one whose self-service link
	// names the caller in a state that gives them Self, under Staff's own
	// rule — or (nil, nil). It needs staff:access and nothing more.
	Self(ctx context.Context) (*PersonEntry, error)
}

// ErrWindowOutOfBounds is answered by People and Capacity for a window that
// starts before the Monday thirteen weeks before today or ends more than
// three years after it.
var ErrWindowOutOfBounds = errors.New("workforce: the window is outside the bound")

// DateSpan is a run of civil dates, From..To inclusive, both at midnight UTC.
type DateSpan struct {
	From, To time.Time
}

// PersonEntry is one person as People and Self answer them.
type PersonEntry struct {
	ID             int64
	DisplayName    string
	EmployeeNumber string
	// DepartmentID and DepartmentName are the person's place today, or on
	// their first day in employment inside the window when that is later;
	// nil and "" when the terms name no department.
	DepartmentID   *int64
	DepartmentName string
	// UserID is the user the person's self-service link names in a state that
	// gives that user Self, nil otherwise — for mailing the person through
	// UserDirectory.Notify, and for nothing else.
	UserID *uuid.UUID
	// Employed is the days inside the window asked the person is in
	// employment, merged into non-overlapping spans. Self answers it empty.
	Employed []DateSpan
}

// PersonCapacity is one person's capacity over the window asked.
type PersonCapacity struct {
	// Segments are consecutive, non-overlapping and inside employment: the
	// hours a week in force — the full position's hours × the FTE, summed over
	// concurrent employments.
	Segments []CapacitySegment
	// Away is the whole-day spans the person is away, coalesced and clipped.
	Away []DateSpan
}

// CapacitySegment is a run of days with one figure of hours a week in force.
type CapacitySegment struct {
	From, To     time.Time
	HoursPerWeek float64
}

// CompetenceEntry is one competence of the catalogue.
type CompetenceEntry struct {
	ID          int64
	Key         string // stable, "hms-kort"
	NameEn      string
	NameNb      string
	CategoryKey string // "sertifikat"
	Kind        string // "skill" or "certificate"
	HasLevel    bool   // its rows carry a level of 0 to 4
	// ValidityMonths is how long a certificate is valid from its issue; nil
	// on a skill and on a certificate without end.
	ValidityMonths *int16
	Hidden         bool // offered for no new row, still held by those who have it
}

// HeldCompetence is one competence a person holds on the date asked about.
type HeldCompetence struct {
	CompetenceID int64
	Key          string
	Kind         string // "skill" or "certificate"
	Level        *int16 // 0 to 4 on a competence with a level, else nil
	Verified     bool   // false for a declared skill; a held certificate is always verified
	ValidFrom    time.Time
	ValidTo      *time.Time // nil without end; both civil dates at midnight UTC
}
