package staff

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// The database's own refusals under a race — two writes that both passed
// their checks — answer as the rules they back: a unique index on the
// employee number or the work email is that field's code, a foreign key
// naming the person on a delete is person_in_use; anything else is the
// error it was.
func TestPersonWriteErrors_MapTheIndexesToTheirCodes(t *testing.T) {
	t.Parallel()
	pgErr := func(code, constraint string) error {
		return fmt.Errorf("wrapped: %w", &pgconn.PgError{Code: code, ConstraintName: constraint})
	}
	for _, c := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"employee number", pgErr("23505", "ux_people_employee_number_upper"), http.StatusBadRequest, codeEmployeeNumberTaken},
		{"work email", pgErr("23505", "ux_people_work_email_lower"), http.StatusBadRequest, codeWorkEmailTaken},
	} {
		r, ok := asRefusal(personWriteError(c.err, "Invalid person"))
		if !ok || r.status != c.status || r.problem.Code == nil || *r.problem.Code != c.code {
			t.Errorf("%s: %+v, want %d %s", c.name, r, c.status, c.code)
		}
	}
	r, ok := asRefusal(personDeleteError(pgErr("23503", "employment_terms_manager_person_id_fkey")))
	if !ok || r.status != http.StatusConflict || r.problem.Code == nil || *r.problem.Code != codePersonInUse {
		t.Errorf("a foreign key on delete: %+v, want 409 person_in_use", r)
	}
	other := errors.New("boom")
	if err := personWriteError(other, "Invalid person"); !errors.Is(err, other) {
		t.Errorf("another error = %v, want it unchanged", err)
	}
	if err := personDeleteError(other); !errors.Is(err, other) {
		t.Errorf("another error on delete = %v, want it unchanged", err)
	}
	if err := personWriteError(nil, "Invalid person"); err != nil {
		t.Errorf("nil = %v, want nil", err)
	}
}
