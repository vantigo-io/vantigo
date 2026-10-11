// Package ident holds the identifier checks the Staff module needs: the
// Norwegian national identity number (fødselsnummer and d-nummer) and a
// foreign one, the bank account (BBAN and IBAN) and the organisation number.
//
// It is a leaf of the standard library only (staff plan reading 7): the checks
// are ported from customers' and invoices' validators rather than imported,
// because a module may not import another, and they are not promoted to a
// platform package while Staff is their only caller beyond those two. Nothing
// here reads the clock, the store or the configuration, and no error it
// returns carries a digit of what it was given: a refusal of a national id
// must never echo the number it refused.
package ident

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode"
)

// NationalIDKind is the kind of a person's national identity number, as the
// contract and the people table spell it.
type NationalIDKind string

const (
	// Fodselsnummer is the Norwegian birth number of a resident.
	Fodselsnummer NationalIDKind = "fodselsnummer"
	// DNummer is the Norwegian number of a non-resident: a fødselsnummer whose
	// first digit is raised by four.
	DNummer NationalIDKind = "d_nummer"
	// Foreign is another country's identifier, kept with that country.
	Foreign NationalIDKind = "foreign"
)

// ErrInvalidNationalID is every refusal of ParseNationalID, wrapped with a
// reason that names no digit. Callers answer it as national_id_invalid.
var ErrInvalidNationalID = errors.New("not a valid national identity number")

// maxForeignIDLength is the longest foreign identifier kept.
const maxForeignIDLength = 40

// Normalised is a national identity number that passed ParseNationalID, in
// the form it is sealed and indexed in; Value hands the number out. It prints
// as a redaction under fmt and log/slog, so a stray log line cannot carry the
// number. Format and LogValue only cover a Normalised printed at the top, or
// in an exported field: in an unexported field of another struct fmt reflects
// into the value without calling either. The fields are therefore held behind
// a pointer, which fmt prints below the top level as an address and never
// follows. The zero value is no number: every accessor answers empty.
type Normalised struct{ p *normalised }

type normalised struct {
	kind    NationalIDKind
	country string    // upper-case alpha-2 for a foreign id, empty for a Norwegian one
	value   string    // eleven digits, or the upper-cased foreign id
	birth   time.Time // the birth date a Norwegian number encodes; zero for a foreign one
}

// fields is the number's fields, or the zero fields for the zero Normalised.
func (n Normalised) fields() normalised {
	if n.p == nil {
		return normalised{}
	}
	return *n.p
}

// Kind is the number's kind.
func (n Normalised) Kind() NationalIDKind { return n.fields().kind }

// Country is a foreign id's upper-case alpha-2 country, and empty for a
// fødselsnummer or d-nummer, whose country is the kind itself.
func (n Normalised) Country() string { return n.fields().country }

// Value is the normalised number: eleven digits for a Norwegian number, the
// upper-cased identifier for a foreign one. It is what is sealed.
func (n Normalised) Value() string { return n.fields().value }

// BirthDate is the birth date a fødselsnummer or d-nummer encodes, as a civil
// date (midnight UTC), so it can be compared with a birth date typed beside
// it. A foreign id encodes none and answers false.
func (n Normalised) BirthDate() (time.Time, bool) {
	birth := n.fields().birth
	return birth, !birth.IsZero()
}

// Format writes a redaction for every verb, so fmt never prints the number.
func (n Normalised) Format(f fmt.State, _ rune) {
	_, _ = fmt.Fprintf(f, "ident.Normalised{%s, redacted}", n.Kind())
}

// LogValue keeps log/slog handlers from reflecting into the number.
func (n Normalised) LogValue() slog.Value {
	return slog.StringValue("ident.Normalised{" + string(n.Kind()) + ", redacted}")
}

