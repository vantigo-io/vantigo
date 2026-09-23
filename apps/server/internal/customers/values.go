package customers

import (
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf16"

	"golang.org/x/text/unicode/norm"

	"github.com/vantigo-io/vantigo/server/internal/customers/gen"
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
// not trailing, and no whitespace anywhere. Widened from "no space" to "no
// Unicode whitespace" in review fix round 1: no contacts test pinned the
// narrower space-only behaviour (a tab-containing address was never
// exercised), and validateEmail (D2, below) now shares this function for its
// own shape check — one email shape rule for the module, not two that can
// silently drift apart.
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
	return !strings.ContainsFunc(value, unicode.IsSpace)
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

// associationTitleRule is the free text a customer–contact association
// carries (typed contact roles design D1): non-blank when given, at most 255
// UTF-16 code units, trimmed but case-preserved. It stays parameterised by the
// noun its message names even though `title` is now the only name the contract
// has (the deprecated `role` alias went with follow-ups design D5, while
// nothing was live): the noun costs a string, and it is the seam a second name
// would use if one ever arrives.
func associationTitleRule(noun, raw string) (string, string) {
	if strings.TrimSpace(raw) == "" {
		return "", fmt.Sprintf("A %s cannot be null or empty", noun)
	}
	if n := utf16Length(raw); n > 255 {
		return "", fmt.Sprintf("A %s cannot be longer than 255 characters, the given value was %d characters", noun, n)
	}
	return strings.TrimSpace(raw), ""
}

// validateContactTitle is associationTitleRule under the only name the field
// has (design D1). Blank-when-given is an error rather than "absent": a client
// that sends "title": "" is saying something, and saying it wrongly, which is
// the distinction every other optional field in this module draws by simply
// omitting the key.
func validateContactTitle(raw string) (string, string) {
	return associationTitleRule("title", raw)
}

// validateAssociationRole is the typed role vocabulary (design D2). Three
// values, defined in code and nowhere else: not a yaml enum (a contract enum
// would answer a 400 the module cannot word) and not a database CHECK (which
// would turn the design's "a wider list is a value change for later" into a
// migration, the same reasoning 00024 gives for a tag's colour). The message
// is the design's own, and the comparison is case-sensitive: 'Billing' is a
// mistake, not a variant, because the value is an identifier that travels into
// URLs and payloads rather than a name anyone types.
func validateAssociationRole(raw string) (string, string) {
	trimmed := strings.TrimSpace(raw)
	for _, role := range contactRoleOrder {
		if trimmed == role {
			return trimmed, ""
		}
	}
	return "", fmt.Sprintf("A contact role must be one of 'billing', 'project' or 'decision_maker', but was '%s'", trimmed)
}

// validateEmail, validatePhone and validateWebsite are the invoice-ready
// customer design's own value objects (docs/superpowers/specs/2026-09-21-
// customers-invoice-ready-design.md, D2): a customer's own contact details,
// not a contact's — validateEmailAddress/validatePhoneNumber above stay
// ContactValueObjectTests ports for Contacts, unchanged. Unlike every
// sibling above, none of the three treats a blank string as an error: the
// sub-resource that calls them (contact_info.go's validateContactInfo, and
// later the billing profile's invoiceEmail/reminderEmail, D4) treats a blank
// field as "clear to NULL" before ever reaching here, so each function below
// only ever validates a string already known to be non-blank.

// validateEmail is D2's email rule: at most 255 UTF-16 code units; trimmed
// but never lower-cased — unlike validateEmailAddress, a customer's own
// address is stored as typed, not canonicalized. The shape check itself is
// hasValidEmailShape (review fix round 1: the original inline check here —
// "exactly one '@', a non-empty local part, a domain containing a dot" —
// missed embedded whitespace and a leading/trailing dot on the domain, cases
// hasValidEmailShape already guarded for contacts). One email shape rule for
// the whole module now, not two that can drift apart; only the message and
// the no-lower-casing normalisation stay D2's own.
func validateEmail(raw string) (string, string) {
	if n := utf16Length(raw); n > 255 {
		return "", fmt.Sprintf("An email address cannot be longer than 255 characters, the given value was %d characters", n)
	}
	trimmed := strings.TrimSpace(raw)
	if !hasValidEmailShape(trimmed) {
		return "", fmt.Sprintf("An email address must look like name@example.com, but was '%s'", raw)
	}
	return trimmed, ""
}

// validatePhone is D2's phone rule: at most 30 UTF-16 code units, only
// digits, spaces and + - ( ), and at least five digits. One message covers
// both the character class and the digit-count floor — unlike
// validatePhoneNumber's three-way split (character class, then digit
// presence, as their own branches), D2 only ever has the one rule to report.
func validatePhone(raw string) (string, string) {
	if n := utf16Length(raw); n > 30 {
		return "", fmt.Sprintf("A phone number cannot be longer than 30 characters, the given value was %d characters", n)
	}
	trimmed := strings.TrimSpace(raw)
	digits := 0
	allowed := true
	for _, r := range trimmed {
		switch {
		case unicode.IsDigit(r):
			digits++
		case r == ' ' || r == '+' || r == '-' || r == '(' || r == ')':
		default:
			allowed = false
		}
	}
	if !allowed || digits < 5 {
		return "", fmt.Sprintf("A phone number may only contain digits, spaces and + - ( ), and needs at least five digits, but was '%s'", raw)
	}
	return trimmed, ""
}

// validateWebsite is D2's website rule: at most 2048 UTF-16 code units, an
// absolute http or https URL — the same shape the timeline's sourceUrl field
// checks inline (timeline.go's validateManualTimelineRequest). The check is
// repeated here, not shared, because that one is inline rather than a named
// function, and this module's wording for it is its own ("A website must
// be...", not "SourceUrl must be...").
func validateWebsite(raw string) (string, string) {
	if n := utf16Length(raw); n > 2048 {
		return "", fmt.Sprintf("A website cannot be longer than 2048 characters, the given value was %d characters", n)
	}
	trimmed := strings.TrimSpace(raw)
	u, err := url.Parse(trimmed)
	if err != nil || !u.IsAbs() || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Sprintf("A website must be an absolute http or https URL, but was '%s'", raw)
	}
	return trimmed, ""
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

// validateAddressType, validatedAddress and validateAddress are the
// invoice-ready customer design's typed address value object
// (docs/superpowers/specs/2026-09-21-customers-invoice-ready-design.md, D3):
// no .NET ancestor, since addresses are new to this port. Shaped like
// validateLegalIdentity above — every field validated independently, every
// error reported together, keyed by the request's own JSON field name — not
// like contactInfo's three fields (contact_info.go), which are validated one
// at a time inline because none of them has a cross-field rule. isPrimary
// carries no value of its own to validate (it is a plain bool addresses.go
// already defaults from an absent request field), so it is not part of
// either type here; addresses.go composes the two.

// validateAddressType is the address type value object: one of postal/
// invoice/delivery/visiting, case-insensitive, trimmed and lowercased,
// shaped like validateCustomerType.
func validateAddressType(raw string) (string, string) {
	if strings.TrimSpace(raw) == "" {
		return "", "An address type cannot be null or empty"
	}
	lower := strings.ToLower(strings.TrimSpace(raw))
	switch lower {
	case "postal", "invoice", "delivery", "visiting":
		return lower, ""
	default:
		return "", fmt.Sprintf("An address type must be one of 'postal', 'invoice', 'delivery' or 'visiting', but was '%s'", raw)
	}
}

// validateAddressLine1 is line1's value object: non-blank, at most 255
// UTF-16 code units, trimmed but case-preserved — shaped like
// validateLegalName.
func validateAddressLine1(raw string) (string, string) {
	if strings.TrimSpace(raw) == "" {
		return "", "An address's first line cannot be null or empty"
	}
	if n := utf16Length(raw); n > 255 {
		return "", fmt.Sprintf("An address's first line cannot be longer than 255 characters, the given value was %d characters", n)
	}
	return strings.TrimSpace(raw), ""
}

// validateOptionalAddressText is label/line2/postalCode/city/region's shared
// rule: nil or blank/whitespace-only both mean "not set" — never an error on
// their own, the same treatment contact_info.go's normalizedOrNil gives
// email/phone/website — only a non-blank value longer than max is rejected,
// under field, worded with noun ("a label", "an address's second line", …).
func validateOptionalAddressText(raw *string, field, noun string, max int, errs map[string][]string) *string {
	if raw == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*raw)
	if trimmed == "" {
		return nil
	}
	if n := utf16Length(trimmed); n > max {
		errs[field] = []string{fmt.Sprintf("%s cannot be longer than %d characters, the given value was %d characters", noun, max, n)}
		return nil
	}
	return &trimmed
}

