package contracts

import (
	"context"
	"time"
)

// Workforce is Staff's register of competences read by another module (staff
// competence design S15): the catalogue, and who holds what, valid on a date.
// Staff is its only provider (module.Module.Workforce) and Staffing its
// intended consumer, which reads it through module.Deps.Workforce and never
// Staff's tables.
//
// Staff is its own gatekeeper (module-boundaries rule 12): it applies the
// Colleague rule — staff:view, and the person active today — to the
// principal in ctx, and answers nothing, not an error, to anyone else; a
// context with neither a request nor contracts.AsUser answers nothing too.
// The consumer never re-states the rule, and calls with the request's
// context, never inside a transaction of its own.
type Workforce interface {
	// Competences is the catalogue: every competence not archived, hidden ones
	// included, with its category, kind, hasLevel and validityMonths. It needs
	// staff:view or staff:manage and answers an empty list otherwise.
	Competences(ctx context.Context) ([]CompetenceEntry, error)
	// HeldCompetences answers, for each person id, the competences they hold
	// valid on the date — on, a calendar date at midnight UTC: the current
	// row, valid_from on or before it and valid_to absent or on or after it.
	// A declared certificate is not held; a declared skill is, with Verified
	// false. A person the caller may not see is simply absent from the map.
	HeldCompetences(ctx context.Context, on time.Time, personIDs []int64) (map[int64][]HeldCompetence, error)
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