// IndexInput is the domain-separated input of the national id's MAC:
// kind | country | value. The kind and the country keep the same characters
// under two kinds — eleven digits as a fødselsnummer and as a foreign id of
// another country — from indexing alike, and "|" occurs in none of the three.
// Its form is part of every MAC stored; changing it orphans them all.
func IndexInput(n Normalised) []byte {
	f := n.fields()
	return []byte(string(f.kind) + "|" + f.country + "|" + f.value)
}

// ParseNationalID checks raw as a national identity number of kind and returns
// it normalised.
//
// A fødselsnummer or a d-nummer is eleven digits once whitespace, hyphens and
// full stops are taken out ("010190 12345" is how people write one); the
// country, if given, must be NO and is not kept. Both mod-11 control digits
// must hold (weights 3 7 6 1 8 9 4 5 2 and 5 4 3 2 7 6 5 4 3 2, a remainder
// that would need the digit 10 refused), and the first six digits must be a
// date: a d-nummer's day is raised by 40 and a fødselsnummer's is not; the
// month may be raised by 40 (H-numbers and Dolly's synthetic persons) or by
// 80 (Skatteetaten's synthetic test persons, staff spec reading 24); the
// century is read from the individual number, so 29 February is judged in the
// year it names.
//
// This is today's rule. Skatteetaten has announced a new format for numbers
// issued from 2032 that relaxes the first control digit (several remainders
// pass, so it is no longer unique); once that rule is final this check must
// widen to it, as customers' refusal must.
//
// A foreign id is 1–40 characters of A–Z, 0–9 and the hyphen, upper-cased,
// with an alpha-2 country, also upper-cased, that is not NO: Norway's own
// numbers are a fødselsnummer or a d-nummer, and one kept again as a foreign
// id of Norway would index apart from itself and slip past the duplicate check.
func ParseNationalID(kind NationalIDKind, country, raw string) (Normalised, error) {
	switch kind {
	case Fodselsnummer, DNummer:
		return parseNorwegian(kind, country, raw)
	case Foreign:
		return parseForeign(country, raw)
	default:
		return Normalised{}, fmt.Errorf("%w: an unknown kind", ErrInvalidNationalID)
	}
}

// nationalIDFirstWeights and nationalIDSecondWeights are the two weight rows
// of the control digits: the tenth digit over the first nine, the eleventh
// over the first ten (ported from customers/values.go).
var (
	nationalIDFirstWeights  = []int{3, 7, 6, 1, 8, 9, 4, 5, 2}
	nationalIDSecondWeights = []int{5, 4, 3, 2, 7, 6, 5, 4, 3, 2}
)

// nationalIDCheckDigit is one mod-11 control digit over the leading digits, or
// -1 when the remainder would make it 10: no number carries that digit.
func nationalIDCheckDigit(digits string, weights []int) int {
	sum := 0
	for i, w := range weights {
		sum += int(digits[i]-'0') * w
	}
	switch check := 11 - sum%11; check {
	case 11:
		return 0
	case 10:
		return -1
	default:
		return check
	}
}

func parseNorwegian(kind NationalIDKind, country, raw string) (Normalised, error) {
	if c := strings.TrimSpace(country); c != "" && !strings.EqualFold(c, "NO") {
		return Normalised{}, fmt.Errorf("%w: a Norwegian number is Norway's", ErrInvalidNationalID)
	}
	digits := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || r == '-' || r == '.' {
			return -1
		}
		return r
	}, raw)
	if len(digits) != 11 || !allDigits(digits) {
		return Normalised{}, fmt.Errorf("%w: not eleven digits", ErrInvalidNationalID)
	}
	if first := nationalIDCheckDigit(digits[:9], nationalIDFirstWeights); first < 0 || int(digits[9]-'0') != first {
		return Normalised{}, fmt.Errorf("%w: the first control digit does not hold", ErrInvalidNationalID)
	}
	if second := nationalIDCheckDigit(digits[:10], nationalIDSecondWeights); second < 0 || int(digits[10]-'0') != second {
		return Normalised{}, fmt.Errorf("%w: the second control digit does not hold", ErrInvalidNationalID)
	}
	birth, ok := norwegianBirthDate(kind, digits)
	if !ok {
		return Normalised{}, fmt.Errorf("%w: the digits name no birth date", ErrInvalidNationalID)
	}
	return Normalised{&normalised{kind: kind, value: digits, birth: birth}}, nil
}

