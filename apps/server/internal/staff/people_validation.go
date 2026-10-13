package staff

import (
	"fmt"
	"net/mail"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is a person's value rules (the plan's "Exact values"): what
// PersonRequest may carry, each field judged on its own and every failure
// collected, and how a valid body becomes the row it writes.

// The widths of a person's columns, in characters.
const (
	personNameMaxLength  = 100
	displayNameMaxLength = 200
	emailMaxLength       = 254
	phoneMaxLength       = 32
	notesMaxLength       = 2000
)

// The values an enumerated field takes.
var (
	genders   = []string{"female", "male", "other", "unspecified"}
	languages = []string{"en", "nb"}
)

// parsedPerson is one validated PersonRequest. A nil pointer is a field left
// out: cleared on a change, except EmployeeNumber (generated on a create,
// never changed) and DisplayName (derived on a create, kept on a change).
type parsedPerson struct {
	EmployeeNumber    *string
	GivenName         string
	FamilyName        string
	DisplayName       *string
	BirthDate         pgtype.Date
	Gender            *string
	WorkEmail         *string
	PhoneWork         *string
	PhoneMobile       *string
	PhonePrivate      *string
	PrivateEmail      *string
	Street            *string
	PostalCode        *string
	City              *string
	Country           *string
	WorkCountry       string
	PreferredLanguage string
	Notes             *string
	// Care is the care-day fields (delivery B, absence_settings.go), cleared
	// when left out as every optional field is.
	Care careDays
}

// parsePerson runs every field's rule over a body. creating is whether the
// body creates the person: then a display name left out is derived from the
// names, and must fit.
func parsePerson(body gen.StaffPersonRequest, creating bool) (parsedPerson, fieldErrors) {
	var f fieldErrors
	var out parsedPerson
	var msg string

	if body.EmployeeNumber != nil {
		var code string
		out.EmployeeNumber, code, msg = parseEmployeeNumber(*body.EmployeeNumber)
		f.addCoded("employeeNumber", code, msg)
	}
	out.GivenName, msg = requiredText("A given name", body.GivenName, personNameMaxLength)
	f.add("givenName", msg)
	out.FamilyName, msg = requiredText("A family name", body.FamilyName, personNameMaxLength)
	f.add("familyName", msg)
	out.DisplayName, msg = optionalText("A display name", body.DisplayName, displayNameMaxLength)
	f.add("displayName", msg)
	if creating && body.DisplayName == nil && out.GivenName != "" && out.FamilyName != "" {
		derived := out.GivenName + " " + out.FamilyName
		if n := utf8.RuneCountInString(derived); n > displayNameMaxLength {
			f.add("displayName", fmt.Sprintf("The given and family names make a display name of %d characters, longer than %d; give a display name", n, displayNameMaxLength))
		}
		out.DisplayName = &derived
	}

	out.BirthDate = pgDate(civilDate(body.BirthDate))
	if body.Gender != nil {
		out.Gender, msg = oneOf("A gender", string(*body.Gender), genders)
		f.add("gender", msg)
	}
	out.WorkEmail, msg = parseEmail("A work email", body.WorkEmail)
	f.add("workEmail", msg)
	out.PrivateEmail, msg = parseEmail("A private email", body.PrivateEmail)
	f.add("privateEmail", msg)
	out.PhoneWork, msg = optionalText("A work phone", body.PhoneWork, phoneMaxLength)
	f.add("phoneWork", msg)
	out.PhoneMobile, msg = optionalText("A mobile phone", body.PhoneMobile, phoneMaxLength)
	f.add("phoneMobile", msg)
	out.PhonePrivate, msg = optionalText("A private phone", body.PhonePrivate, phoneMaxLength)
	f.add("phonePrivate", msg)
	out.Street, msg = optionalText("A street", body.Street, streetMaxLength)
	f.add("street", msg)
	out.PostalCode, msg = optionalText("A postal code", body.PostalCode, postalCodeMaxLength)
	f.add("postalCode", msg)
	out.City, msg = optionalText("A city", body.City, cityMaxLength)
	f.add("city", msg)
	if body.Country != nil {
		var country string
		country, msg = parseCountry(body.Country, "")
		f.add("country", msg)
		out.Country = &country
	}
	out.WorkCountry, msg = parseCountry(body.WorkCountry, "NO")
	f.add("workCountry", msg)
	out.PreferredLanguage = "nb"
	if body.PreferredLanguage != nil {
		var language *string
		language, msg = oneOf("A language", string(*body.PreferredLanguage), languages)
		f.add("preferredLanguage", msg)
		if language != nil {
			out.PreferredLanguage = *language
		}
	}
	out.Notes, msg = optionalText("Notes", body.Notes, notesMaxLength)
	f.add("notes", msg)
	out.Care = parseCareDays(body, &f)
	return out, f
}

