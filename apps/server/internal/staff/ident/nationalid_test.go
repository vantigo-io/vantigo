package ident_test

import (
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/staff/ident"
)

// nationalIDForTest builds an eleven-digit Norwegian national identity number,
// ported from customers' NationalIDForTest: birth is six digits, DDMMYY, and
// the individual number is the first one from individual up whose two mod-11
// check digits both exist. dNumber raises the first digit by four, which is
// all a d-nummer is.
//
// It is its own arithmetic, deliberately not the package's: a test that built
// its numbers with the function under test could not catch a wrong weight in
// it. Nothing built here may be a real person's number: the month is in a
// synthetic range — plus 40 or plus 80 — or, for a plain month, the year is
// 30–39 with an individual number of 500 or more, a birth in the 2030s that
// nobody alive can have yet.
func nationalIDForTest(birth string, individual int, dNumber bool) string {
	plainFuture := len(birth) == 6 && birth[2:4] <= "12" && birth[4] == '3' && individual >= 500
	if len(birth) != 6 || (birth[2:4] < "41" && !plainFuture) {
		panic("nationalIDForTest: " + birth + " is neither a synthetic birth date (the month plus 40 or 80) nor one in the 2030s")
	}
	firstWeights := []int{3, 7, 6, 1, 8, 9, 4, 5, 2}
	secondWeights := []int{5, 4, 3, 2, 7, 6, 5, 4, 3, 2}
	check := func(digits, weights []int) int {
		sum := 0
		for i, w := range weights {
			sum += digits[i] * w
		}
		return (11 - sum%11) % 11
	}
	for n := individual; n < 1000; n++ {
		digits := make([]int, 0, 11)
		for _, r := range fmt.Sprintf("%s%03d", birth, n) {
			digits = append(digits, int(r-'0'))
		}
		if dNumber {
			digits[0] += 4
		}
		first := check(digits, firstWeights)
		if first == 10 {
			continue
		}
		digits = append(digits, first)
		second := check(digits, secondWeights)
		if second == 10 {
			continue
		}
		digits = append(digits, second)
		var b strings.Builder
		for _, d := range digits {
			b.WriteByte(byte('0' + d))
		}
		return b.String()
	}
	panic("nationalIDForTest: no individual number from " + strconv.Itoa(individual) + " up gives two check digits for " + birth)
}

// bumpDigit returns s with the digit at i raised by one, modulo ten.
func bumpDigit(s string, i int) string {
	return s[:i] + strconv.Itoa((int(s[i]-'0')+1)%10) + s[i+1:]
}

