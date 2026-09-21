package customers

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf16"
)

// This file ports the .NET Customers module's value objects
// (DM/Customers/Common/*.cs, DM/Customers/ValueObjects/LegalIdentity.cs,
// customers inventory §2.2): each a `TryCreate(raw) -> (normalized, error)`
// pair in .NET, kept here as a normalize-or-explain function rather than a
// wrapper type, since Go has no implicit conversions to make a wrapper type
// pull its weight. The exact error message text is the contract — this
// module has no machine-readable error codes at all (inventory §1) — so
// every string below is copied verbatim from the .NET source, not
// paraphrased.

// utf16Length is len(s) as .NET's string.Length counts it: UTF-16 code
// units, not runes. Duplicated from internal/identity/passwords.go's
// utf16Length: depguard forbids this module importing identity, and the
// function is four lines.
func utf16Length(s string) int {
	n := 0
	for _, r := range s {
		n += utf16.RuneLen(r)
	}
	return n
}

// validateFriendlyName is FriendlyName's Validate and constructor
// (DM/Customers/Common/FriendlyName.cs): non-blank, at most 255 UTF-16
// code units, trimmed but case-preserved.
func validateFriendlyName(raw string) (string, string) {
	if strings.TrimSpace(raw) == "" {
		return "", "A friendly name cannot be null or empty"
	}
	if n := utf16Length(raw); n > 255 {
		return "", fmt.Sprintf("A friendly name cannot be longer than 255 characters, the given value was %d characters", n)
	}
	return strings.TrimSpace(raw), ""
}

// validateCustomerStatus is CustomerStatus's Validate and constructor
// (DM/Customers/Common/CustomerStatus.cs): one of active/disabled/archived,
// case-insensitive, trimmed and lowercased. The "but was '{v}'" message
// quotes the raw, unnormalized value, as .NET's does.
func validateCustomerStatus(raw string) (string, string) {
	if strings.TrimSpace(raw) == "" {
		return "", "A customer status cannot be null or empty"
	}
	lower := strings.ToLower(strings.TrimSpace(raw))
	switch lower {
	case "active", "disabled", "archived":
		return lower, ""
	default:
		return "", fmt.Sprintf("A customer status must be one of 'active', 'disabled' or 'archived', but was '%s'", raw)
	}
}

// validateCustomerType is the customer type value object
// (00007_customers_type.sql): one of business/person, case-insensitive,
// trimmed and lowercased, shaped like validateCustomerStatus. Unlike
// validateLegalType below — a legacy convention that lets any non-blank
// value through — this is an enforced set, since the type drives the UI
// and the stats figures. The "but was '{v}'" message quotes the raw value.
func validateCustomerType(raw string) (string, string) {
	if strings.TrimSpace(raw) == "" {
		return "", "A customer type cannot be null or empty"
	}
	lower := strings.ToLower(strings.TrimSpace(raw))
	switch lower {
	case "business", "person":
		return lower, ""
	default:
		return "", fmt.Sprintf("A customer type must be one of 'business' or 'person', but was '%s'", raw)
	}
}

// identityTypeMismatch is the error under "identity.type" when a legal
// identity's type disagrees with the customer type it is attached to: a
// Brreg business identity on a private person, or a person identity on a
// business, is never a consistent record. Empty when they agree.
func identityTypeMismatch(customerType string, identity *legalIdentity) string {
	if identity == nil || identity.Type == customerType {
		return ""
	}
	return fmt.Sprintf("A legal identity's type must match the customer type '%s', but was '%s'", customerType, identity.Type)
}

// validateCountryCode is CountryCode's Validate and constructor
// (DM/Customers/Common/CountryCode.cs), extended by customers foundation
// design D2: non-blank, and — unlike .NET, which had no ISO-3166 shape
// check — must be an assigned ISO 3166-1 alpha-2 code (iso3166Alpha2,
// countries.go), case-insensitive, trimmed and lowercased. The "but was
// '{v}'" message quotes the raw, unnormalized value, as every other
// enforced-set validator in this file does.
func validateCountryCode(raw string) (string, string) {
	if strings.TrimSpace(raw) == "" {
		return "", "A country code cannot be null or empty"
	}
	lower := strings.ToLower(strings.TrimSpace(raw))
	if _, ok := iso3166Alpha2[lower]; !ok {
		return "", fmt.Sprintf("A country code must be an ISO 3166-1 alpha-2 code, but was '%s'", raw)
	}
	return lower, ""
}

// validNorwegianOrgNumber is the Brreg organisasjonsnummer mod-11 check
// customers foundation design D2 adds: digits must already be free of
// whitespace (validateLegalIdentity strips it before calling this). Exactly
// nine ASCII digits, the last of which is the mod-11 check digit computed
// over the first eight using weights 3 2 7 6 5 4 3 2. A remainder that
// would produce check digit 10 is invalid outright — nine digits have no
// way to encode a two-digit check value — regardless of what the ninth
// digit actually is.
func validNorwegianOrgNumber(digits string) bool {
	if len(digits) != 9 {
		return false
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return false
		}
	}
	weights := [8]int{3, 2, 7, 6, 5, 4, 3, 2}
	sum := 0
	for i, w := range weights {
		sum += int(digits[i]-'0') * w
	}
	check := 0
	if r := sum % 11; r != 0 {
		check = 11 - r
		if check == 10 {
			return false
		}
	}
	return int(digits[8]-'0') == check
}

