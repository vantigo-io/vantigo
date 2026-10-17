package identity_test

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/identity"
)

// employeeNumberPairs are spellings a caller's record and identity could
// hold for one number, or for two: case, surrounding space and plainly
// different numbers in Staff's alphabet; and, outside it, case in ß/ẞ and in
// the ǆ digraph's three forms, precomposed against combining marks, the
// Kelvin sign and letters a C library's upper() maps unlike Go — which
// identity now refuses outright, so no Unicode case mapping ever decides.
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
	{"e-1", "E-1"},
	{"abc-9", "ABC-9"},
	{"E-1", "E-01"},
	{"E-1 ", "e-1"},
}

// numbered is n with i put before its first character that is not a space,
// so a pair's surrounding space survives the index.
func numbered(i int, n string) string {
	trimmed := strings.TrimLeft(n, " ")
	return n[:len(n)-len(trimmed)] + fmt.Sprint(i) + trimmed
}

// staffAlphabet is Staff's own rule for an employee number once trimmed
// (staff/values.go's numberPattern, at most 16 characters): the only
// numbers identity takes.
var staffAlphabet = regexp.MustCompile(`^[A-Za-z0-9-]{1,16}$`)

// TestEmployeeNumbers_IdentityAndTheContractAgree is the differential test
// for the employee number's one comparison: for every pair in Staff's
// alphabet, a second user adopted with b after a first with a is refused
// employee_number_taken exactly when contracts.SameEmployeeNumber(a, b) — so
// a caller that acts on a user only when the numbers are the same never acts
// on one identity's unique index tells apart, nor misses one it holds as the
// same. A number outside the alphabet is refused 400 invalid_request and
// stored nowhere. The numbers carry the pair's index, so no pair collides
// with another's.
func TestEmployeeNumbers_IdentityAndTheContractAgree(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	takes := func(n string) bool { return staffAlphabet.MatchString(strings.TrimSpace(n)) }

	for i, pair := range employeeNumberPairs {
		a, b := numbered(i, pair.a), numbered(i, pair.b)
		first := h.insertUser(t, fmt.Sprintf("first-%d@example.test", i), identity.RoleUserID)
		second := h.insertUser(t, fmt.Sprintf("second-%d@example.test", i), identity.RoleUserID)
		if !takes(a) {
			if r := manage(owner, first, a); r.status != http.StatusBadRequest || r.code() != "invalid_request" {
				t.Errorf("%q: status %d code %q, want 400 invalid_request", a, r.status, r.code())
			}
			h.advance(time.Minute)
			continue
		}
		mustManage(t, owner, first, a)

		r := manage(owner, second, b)
		if !takes(b) {
			if r.status != http.StatusBadRequest || r.code() != "invalid_request" {
				t.Errorf("%q: status %d code %q, want 400 invalid_request", b, r.status, r.code())
			}
			h.advance(time.Minute)
			continue
		}
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
	if n := h.count(t, `SELECT count(*) FROM identity.users WHERE employee_number !~ '^[A-Za-z0-9-]{1,16}$'`); n != 0 {
		t.Errorf("%d users hold a number outside Staff's alphabet", n)
	}
}
