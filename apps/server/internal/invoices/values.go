package invoices

import (
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// Every day of this module is a day of the workspace's business time zone
// (time zones design D2), read through Deps.Calendar once per request or
// worker cycle (businessZone) and handed down as a *time.Location; the day an
// instant falls on is holidays.DayIn(t, loc), a UTC midnight, the form every
// date here is compared at. Postgres' CURRENT_DATE is UTC in the container
// and is never used. D1 of the foundation design said "Norwegian bookkeeping,
// so there is no setting": its reason stands — bookkeeping has one calendar,
// and there is no setting of this module's — but that calendar is now the
// installation's rather than Norway's, which for a Norwegian installation is
// the same day.

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