// norwegianBirthDate reads the birth date from eleven checked digits: DDMMYY,
// then the three-digit individual number that fixes the century.
func norwegianBirthDate(kind NationalIDKind, digits string) (time.Time, bool) {
	day := twoDigits(digits[0:2])
	if kind == DNummer {
		// A d-nummer is the only one whose day is raised; a fødselsnummer
		// with a day of 41 or more is a d-nummer given as the wrong kind.
		if day < 41 {
			return time.Time{}, false
		}
		day -= 40
	}
	month := twoDigits(digits[2:4])
	switch {
	case month >= 81:
		month -= 80
	case month >= 41:
		month -= 40
	}
	yy := twoDigits(digits[4:6])
	individual := 100*int(digits[6]-'0') + twoDigits(digits[7:9])

	// The register's allocation of individual numbers to centuries:
	// 000–499 the 1900s; 500–999 with a year of 00–39 the 2000s; 500–749 with
	// 54–99 the 1800s; 900–999 with 40–99 the 1900s. What is left — 500–749
	// with 40–53, 750–899 with 40–99 — is allocated to no century, and a
	// number in it has no birth date to agree with.
	var century int
	switch {
	case individual <= 499:
		century = 1900
	case yy <= 39:
		century = 2000
	case individual <= 749 && yy >= 54:
		century = 1800
	case individual >= 900:
		century = 1900
	default:
		return time.Time{}, false
	}
	if month < 1 || month > 12 || day < 1 {
		return time.Time{}, false
	}
	birth := time.Date(century+yy, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	// time.Date normalises 31 April into 1 May; a date that moved was never one.
	if birth.Day() != day || int(birth.Month()) != month {
		return time.Time{}, false
	}
	return birth, true
}

// parseForeign checks the ASCII classes on the trimmed input before anything
// is upper-cased: strings.ToUpper maps some letters outside A–Z onto it (ſ to
// S, ı to I), so a check after it would let them through as their images.
func parseForeign(country, raw string) (Normalised, error) {
	c := strings.TrimSpace(country)
	if len(c) != 2 || !isASCIILetter(c[0]) || !isASCIILetter(c[1]) {
		return Normalised{}, fmt.Errorf("%w: a foreign id needs its country as two letters", ErrInvalidNationalID)
	}
	c = strings.ToUpper(c)
	if c == "NO" {
		return Normalised{}, fmt.Errorf("%w: a Norwegian number is a fødselsnummer or a d-nummer", ErrInvalidNationalID)
	}
	v := strings.TrimSpace(raw)
	if v == "" || len(v) > maxForeignIDLength {
		return Normalised{}, fmt.Errorf("%w: a foreign id is one to forty characters", ErrInvalidNationalID)
	}
	for i := 0; i < len(v); i++ {
		if !isASCIILetter(v[i]) && !isDigit(v[i]) && v[i] != '-' {
			return Normalised{}, fmt.Errorf("%w: a foreign id is letters, digits and hyphens", ErrInvalidNationalID)
		}
	}
	return Normalised{&normalised{kind: Foreign, country: c, value: strings.ToUpper(v)}}, nil
}

func twoDigits(s string) int { return 10*int(s[0]-'0') + int(s[1]-'0') }

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isUpperLetter(c byte) bool { return c >= 'A' && c <= 'Z' }

func isASCIILetter(c byte) bool { return isUpperLetter(c) || (c >= 'a' && c <= 'z') }

func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if !isDigit(s[i]) {
			return false
		}
	}
	return s != ""
}
