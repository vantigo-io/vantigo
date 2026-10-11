package staff_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// Every field of PersonRequest, judged on its own: a value that breaks the
// field's rule is a 400 naming that field (and carrying the rule's code when
// the design names one), and a value it accepts is stored as the rule
// normalises it — trimmed, emails lower-cased, countries upper-cased. Each
// case is a create from a valid body with one field replaced.
func TestPostPeople_ValidationPerField(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	long := func(n int) string { return strings.Repeat("a", n) }
	email := func(n int) string { return long(n-len("@example.no")) + "@example.no" }

	refusedCases := []struct {
		name  string
		field string
		value any
		code  string
	}{
		{"given name empty", "givenName", "", ""},
		{"given name blank", "givenName", "   ", ""},
		{"given name 101", "givenName", long(101), ""},
		{"family name empty", "familyName", "", ""},
		{"family name 101", "familyName", long(101), ""},
		{"display name blank", "displayName", "  ", ""},
		{"display name 201", "displayName", long(201), ""},
		{"employee number blank", "employeeNumber", " ", ""},
		{"employee number with a space", "employeeNumber", "a b", ""},
		{"employee number with a letter outside a-z", "employeeNumber", "Ø1", ""},
		{"employee number 17", "employeeNumber", "A1234567890123456", "employee_number_too_long"},
		{"gender outside the list", "gender", "x", ""},
		{"work email without a domain", "workEmail", "kari", ""},
		{"work email with a display name", "workEmail", "Kari <kari@example.no>", ""},
		{"work email 255", "workEmail", email(255), ""},
		{"work email blank", "workEmail", "  ", ""},
		{"private email invalid", "privateEmail", "kari@", ""},
		{"private email 255", "privateEmail", email(255), ""},
		{"work phone 33", "phoneWork", long(33), ""},
		{"mobile 33", "phoneMobile", long(33), ""},
		{"private phone 33", "phonePrivate", long(33), ""},
		{"street 201", "street", long(201), ""},
		{"postal code 17", "postalCode", long(17), ""},
		{"city 101", "city", long(101), ""},
		{"country unknown", "country", "XX", ""},
		{"work country unknown", "workCountry", "ZZ", ""},
		{"work country three letters", "workCountry", "NOR", ""},
		{"language outside the list", "preferredLanguage", "de", ""},
		{"notes 2001", "notes", long(2001), ""},
		{"notes blank", "notes", " ", ""},
	}
	for _, c := range refusedCases {
		p := refused(t, hr.Do(http.MethodPost, peoplePath, with(newPerson("Kari", "Nordmann"), c.field, c.value)), http.StatusBadRequest, c.code)
		if len(p.Errors[c.field]) == 0 {
			t.Errorf("%s: errors = %v, want %s named", c.name, p.Errors, c.field)
		}
	}
	// A date that is not one is not a request the server can decode.
	if r := hr.Do(http.MethodPost, peoplePath, with(newPerson("Kari", "Nordmann"), "birthDate", "2026-13-01")); r.Status != http.StatusBadRequest {
		t.Errorf("birthDate 2026-13-01: %d, want 400", r.Status)
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.people`); n != 0 {
		t.Errorf("people after refusals = %d, want 0", n)
	}
}

// The accepted values come back normalised.
func TestPostPeople_ValuesAreNormalised(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	p := createPerson(t, hr, map[string]any{
		"givenName": "  Kari ", "familyName": " Nordmann", "displayName": " Kari N ",
		"employeeNumber": "k-1", "birthDate": "1990-02-28", "gender": "female",
		"workEmail": " Kari.Nordmann@Example.NO", "privateEmail": "KARI@privat.no ",
		"phoneWork": " 22 00 00 00 ", "phoneMobile": "+47 900 00 000", "phonePrivate": "55 00 00 00",
		"street": " Storgata 1 ", "postalCode": " 0155 ", "city": " Oslo ", "country": "no",
		"workCountry": "se", "preferredLanguage": "en", "notes": " Starts in May ",
	})
	got := map[string]string{
		"givenName": p.GivenName, "familyName": p.FamilyName, "displayName": p.DisplayName,
		"employeeNumber": p.EmployeeNumber, "birthDate": p.BirthDate.String(), "gender": string(*p.Gender),
		"workEmail": *p.WorkEmail, "privateEmail": *p.PrivateEmail, "phoneWork": *p.PhoneWork,
		"phoneMobile": *p.PhoneMobile, "phonePrivate": *p.PhonePrivate, "street": *p.Street,
		"postalCode": *p.PostalCode, "city": *p.City, "country": *p.Country,
		"workCountry": p.WorkCountry, "preferredLanguage": string(p.PreferredLanguage), "notes": *p.Notes,
	}
	want := map[string]string{
		"givenName": "Kari", "familyName": "Nordmann", "displayName": "Kari N",
		"employeeNumber": "k-1", "birthDate": "1990-02-28", "gender": "female",
		"workEmail": "kari.nordmann@example.no", "privateEmail": "kari@privat.no", "phoneWork": "22 00 00 00",
		"phoneMobile": "+47 900 00 000", "phonePrivate": "55 00 00 00", "street": "Storgata 1",
		"postalCode": "0155", "city": "Oslo", "country": "NO",
		"workCountry": "SE", "preferredLanguage": "en", "notes": "Starts in May",
	}
	for field, w := range want {
		if got[field] != w {
			t.Errorf("%s = %q, want %q", field, got[field], w)
		}
	}
	stored := modtest.One[string](t, h.Harness, `SELECT work_email || ' ' || private_email FROM staff.people WHERE id = $1`, p.Id)
	if stored != "kari.nordmann@example.no kari@privat.no" {
		t.Errorf("stored emails = %q, want both lower-cased", stored)
	}
}