// validateLegalID is LegalId's Validate and constructor
// (DM/Customers/Common/LegalId.cs): non-blank, at most 50 UTF-16 code
// units, trimmed and lowercased. Not format- or checksum-validated here —
// customers foundation design D2's Norwegian organisasjonsnummer check
// lives in validateLegalIdentity instead, since it only applies once the
// country and type are both known, never inside this generic, source-blind
// validator.
func validateLegalID(raw string) (string, string) {
	if strings.TrimSpace(raw) == "" {
		return "", "A legal id cannot be null or empty"
	}
	if n := utf16Length(raw); n > 50 {
		return "", fmt.Sprintf("A legal id cannot be longer than 50 characters, the given value was %d characters", n)
	}
	return strings.ToLower(strings.TrimSpace(raw)), ""
}

// validateLegalName is LegalName's Validate and constructor
// (DM/Customers/Common/LegalName.cs): non-blank, at most 255 UTF-16 code
// units, trimmed but case-preserved.
func validateLegalName(raw string) (string, string) {
	if strings.TrimSpace(raw) == "" {
		return "", "A legal name cannot be null or empty"
	}
	if n := utf16Length(raw); n > 255 {
		return "", fmt.Sprintf("A legal name cannot be longer than 255 characters, the given value was %d characters", n)
	}
	return strings.TrimSpace(raw), ""
}

// validateLegalSource is LegalSource's Validate and constructor
// (DM/Customers/Common/LegalSource.cs): one of brreg/manual,
// case-insensitive, trimmed and lowercased.
func validateLegalSource(raw string) (string, string) {
	if strings.TrimSpace(raw) == "" {
		return "", "A legal source cannot be null or empty"
	}
	lower := strings.ToLower(strings.TrimSpace(raw))
	if lower != "brreg" && lower != "manual" {
		return "", fmt.Sprintf("A legal source must be one of 'brreg', 'manual', but was '%s'", raw)
	}
	return lower, ""
}

// validateLegalType is LegalType's Validate and constructor
// (DM/Customers/Common/LegalType.cs): only non-blank is required —
// "person"/"business" are conventional constants, not an enforced set, so
// e.g. "spaceship" passes.
func validateLegalType(raw string) (string, string) {
	if strings.TrimSpace(raw) == "" {
		return "", "A legal type cannot be null or empty"
	}
	return strings.ToLower(strings.TrimSpace(raw)), ""
}

// validatePersonName is PersonName's Validate and constructor
// (DM/Contacts/Common/PersonName.cs): non-blank, at most 100 UTF-16 code
// units, trimmed but case-preserved.
func validatePersonName(raw string) (string, string) {
	if strings.TrimSpace(raw) == "" {
		return "", "A name cannot be null or empty"
	}
	if n := utf16Length(raw); n > 100 {
		return "", fmt.Sprintf("A name cannot be longer than 100 characters, the given value was %d characters", n)
	}
	return strings.TrimSpace(raw), ""
}

// validateNamePart is NamePart's Validate and constructor
// (DM/Contacts/Common/NamePart.cs): non-blank, at most 20 UTF-16 code units,
// trimmed but case-preserved.
func validateNamePart(raw string) (string, string) {
	if strings.TrimSpace(raw) == "" {
		return "", "A name part cannot be null or empty"
	}
	if n := utf16Length(raw); n > 20 {
		return "", fmt.Sprintf("A name part cannot be longer than 20 characters, the given value was %d characters", n)
	}
	return strings.TrimSpace(raw), ""
}

// isAllowedPhoneCharacter is PhoneNumber.IsAllowedCharacter
// (DM/Contacts/Common/PhoneNumber.cs:46-47): char.IsDigit(character) is
// Unicode-aware in .NET (true for any Unicode decimal digit, not only
// ASCII), so unicode.IsDigit is the faithful port, not an ASCII range check.
func isAllowedPhoneCharacter(r rune) bool {
	if unicode.IsDigit(r) {
		return true
	}
	switch r {
	case ' ', '+', '-', '(', ')', '.':
		return true
	default:
		return false
	}
}

// validatePhoneNumber is PhoneNumber's Validate and constructor
// (DM/Contacts/Common/PhoneNumber.cs): non-blank, at most 30 UTF-16 code
// units, only digits/space/+-().  and at least one digit, trimmed but
// case-preserved (it has no case). The check order — blank, length,
// character class, then digit presence — matters: "+-() ." contains only
// allowed characters but no digit, so it fails on the digit check, not the
// character-class one.
func validatePhoneNumber(raw string) (string, string) {
	if strings.TrimSpace(raw) == "" {
		return "", "A phone number cannot be null or empty"
	}
	if n := utf16Length(raw); n > 30 {
		return "", fmt.Sprintf("A phone number cannot be longer than 30 characters, the given value was %d characters", n)
	}
	trimmed := strings.TrimSpace(raw)
	for _, r := range trimmed {
		if !isAllowedPhoneCharacter(r) {
			return "", "A phone number can only contain digits, spaces and the characters + - ( ) ."
		}
	}
	hasDigit := false
	for _, r := range raw {
		if unicode.IsDigit(r) {
			hasDigit = true
			break
		}
	}
	if !hasDigit {
		return "", "A phone number must contain at least one digit"
	}
	return trimmed, ""
}

