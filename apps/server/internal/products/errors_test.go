package products

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// TestIsRestrictConflict_MapsBothSQLStates pins both halves of the
// RESTRICT-to-409 divergence (products inventory §4/§7 oddity 7, corrected
// 2026-09-12): 23001 (restrict_violation) is what this schema's literal
// `ON DELETE RESTRICT` foreign keys actually raise, confirmed against a
// real Postgres instance in internal/db/schema_test.go's
// isRestrictViolation and exercised end-to-end by
// categories_test.go's/taxcategories_test.go's delete-blocked-by-RESTRICT
// tests; 23503 (foreign_key_violation, the FK default this schema never
// uses) is unreachable through any real delete this schema can produce, so
// it is pinned here directly against a synthetic error instead. A mutation
// that dropped either half of the `||`, that compared against the wrong
// code, or that swapped either for 23505/23P01 (the two SQLSTATEs
// httpx.WriteError already maps for every other module) would flip one of
// these cases without flipping the other.
func TestIsRestrictConflict_MapsBothSQLStates(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"restrict_violation 23001", &pgconn.PgError{Code: "23001"}, true},
		{"foreign_key_violation 23503", &pgconn.PgError{Code: "23503"}, true},
		{"unique_violation 23505 is httpx.WriteError's job, not this one's", &pgconn.PgError{Code: "23505"}, false},
		{"exclusion_violation 23P01 is httpx.WriteError's job, not this one's", &pgconn.PgError{Code: "23P01"}, false},
		{"an unrelated Postgres error code", &pgconn.PgError{Code: "42601"}, false},
		{"a non-Postgres error", errors.New("boom"), false},
		{"nil", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isRestrictConflict(c.err); got != c.want {
				t.Errorf("isRestrictConflict(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}
