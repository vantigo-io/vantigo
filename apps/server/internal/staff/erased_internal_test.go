package staff

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// The read-only guard sits in withPersonLock (plan Task 2): on a person the
// retention run erased, a write under refuseErased is the 409 person_erased
// with fn never run; one of the exceptions, under allowErased, runs; and a
// person who is not erased runs either way.
func TestWithPersonLock_TheErasedGuard(t *testing.T) {
	t.Parallel()
	pool, _ := testdb.Migrated(t)
	ctx := context.Background()
	s := &server{deps: module.Deps{Pool: pool, Clock: time.Now}}
	seed := func(number string, erased bool) int64 {
		var id int64
		if err := pool.QueryRow(ctx, `INSERT INTO staff.people (employee_number, given_name, family_name, display_name, erased_at, created_at, updated_at)
			VALUES ($1, 'Kari', 'Nordmann', 'Kari Nordmann', CASE WHEN $2 THEN now() END, now(), now()) RETURNING id`, number, erased).Scan(&id); err != nil {
			t.Fatalf("seed a person: %v", err)
		}
		return id
	}
	erased, kept := seed("E1", true), seed("K1", false)

	for _, c := range []struct {
		name    string
		person  int64
		rule    erasedRule
		refused bool
	}{
		{"a write on an erased person", erased, refuseErased, true},
		{"an exception on an erased person", erased, allowErased, false},
		{"a write on a person not erased", kept, refuseErased, false},
		{"an exception on a person not erased", kept, allowErased, false},
	} {
		ran := false
		err := s.withPersonLock(ctx, c.person, c.rule, func(context.Context, pgx.Tx, *store.Queries) error {
			ran = true
			return nil
		})
		r, isRefusal := asRefusal(err)
		switch {
		case c.refused && (!isRefusal || r.status != http.StatusConflict || r.problem.Code == nil || *r.problem.Code != codePersonErased):
			t.Errorf("%s = %v, want the 409 person_erased", c.name, err)
		case c.refused && ran:
			t.Errorf("%s: fn ran", c.name)
		case !c.refused && (err != nil || !ran):
			t.Errorf("%s = %v (ran %v), want fn run", c.name, err, ran)
		}
	}
}