// parseEmployeeNumber is a typed employee number: trimmed, 1 to 16 letters
// a–z, digits or hyphens, kept as cased — uniqueness, however it is cased, is
// the index's. Longer than 16 is the coded rule employee_number_too_long.
func parseEmployeeNumber(raw string) (*string, string, string) {
	v := strings.TrimSpace(raw)
	switch n := utf8.RuneCountInString(v); {
	case n == 0:
		return nil, "", "An employee number cannot be blank; leave it out to have one generated"
	case n > employeeNumberMax:
		return nil, codeEmployeeNumberTooLong, fmt.Sprintf("An employee number is at most %d characters, the given value was %d", employeeNumberMax, n)
	case !numberPattern.MatchString(v):
		return nil, "", "An employee number may hold only letters a–z, digits and hyphens"
	}
	return &v, "", ""
}

// parseEmail is an optional email address: trimmed, at most 254 characters,
// an RFC 5322 addr-spec alone — no display name, no angle brackets — and
// stored lower-cased, so the uniqueness of a work email is judged the way a
// person reads one.
func parseEmail(label string, raw *string) (*string, string) {
	v, msg := optionalText(label, raw, emailMaxLength)
	if v == nil || msg != "" {
		return nil, msg
	}
	addr, err := mail.ParseAddress(*v)
	if err != nil || addr.Name != "" || addr.Address != *v || !strings.Contains(addr.Address, "@") {
		return nil, fmt.Sprintf("%s must be an address such as name@example.no", label)
	}
	lower := strings.ToLower(*v)
	return &lower, ""
}

// oneOf is an enumerated value: one of allowed, as sent.
func oneOf(label, v string, allowed []string) (*string, string) {
	for _, a := range allowed {
		if v == a {
			return &v, ""
		}
	}
	return nil, fmt.Sprintf("%s is one of %s, but was '%s'", label, strings.Join(allowed, ", "), v)
}

// applyTo is the row p makes of current: every editable field replaced, the
// display name kept when p leaves it out, the employee number never touched.
func (p parsedPerson) applyTo(current store.GetPersonRow) store.GetPersonRow {
	next := current
	next.GivenName = p.GivenName
	next.FamilyName = p.FamilyName
	if p.DisplayName != nil {
		next.DisplayName = *p.DisplayName
	}
	next.BirthDate = p.BirthDate
	next.Gender = p.Gender
	next.WorkEmail = p.WorkEmail
	next.PhoneWork = p.PhoneWork
	next.PhoneMobile = p.PhoneMobile
	next.PhonePrivate = p.PhonePrivate
	next.PrivateEmail = p.PrivateEmail
	next.Street = p.Street
	next.PostalCode = p.PostalCode
	next.City = p.City
	next.Country = p.Country
	next.WorkCountry = p.WorkCountry
	next.PreferredLanguage = p.PreferredLanguage
	next.Notes = p.Notes
	next.ChildrenUnder12 = p.Care.ChildrenUnder12
	next.SoleCarer = p.Care.SoleCarer
	next.ChronicallyIllChildren = p.Care.ChronicallyIllChildren
	return next
}

// personValues is a row's editable fields by the contract's names, each as
// text and nil when empty — what an event's payload records and what a
// change is judged against.
func personValues(row store.GetPersonRow) map[string]*string {
	var birth *string
	if row.BirthDate.Valid {
		v := row.BirthDate.Time.Format(time.DateOnly)
		birth = &v
	}
	text := func(v string) *string { return &v }
	return map[string]*string{
		"employeeNumber":    text(row.EmployeeNumber),
		"givenName":         text(row.GivenName),
		"familyName":        text(row.FamilyName),
		"displayName":       text(row.DisplayName),
		"birthDate":         birth,
		"gender":            row.Gender,
		"workEmail":         row.WorkEmail,
		"phoneWork":         row.PhoneWork,
		"phoneMobile":       row.PhoneMobile,
		"phonePrivate":      row.PhonePrivate,
		"privateEmail":      row.PrivateEmail,
		"street":            row.Street,
		"postalCode":        row.PostalCode,
		"city":              row.City,
		"country":           row.Country,
		"workCountry":       text(row.WorkCountry),
		"preferredLanguage": text(row.PreferredLanguage),
		"notes":             row.Notes,

		"childrenUnder12":        smallText(row.ChildrenUnder12),
		"soleCarer":              boolText(row.SoleCarer),
		"chronicallyIllChildren": smallText(row.ChronicallyIllChildren),
	}
}

// smallText is a nullable smallint as a payload's text, nil when NULL.
func smallText(v *int16) *string {
	if v == nil {
		return nil
	}
	out := strconv.Itoa(int(*v))
	return &out
}

// boolText is a nullable boolean as a payload's text, nil when NULL.
func boolText(v *bool) *string {
	if v == nil {
		return nil
	}
	out := strconv.FormatBool(*v)
	return &out
}

// diffPerson is every editable field that differs between before and after,
// with both values; before nil is a creation, whose every set field is a
// change from nothing.
func diffPerson(before *store.GetPersonRow, after store.GetPersonRow) changes {
	old := map[string]*string{}
	if before != nil {
		old = personValues(*before)
	}
	out := changes{}
	for name, to := range personValues(after) {
		from := old[name]
		if textEqual(from, to) {
			continue
		}
		out[name] = fieldChange{From: textValue(from), To: textValue(to)}
	}
	return out
}

func textEqual(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// textValue is v for a payload: absent (nil) when empty.
func textValue(v *string) any {
	if v == nil {
		return nil
	}
	return *v
}