func civil(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

func TestParseNationalID(t *testing.T) {
	t.Parallel()
	fnr := nationalIDForTest("018190", 100, false)
	dnr := nationalIDForTest("018190", 100, true)

	type want struct {
		value, country string
		birth          time.Time // zero: no birth date
	}
	for _, c := range []struct {
		name         string
		kind         ident.NationalIDKind
		country, raw string
		want         *want // nil: refused
	}{
		{"a fødselsnummer is accepted with its birth date", ident.Fodselsnummer, "", fnr, &want{fnr, "", civil(1990, 1, 1)}},
		{"a fødselsnummer written with a space is read as its digits", ident.Fodselsnummer, "", fnr[:6] + " " + fnr[6:], &want{fnr, "", civil(1990, 1, 1)}},
		{"a fødselsnummer written with a hyphen is read as its digits", ident.Fodselsnummer, "", fnr[:6] + "-" + fnr[6:], &want{fnr, "", civil(1990, 1, 1)}},
		{"a fødselsnummer with the country NO keeps no country", ident.Fodselsnummer, "no", fnr, &want{fnr, "", civil(1990, 1, 1)}},
		{"a fødselsnummer with another country is refused", ident.Fodselsnummer, "SE", fnr, nil},
		{"the first control digit wrong is refused", ident.Fodselsnummer, "", bumpDigit(fnr, 9), nil},
		{"the second control digit wrong is refused", ident.Fodselsnummer, "", bumpDigit(fnr, 10), nil},
		{"ten digits are refused", ident.Fodselsnummer, "", fnr[:10], nil},
		{"twelve digits are refused", ident.Fodselsnummer, "", fnr + "0", nil},
		{"a letter among the digits is refused", ident.Fodselsnummer, "", fnr[:10] + "x", nil},
		{"empty is refused", ident.Fodselsnummer, "", "", nil},
		{"31 April is refused", ident.Fodselsnummer, "", nationalIDForTest("318490", 100, false), nil},
		{"day zero is refused", ident.Fodselsnummer, "", nationalIDForTest("008190", 100, false), nil},
		{"month thirteen in the synthetic range is refused", ident.Fodselsnummer, "", nationalIDForTest("019390", 100, false), nil},
		{"29 February 1900 is refused: individual 000-499 is the 1900s", ident.Fodselsnummer, "", nationalIDForTest("298200", 100, false), nil},
		{"29 February 2000 is accepted: individual 500-999 with year 00-39 is the 2000s", ident.Fodselsnummer, "", nationalIDForTest("298200", 600, false), &want{nationalIDForTest("298200", 600, false), "", civil(2000, 2, 29)}},
		{"individual 500-749 with year 54-99 is the 1800s", ident.Fodselsnummer, "", nationalIDForTest("018190", 600, false), &want{nationalIDForTest("018190", 600, false), "", civil(1890, 1, 1)}},
		{"individual 900-999 with year 40-99 is the 1900s", ident.Fodselsnummer, "", nationalIDForTest("018190", 950, false), &want{nationalIDForTest("018190", 950, false), "", civil(1990, 1, 1)}},
		{"individual 500-999 with year 10 is the 2000s", ident.Fodselsnummer, "", nationalIDForTest("018110", 600, false), &want{nationalIDForTest("018110", 600, false), "", civil(2010, 1, 1)}},
		{"individual 750-899 with year 40-99 is no century and refused", ident.Fodselsnummer, "", nationalIDForTest("018190", 800, false), nil},
		{"individual 500-749 with year 40-53 is no century and refused", ident.Fodselsnummer, "", nationalIDForTest("018145", 600, false), nil},
		{"individual 500-749 with year 53 is no century and refused", ident.Fodselsnummer, "", nationalIDForTest("018153", 600, false), nil},
		{"individual 500-749 with year 54 is 1854", ident.Fodselsnummer, "", nationalIDForTest("018154", 600, false), &want{nationalIDForTest("018154", 600, false), "", civil(1854, 1, 1)}},
		{"a plain month is accepted in a fødselsnummer", ident.Fodselsnummer, "", nationalIDForTest("010139", 500, false), &want{nationalIDForTest("010139", 500, false), "", civil(2039, 1, 1)}},
		{"a plain month is accepted in a d-nummer", ident.DNummer, "", nationalIDForTest("311239", 500, true), &want{nationalIDForTest("311239", 500, true), "", civil(2039, 12, 31)}},
		{"the month plus 40 is accepted", ident.Fodselsnummer, "", nationalIDForTest("014190", 100, false), &want{nationalIDForTest("014190", 100, false), "", civil(1990, 1, 1)}},
		{"the month plus 80 is accepted", ident.Fodselsnummer, "", nationalIDForTest("018290", 100, false), &want{nationalIDForTest("018290", 100, false), "", civil(1990, 2, 1)}},
		{"a d-nummer is accepted with its birth date", ident.DNummer, "", dnr, &want{dnr, "", civil(1990, 1, 1)}},
		{"a d-nummer on the 31st is accepted", ident.DNummer, "", nationalIDForTest("318190", 100, true), &want{nationalIDForTest("318190", 100, true), "", civil(1990, 1, 31)}},
		{"a d-nummer given as a fødselsnummer is refused", ident.Fodselsnummer, "", dnr, nil},
		{"a fødselsnummer given as a d-nummer is refused", ident.DNummer, "", fnr, nil},
		{"a foreign id is upper-cased with its country", ident.Foreign, "se", " ab-123 ", &want{"AB-123", "SE", time.Time{}}},
		{"a foreign id of 40 characters is accepted", ident.Foreign, "DE", strings.Repeat("A", 40), &want{strings.Repeat("A", 40), "DE", time.Time{}}},
		{"a foreign id of 41 characters is refused", ident.Foreign, "DE", strings.Repeat("A", 41), nil},
		{"a foreign id needs a country", ident.Foreign, "", "AB-123", nil},
		{"a foreign id's country is two letters", ident.Foreign, "S1", "AB-123", nil},
		{"a foreign id's country of one letter is refused", ident.Foreign, "S", "AB-123", nil},
		{"an empty foreign id is refused", ident.Foreign, "SE", "  ", nil},
		{"a space inside a foreign id is refused", ident.Foreign, "SE", "AB 123", nil},
		{"a letter outside A-Z in a foreign id is refused", ident.Foreign, "SE", "ÆB-123", nil},
		{"a long s that upper-cases to S is refused", ident.Foreign, "SE", "ſB-123", nil},
		{"a dotless i that upper-cases to I is refused", ident.Foreign, "SE", "ıB-123", nil},
		{"a country's long s that upper-cases to S is refused", ident.Foreign, "ſE", "AB-123", nil},
		{"a foreign id with the country NO is refused: Norway's numbers have their own kinds", ident.Foreign, "no", "AB-123", nil},
		{"a fødselsnummer as a foreign id of Norway is refused", ident.Foreign, "NO", fnr, nil},
		{"an unknown kind is refused", ident.NationalIDKind("passport"), "", fnr, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, err := ident.ParseNationalID(c.kind, c.country, c.raw)
			if c.want == nil {
				if !errors.Is(err, ident.ErrInvalidNationalID) {
					t.Fatalf("ParseNationalID(%s, %q, …) error = %v, want ErrInvalidNationalID", c.kind, c.country, err)
				}
				if strings.ContainsAny(err.Error(), "0123456789") {
					t.Errorf("error %q carries a digit; a refusal names no digit", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseNationalID(%s, %q, …) = %v, want accepted", c.kind, c.country, err)
			}
			if got.Kind() != c.kind || got.Value() != c.want.value || got.Country() != c.want.country {
				t.Errorf("= kind %s, value %q, country %q; want %s, %q, %q", got.Kind(), got.Value(), got.Country(), c.kind, c.want.value, c.want.country)
			}
			birth, ok := got.BirthDate()
			if ok != !c.want.birth.IsZero() || !birth.Equal(c.want.birth) {
				t.Errorf("BirthDate() = %v, %v; want %v", birth, ok, c.want.birth)
			}
		})
	}
}

// Every weight is between 1 and 9 and 11 is prime, so changing any one digit of
// a valid number moves the check it takes part in (ported from customers'
// TestIsNorwegianNationalID_AnyOneDigitChangedIsNotOne).
func TestParseNationalID_AnyOneDigitChangedIsRefused(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		kind  ident.NationalIDKind
		valid string
	}{
		{ident.Fodselsnummer, nationalIDForTest("298292", 400, false)},
		{ident.DNummer, nationalIDForTest("318199", 7, true)},
	} {
		if _, err := ident.ParseNationalID(c.kind, "", c.valid); err != nil {
			t.Fatalf("ParseNationalID(%s, %q) = %v, want accepted", c.kind, c.valid, err)
		}
		for i := range c.valid {
			for d := byte('0'); d <= '9'; d++ {
				if d == c.valid[i] {
					continue
				}
				changed := c.valid[:i] + string(d) + c.valid[i+1:]
				if _, err := ident.ParseNationalID(c.kind, "", changed); err == nil {
					t.Errorf("ParseNationalID(%s, %q) accepted: digit %d changed from %c to %c", c.kind, changed, i, c.valid[i], d)
				}
			}
		}
	}
}

// The MAC input is kind | country | value, so the same characters under two
// kinds index apart, and its form is pinned: changing it orphans every MAC
// already stored.
func TestIndexInput(t *testing.T) {
	t.Parallel()
	fnr := nationalIDForTest("018190", 100, false)
	asFnr, err := ident.ParseNationalID(ident.Fodselsnummer, "", fnr)
	if err != nil {
		t.Fatal(err)
	}
	asForeign, err := ident.ParseNationalID(ident.Foreign, "SE", fnr)
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := ident.ParseNationalID(ident.Foreign, "se", "ab-123")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		n    ident.Normalised
		want string
	}{
		{"a fødselsnummer has an empty country", asFnr, "fodselsnummer||" + fnr},
		{"the same digits as a foreign id carry kind and country", asForeign, "foreign|SE|" + fnr},
		{"a foreign id is indexed normalised", foreign, "foreign|SE|AB-123"},
	} {
		if got := string(ident.IndexInput(c.n)); got != c.want {
			t.Errorf("%s: IndexInput = %q, want %q", c.name, got, c.want)
		}
	}
	if string(ident.IndexInput(asFnr)) == string(ident.IndexInput(asForeign)) {
		t.Error("the same digits under two kinds give the same index input")
	}
}