// hasValidEmailShape is EmailAddress.HasValidShape
// (DM/Contacts/Common/EmailAddress.cs:45-54): exactly one '@' at a positive
// index, a '.' somewhere after it with at least one character in between,
// not trailing, and no space anywhere.
func hasValidEmailShape(value string) bool {
	at := strings.IndexByte(value, '@')
	if at <= 0 || at != strings.LastIndexByte(value, '@') {
		return false
	}
	dot := strings.IndexByte(value[at:], '.')
	if dot == -1 || dot+at <= at+1 {
		return false
	}
	if strings.HasSuffix(value, ".") {
		return false
	}
	return !strings.Contains(value, " ")
}

// validateEmailAddress is EmailAddress's Validate and constructor
// (DM/Contacts/Common/EmailAddress.cs): non-blank, at most 255 UTF-16 code
// units, a plausible shape (not full RFC 5322), trimmed and lowercased.
func validateEmailAddress(raw string) (string, string) {
	if strings.TrimSpace(raw) == "" {
		return "", "An email address cannot be null or empty"
	}
	if n := utf16Length(raw); n > 255 {
		return "", fmt.Sprintf("An email address cannot be longer than 255 characters, the given value was %d characters", n)
	}
	trimmed := strings.TrimSpace(raw)
	if !hasValidEmailShape(trimmed) {
		return "", "An email address must have the shape 'name@domain.tld'"
	}
	return strings.ToLower(trimmed), ""
}

// validateContactRole is ContactRole's Validate and constructor
// (DM/Contacts/Common/ContactRole.cs): non-blank, at most 255 UTF-16 code
// units, trimmed but case-preserved.
func validateContactRole(raw string) (string, string) {
	if strings.TrimSpace(raw) == "" {
		return "", "A role cannot be null or empty"
	}
	if n := utf16Length(raw); n > 255 {
		return "", fmt.Sprintf("A role cannot be longer than 255 characters, the given value was %d characters", n)
	}
	return strings.TrimSpace(raw), ""
}

// legalIdentity is a customer's normalized legal identity: the five value
// objects LegalIdentity.cs composes, always all-set-or-all-unset together
// (inventory §2.1). A nil *legalIdentity is "no identity"; every field of a
// non-nil one has already passed its value object's validation.
type legalIdentity struct {
	Country string
	Type    string
	ID      string
	Name    string
	Source  string
}

// validateLegalIdentity is LegalIdentity.TryCreate
// (DM/Customers/ValueObjects/LegalIdentity.cs:18-72): every field is
// validated independently and every error reported together, keyed
// "country"/"type"/"id"/"name"/"source" — never short-circuited on the
// first failure. Customers foundation design D2 adds one cross-field rule on
// top: when the normalised country is "no" and the normalised type is
// "business", id must be a Norwegian organisasjonsnummer, checked (and, on
// success, stored as) here rather than in validateLegalID — this is the one
// place both values are already known good. Every other combination,
// including "no" with type "person" (deliberately not treated as a
// fødselsnummer field — that is P5's GDPR call to make, not this one's),
// keeps validateLegalID's plain non-blank/length rule.
func validateLegalIdentity(country, legalType, id, name, source string) (legalIdentity, map[string][]string) {
	errs := map[string][]string{}

	c, err := validateCountryCode(country)
	if err != "" {
		errs["country"] = []string{err}
	}
	t, err := validateLegalType(legalType)
	if err != "" {
		errs["type"] = []string{err}
	}
	i, err := validateLegalID(id)
	if err != "" {
		errs["id"] = []string{err}
	}
	n, err := validateLegalName(name)
	if err != "" {
		errs["name"] = []string{err}
	}
	s, err := validateLegalSource(source)
	if err != "" {
		errs["source"] = []string{err}
	}

	if errs["country"] == nil && errs["type"] == nil && errs["id"] == nil && c == "no" && t == "business" {
		digits := stripWhitespace(i)
		if !validNorwegianOrgNumber(digits) {
			errs["id"] = []string{fmt.Sprintf("A Norwegian organisation number must be nine digits with a valid check digit, but was '%s'", id)}
		} else {
			i = digits
		}
	}

	if len(errs) > 0 {
		return legalIdentity{}, errs
	}
	return legalIdentity{Country: c, Type: t, ID: i, Name: n, Source: s}, nil
}

// stripWhitespace removes every Unicode whitespace rune, not merely leading
// and trailing ones: an organisasjonsnummer copied from a form is often
// grouped in threes ("974 760 673"), and every space in it is noise, not
// structure.
func stripWhitespace(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
}
