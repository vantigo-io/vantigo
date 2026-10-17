package identity_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/identity"
)

// employeeNumberPairs are spellings a caller's record and identity could
// hold for one number, or for two: case in Latin, in ß/ẞ and in the ǆ
// digraph's three forms, precomposed against combining marks, surrounding
// space, the Kelvin sign, letters a C library's upper() maps unlike Go, and
// plainly different numbers.
var employeeNumberPairs = []struct{ a, b string }{
	{"æ-1", "Æ-1"},
	{"ß-1", "ẞ-1"},
	{"straße", "STRASSE"},
	{"ǆ-1", "Ǆ-1"},
	{"ǅ-1", "ǆ-1"},
	{"é-1", "é-1"},
	{"Å-1", "Å-1"},
	{" E-1", "E-1"},
	{"K-1", "k-1"},
	// Where a C library's upper() and Go's case mapping disagree: musl's
	// (the CI image's) leaves circled ⓐ and ɤ as they are, glibc's leaves
	// U+A7D5 — only the key identity computes in Go may decide.
	{"\u24d0-1", "\u24b6-1"},
	{"\u0264-1", "\ua7cb-1"},
	{"\ua7d5-1", "\ua7d4-1"},
	{"E-1", "E-2"},
	{"æ-1", "ae-1"},
}

// TestEmployeeNumbers_IdentityAndTheContractAgree is the differential test
// for the employee number's one comparison: for every pair, a second user
// adopted with b after a first with a is refused employee_number_taken
// exactly when contracts.SameEmployeeNumber(a, b) — so a caller that acts on
// a user only when the numbers are the same never acts on one identity's
// unique index tells apart, nor misses one it holds as the same. The
// numbers carry the pair's index, so no pair collides with another's.
func TestEmployeeNumbers_IdentityAndTheContractAgree(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)

	for i, pair := range employeeNumberPairs {
		a, b := fmt.Sprintf("%d%s", i, pair.a), fmt.Sprintf("%d%s", i, pair.b)
		first := h.insertUser(t, fmt.Sprintf("first-%d@example.test", i), identity.RoleUserID)
		second := h.insertUser(t, fmt.Sprintf("second-%d@example.test", i), identity.RoleUserID)
		mustManage(t, owner, first, a)

		r := manage(owner, second, b)
		refused := r.status == http.StatusConflict && r.code() == "employee_number_taken"
		if !refused && r.status != http.StatusOK {
			t.Fatalf("%q then %q: status %d code %q, want 200 or 409 employee_number_taken", a, b, r.status, r.code())
		}
		if same := contracts.SameEmployeeNumber(a, b); refused != same {
			t.Errorf("%q then %q: identity refused the second = %v, SameEmployeeNumber = %v", a, b, refused, same)
		}
		// Past the user-management limit's window before the next pair.
		h.advance(time.Minute)
	}
}