// A parsed number never prints its digits by accident: fmt and slog both see
// a redaction, and only Value hands them out.
func TestNormalised_NeverPrintsTheValue(t *testing.T) {
	t.Parallel()
	fnr := nationalIDForTest("018190", 100, false)
	n, err := ident.ParseNationalID(ident.Fodselsnummer, "", fnr)
	if err != nil {
		t.Fatal(err)
	}
	var logged strings.Builder
	slog.New(slog.NewTextHandler(&logged, nil)).Info("test", slog.Any("id", n))
	outputs := map[string]string{"slog": logged.String()}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s"} {
		outputs[verb] = fmt.Sprintf(verb, n)
	}
	for name, out := range outputs {
		if strings.Contains(out, fnr[6:]) || !strings.Contains(out, "redacted") {
			t.Errorf("%s: %q, want a redaction without the digits", name, out)
		}
	}

	// In an unexported field of another struct fmt does not call Format: it
	// reflects into the value, and must find nothing there but a pointer.
	type request struct{ id ident.Normalised }
	var deep strings.Builder
	slog.New(slog.NewTextHandler(&deep, nil)).Info("test", slog.Any("request", request{n}))
	nested := map[string]string{"nested slog text": deep.String()}
	for _, verb := range []string{"%v", "%+v", "%#v"} {
		nested["nested "+verb] = fmt.Sprintf(verb, request{n})
	}
	for name, out := range nested {
		if strings.Contains(out, fnr) || strings.Contains(out, fnr[6:]) || strings.Contains(out, "fodselsnummer") {
			t.Errorf("%s: %q carries the number", name, out)
		}
	}
}
