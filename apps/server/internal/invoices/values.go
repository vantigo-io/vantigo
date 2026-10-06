package invoices

import (
	"fmt"
	"time"
	// The business time zone is a fixed constant, so the module carries its own
	// copy of it rather than trusting the host's zoneinfo (the runtime image is
	// distroless). cmd/vantigo embeds it too; a second import costs nothing.
	_ "time/tzdata"

	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// oslo is the business time zone (D1): this is Norwegian bookkeeping, so there
// is no setting. Every "today" of this module is the calendar day of
// Deps.Clock() here, computed in Go — Postgres' CURRENT_DATE is UTC in the
// container and is never used.
var oslo = mustLoadLocation("Europe/Oslo")

func mustLoadLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(fmt.Sprintf("invoices: load %s: %v", name, err))
	}
	return loc
}

// businessDay is the calendar day an instant falls on in Oslo, as the UTC
// midnight every date of this module is compared at — expenses' own
// derivation (entries_validation.go), with the zone fixed.
func businessDay(t time.Time) time.Time {
	t = t.In(oslo)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// pgDate stores a date as the date column wants it.
func pgDate(d time.Time) pgtype.Date { return pgtype.Date{Time: d, Valid: true} }

// pgDateOf is a nullable date column's value as the UTC midnight every date
// here is compared at, nil when NULL.
func pgDateOf(d pgtype.Date) *time.Time {
	if !d.Valid {
		return nil
	}
	return ptr(utcDay(d.Time))
}

// wireDate is a date as the contract carries one.
func wireDate(d time.Time) openapi_types.Date { return openapi_types.Date{Time: d} }

// floatFromNumeric reads a NOT NULL numeric column onto the wire as a JSON
// number. Money never goes the other way through a float: amounts enter
// through ratFromFloat's decimal text (decimal.go).
func floatFromNumeric(n pgtype.Numeric) (float64, error) {
	if !n.Valid {
		return 0, nil
	}
	f, err := n.Float64Value()
	if err != nil {
		return 0, fmt.Errorf("invoices: read a stored decimal: %w", err)
	}
	return f.Float64, nil
}

// utcDay is a wire date as the UTC midnight every date here is compared at.
func utcDay(d time.Time) time.Time {
	d = d.UTC()
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)
}