// norwegianPostalCode is the four-digit shape a Norwegian postal code must
// have (invoice-ready customer design D3) — postnummer are always four
// digits, "0001" through "9990" in practice, but this checks the shape only,
// not a registered-range table.
var norwegianPostalCode = regexp.MustCompile(`^[0-9]{4}$`)

// validatedAddress is CustomerAddressRequest's validated, normalized values
// (invoice-ready customer design D3), everything but isPrimary (see the
// comment above validateAddressType).
type validatedAddress struct {
	Type       string
	Label      *string
	Line1      string
	Line2      *string
	PostalCode *string
	City       *string
	Region     *string
	Country    string
}

// validateAddress is CustomerAddressRequest's TryCreate (invoice-ready
// customer design D3): every field validated independently and every error
// reported together, keyed "type"/"label"/"line1"/"line2"/"postalCode"/
// "city"/"region"/"country" — never short-circuited on the first failure.
// The one cross-field rule — a Norwegian address needs a four-digit postal
// code and a city (the controller ruling's exact wording) — only runs once
// country, postalCode and city have each already passed their own check,
// the same ordering validateLegalIdentity's Norwegian organisation-number
// rule uses: it never overwrites the specific reason postalCode or city
// already failed for on its own. Takes the generated request type directly,
// the same shape validateBillingProfile's own does — req.IsPrimary carries
// no value of its own to validate (it is a plain bool the caller already
// defaults), so it is not read here; addresses.go composes the two.
func validateAddress(req gen.CustomerAddressRequest) (validatedAddress, map[string][]string) {
	errs := map[string][]string{}

	t, err := validateAddressType(req.Type)
	if err != "" {
		errs["type"] = []string{err}
	}
	l1, err := validateAddressLine1(req.Line1)
	if err != "" {
		errs["line1"] = []string{err}
	}
	c, err := validateCountryCode(req.Country)
	if err != "" {
		errs["country"] = []string{err}
	}
	lbl := validateOptionalAddressText(req.Label, "label", "A label", 100, errs)
	l2 := validateOptionalAddressText(req.Line2, "line2", "An address's second line", 255, errs)
	pc := validateOptionalAddressText(req.PostalCode, "postalCode", "A postal code", 20, errs)
	ct := validateOptionalAddressText(req.City, "city", "A city", 100, errs)
	rg := validateOptionalAddressText(req.Region, "region", "A region", 100, errs)

	if errs["country"] == nil && c == "no" {
		if errs["postalCode"] == nil && (pc == nil || !norwegianPostalCode.MatchString(*pc)) {
			errs["postalCode"] = []string{"A Norwegian address needs a four-digit postal code"}
		}
		if errs["city"] == nil && ct == nil {
			errs["city"] = []string{"A Norwegian address needs a city"}
		}
	}

	if len(errs) > 0 {
		return validatedAddress{}, errs
	}
	return validatedAddress{Type: t, Label: lbl, Line1: l1, Line2: l2, PostalCode: pc, City: ct, Region: rg, Country: c}, nil
}

// tagColors is the colour vocabulary a tag may use (owner and tags design
// D2): Mantine's own named colours, which is what the UI paints a chip with.
// Validating against the UI's palette in the API is deliberate and is the
// whole point of the rule — the alternative is every consumer sanitising
// whatever arrived, and a chip painted with a value a stylesheet does not know
// is an invisible chip. black and white are deliberately absent: a chip needs
// contrast against both themes.
var tagColors = []string{"gray", "red", "pink", "grape", "violet", "indigo", "blue", "cyan", "teal", "green", "lime", "yellow", "orange"}

// validateTagName is the tag name rule: NFC-normalised, non-blank, at most 100
// UTF-16 code units, trimmed but case-preserved — validateFriendlyName's own
// shape with 100 instead of 255. Case is preserved even though uniqueness
// ignores it (migration 00024's lower(name) index): 'VIP' is how someone wrote
// it and is how it should read back, while 'vip' is not a second tag.
//
// The NFC pass (final fix wave M2) is the same argument as the lower(name)
// index, one layer down. 'Café' with a precomposed é and 'Café' with an e plus
// a combining acute are one word to every reader and two different byte strings
// to the index, so without it a macOS filename, an iOS keyboard or a paste from
// another system quietly creates a second, indistinguishable tag and splits the
// customers carrying it in two. Normalising on the way in — not comparing both
// forms at each call site — means the stored name is the composed one whichever
// form arrived, and it happens BEFORE the length check so the count is of the
// string that will actually be stored.
func validateTagName(raw string) (string, string) {
	name := norm.NFC.String(raw)
	if strings.TrimSpace(name) == "" {
		return "", "A tag name cannot be null or empty"
	}
	if n := utf16Length(name); n > 100 {
		return "", fmt.Sprintf("A tag name cannot be longer than 100 characters, the given value was %d characters", n)
	}
	return strings.TrimSpace(name), ""
}

// validateGroupName is the group name's rule, which is validateTagName's under
// its own noun (customer groups design D2): 1-100 characters after NFC
// normalisation, trimmed. Not shared with the tags' own function despite being
// the same three lines — the message names the thing being validated, and a
// parameterised noun for two callers would be a seam standing in for a word.
func validateGroupName(raw string) (string, string) {
	name := norm.NFC.String(raw)
	if strings.TrimSpace(name) == "" {
		return "", "A group name cannot be null or empty"
	}
	if n := utf16Length(name); n > 100 {
		return "", fmt.Sprintf("A group name cannot be longer than 100 characters, the given value was %d characters", n)
	}
	return strings.TrimSpace(name), ""
}

// validateTagColor is the colour rule. Case-sensitive, like every other value
// rule here that names a closed set of lowercase tokens: a query parameter or
// an enum token is never normalized in this module, only rejected.
//
// The message is built from tagColors rather than written out as a literal —
// the one enforced-set validator in this file that does, because it is the one
// set that will grow: a palette is a UI decision, and the day a colour is
// added, a hand-written sentence is a second place to forget.
func validateTagColor(raw string) (string, string) {
	if slices.Contains(tagColors, raw) {
		return raw, ""
	}
	quoted := make([]string, 0, len(tagColors))
	for _, c := range tagColors {
		quoted = append(quoted, "'"+c+"'")
	}
	return "", fmt.Sprintf("A tag colour must be one of %s or %s, but was '%s'",
		strings.Join(quoted[:len(quoted)-1], ", "), quoted[len(quoted)-1], raw)
}
