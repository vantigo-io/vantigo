package customers

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"testing"
)

// Ported from Domain/Customers/Common/FriendlyNameTests.cs.
// Constructor_WithNullOrWhitespace_ThrowsDomainException. Asserts the exact
// message text: this module's only error vocabulary (inventory §1/§2.2),
// and the whole reason FriendlyName.cs's Validate exists rather than a bare
// bool.
func TestValidateFriendlyName_BlankIsInvalid(t *testing.T) {
	const want = "A friendly name cannot be null or empty"
	for _, v := range []string{"", "   "} {
		if _, err := validateFriendlyName(v); err != want {
			t.Errorf("validateFriendlyName(%q) = error %q, want %q", v, err, want)
		}
	}
}

// Ported from Domain/Customers/Common/FriendlyNameTests.cs.
// Constructor_WithValueLongerThanMaxLength_ThrowsDomainException.
func TestValidateFriendlyName_TooLongIsInvalid(t *testing.T) {
	v := strings.Repeat("a", 256)
	want := "A friendly name cannot be longer than 255 characters, the given value was 256 characters"
	if _, err := validateFriendlyName(v); err != want {
		t.Errorf("validateFriendlyName(256 chars) = error %q, want %q", err, want)
	}
}

// Ported from Domain/Customers/Common/FriendlyNameTests.cs.
// Constructor_WithValueAtMaxLength_Succeeds.
func TestValidateFriendlyName_AtMaxLengthSucceeds(t *testing.T) {
	v := strings.Repeat("a", 255)
	got, err := validateFriendlyName(v)
	if err != "" || got != v {
		t.Errorf("validateFriendlyName(255 chars) = %q, %q, want the value unchanged and no error", got, err)
	}
}

// Ported from Domain/Customers/Common/FriendlyNameTests.cs.
// Constructor_TrimsValueButPreservesCasing.
func TestValidateFriendlyName_TrimsButPreservesCasing(t *testing.T) {
	got, err := validateFriendlyName(" Wayne Enterprises ")
	if err != "" || got != "Wayne Enterprises" {
		t.Errorf("validateFriendlyName(%q) = %q, %q, want \"Wayne Enterprises\", no error", " Wayne Enterprises ", got, err)
	}
}

// Ported from Domain/Customers/Common/CountryCodeTests.cs.
// Constructor_WithNullOrWhitespace_ThrowsDomainException.
func TestValidateCountryCode_BlankIsInvalid(t *testing.T) {
	const want = "A country code cannot be null or empty"
	for _, v := range []string{"", "   "} {
		if _, err := validateCountryCode(v); err != want {
			t.Errorf("validateCountryCode(%q) = error %q, want %q", v, err, want)
		}
	}
}

// Ported from Domain/Customers/Common/CountryCodeTests.cs.
// Constructor_TrimsAndLowercasesValue. "no" is an assigned ISO 3166-1
// alpha-2 code, so this also exercises the shape check added in customers
// foundation design D2 without tripping it.
func TestValidateCountryCode_TrimsAndLowercases(t *testing.T) {
	got, err := validateCountryCode(" NO ")
	if err != "" || got != "no" {
		t.Errorf("validateCountryCode(%q) = %q, %q, want \"no\", no error", " NO ", got, err)
	}
}

// Not a port: .NET's CountryCode never checked ISO 3166-1 shape (customers
// foundation design D2 adds it). "nor" is the alpha-3 form of a real
// country, not an alpha-2 code; "Narnia" is not a country at all; both, like
// "xx", must be rejected with the raw (unnormalized) value quoted back, this
// module's usual convention.
func TestValidateCountryCode_RejectsUnknownCodes(t *testing.T) {
	for _, v := range []string{"xx", "nor", "Narnia"} {
		want := fmt.Sprintf("A country code must be an ISO 3166-1 alpha-2 code, but was '%s'", v)
		if _, err := validateCountryCode(v); err != want {
			t.Errorf("validateCountryCode(%q) = error %q, want %q", v, err, want)
		}
	}
}

// Not a port: pins the ISO 3166-1 alpha-2 table itself (customers
// foundation design D2) against silent drift — 249 officially assigned
// codes, no more and no fewer — plus a few codes people commonly confuse
// with an assigned one: "xk" (Kosovo) and "uk" (the everyday alias for
// "gb") are both still unassigned by ISO, so neither belongs in the set.
func TestIso3166Alpha2_Has249AssignedCodes(t *testing.T) {
	if n := len(iso3166Alpha2); n != 249 {
		t.Errorf("len(iso3166Alpha2) = %d, want 249", n)
	}
	for _, code := range []string{"no", "se", "gb", "us"} {
		if _, ok := iso3166Alpha2[code]; !ok {
			t.Errorf("iso3166Alpha2[%q] missing, want present", code)
		}
	}
	for _, code := range []string{"xk", "uk"} {
		if _, ok := iso3166Alpha2[code]; ok {
			t.Errorf("iso3166Alpha2[%q] present, want absent", code)
		}
	}
}

// Ported from Domain/Customers/Common/LegalIdTests.cs.
// Constructor_WithNullOrWhitespace_ThrowsDomainException.
func TestValidateLegalID_BlankIsInvalid(t *testing.T) {
	const want = "A legal id cannot be null or empty"
	for _, v := range []string{"", "   "} {
		if _, err := validateLegalID(v); err != want {
			t.Errorf("validateLegalID(%q) = error %q, want %q", v, err, want)
		}
	}
}

// Ported from Domain/Customers/Common/LegalIdTests.cs.
// Constructor_WithValueLongerThanMaxLength_ThrowsDomainException.
func TestValidateLegalID_TooLongIsInvalid(t *testing.T) {
	v := strings.Repeat("1", 51)
	want := "A legal id cannot be longer than 50 characters, the given value was 51 characters"
	if _, err := validateLegalID(v); err != want {
		t.Errorf("validateLegalID(51 chars) = error %q, want %q", err, want)
	}
}

// Ported from Domain/Customers/Common/LegalIdTests.cs.
// Constructor_WithValueAtMaxLength_Succeeds.
func TestValidateLegalID_AtMaxLengthSucceeds(t *testing.T) {
	v := strings.Repeat("1", 50)
	got, err := validateLegalID(v)
	if err != "" || got != v {
		t.Errorf("validateLegalID(50 chars) = %q, %q, want the value unchanged and no error", got, err)
	}
}

// Ported from Domain/Customers/Common/LegalIdTests.cs.
// Constructor_TrimsAndLowercasesValue.
func TestValidateLegalID_TrimsAndLowercases(t *testing.T) {
	got, err := validateLegalID(" 923609016-A ")
	if err != "" || got != "923609016-a" {
		t.Errorf("validateLegalID(%q) = %q, %q, want \"923609016-a\", no error", " 923609016-A ", got, err)
	}
}

// Not a port: pins validNorwegianOrgNumber, the mod-11 check customers
// foundation design D2 adds (weights 3 2 7 6 5 4 3 2 over the first eight
// digits; a remainder that would produce check digit 10 is invalid, since
// nine digits cannot encode it). The function takes an already-stripped
// digit string — validateLegalIdentity strips whitespace before calling it
// — so it need not tolerate spaces itself.
func TestValidNorwegianOrgNumber(t *testing.T) {
	cases := map[string]bool{
		"923609016":  true,  // Equinor ASA
		"974760673":  true,  // Brønnøysundregistrene
		"923609017":  false, // Equinor's number with the check digit flipped
		"92360901":   false, // eight digits
		"9236090166": false, // ten digits
		"92360901a":  false, // a letter where a digit belongs
		"912345678":  false, // the first eight digits' weighted sum gives check digit 10
		"":           false,
	}
	for v, want := range cases {
		if got := validNorwegianOrgNumber(v); got != want {
			t.Errorf("validNorwegianOrgNumber(%q) = %v, want %v", v, got, want)
		}
	}
}

// Ported from Domain/Customers/Common/LegalNameTests.cs.
// Constructor_WithNullOrWhitespace_ThrowsDomainException.
func TestValidateLegalName_BlankIsInvalid(t *testing.T) {
	const want = "A legal name cannot be null or empty"
	for _, v := range []string{"", "   "} {
		if _, err := validateLegalName(v); err != want {
			t.Errorf("validateLegalName(%q) = error %q, want %q", v, err, want)
		}
	}
}

// Ported from Domain/Customers/Common/LegalNameTests.cs.
// Constructor_WithValueLongerThanMaxLength_ThrowsDomainException.
func TestValidateLegalName_TooLongIsInvalid(t *testing.T) {
	v := strings.Repeat("a", 256)
	want := "A legal name cannot be longer than 255 characters, the given value was 256 characters"
	if _, err := validateLegalName(v); err != want {
		t.Errorf("validateLegalName(256 chars) = error %q, want %q", err, want)
	}
}

// Ported from Domain/Customers/Common/LegalNameTests.cs.
// Constructor_WithValueAtMaxLength_Succeeds.
func TestValidateLegalName_AtMaxLengthSucceeds(t *testing.T) {
	v := strings.Repeat("a", 255)
	got, err := validateLegalName(v)
	if err != "" || got != v {
		t.Errorf("validateLegalName(255 chars) = %q, %q, want the value unchanged and no error", got, err)
	}
}

// Ported from Domain/Customers/Common/LegalNameTests.cs.
// Constructor_TrimsValueButPreservesCasing.
func TestValidateLegalName_TrimsButPreservesCasing(t *testing.T) {
	got, err := validateLegalName(" Acme AS ")
	if err != "" || got != "Acme AS" {
		t.Errorf("validateLegalName(%q) = %q, %q, want \"Acme AS\", no error", " Acme AS ", got, err)
	}
}

// Ported from Domain/Customers/Common/LegalTypeTests.cs.
// Constructor_WithNullOrWhitespace_ThrowsDomainException.
func TestValidateLegalType_BlankIsInvalid(t *testing.T) {
	const want = "A legal type cannot be null or empty"
	for _, v := range []string{"", "   "} {
		if _, err := validateLegalType(v); err != want {
			t.Errorf("validateLegalType(%q) = error %q, want %q", v, err, want)
		}
	}
}

// Ported from Domain/Customers/Common/LegalTypeTests.cs.
// Constructor_TrimsAndLowercasesValue.
func TestValidateLegalType_TrimsAndLowercases(t *testing.T) {
	got, err := validateLegalType(" Business ")
	if err != "" || got != "business" {
		t.Errorf("validateLegalType(%q) = %q, %q, want \"business\", no error", " Business ", got, err)
	}
}

// Ported from Domain/Customers/Common/LegalTypeTests.cs.
// ImplicitConversion_FromWellKnownValues_CreatesLegalType (person and
// business are conventional constants, not an enforced set — any non-blank
// string is valid, but these two pin the values the rest of the module
// expects to see).
func TestValidateLegalType_WellKnownValues(t *testing.T) {
	for _, v := range []string{"person", "business"} {
		got, err := validateLegalType(v)
		if err != "" || got != v {
			t.Errorf("validateLegalType(%q) = %q, %q, want %q, no error", v, got, err, v)
		}
	}
}

// No dedicated .NET test class exists for LegalSource (customers inventory
// §7 lists none), so this is not a port; it is a unit-level pin of the
// behaviour that CreateCustomer_WithInvalidLegalSource_ReturnsBadRequestWithFieldError
// (Integration/CustomersEndpointsTests.cs, ported in customers_test.go)
// otherwise only exercises indirectly through the HTTP layer. Exact message
// text, including the raw (unnormalized) value LegalSource.cs's message
// quotes back for the two rejected-but-non-blank cases.
func TestValidateLegalSource_InvalidIsRejected(t *testing.T) {
	cases := map[string]string{
		"":       "A legal source cannot be null or empty",
		"   ":    "A legal source cannot be null or empty",
		"bogus":  "A legal source must be one of 'brreg', 'manual', but was 'bogus'",
		"BRREG!": "A legal source must be one of 'brreg', 'manual', but was 'BRREG!'",
	}
	for v, want := range cases {
		if _, err := validateLegalSource(v); err != want {
			t.Errorf("validateLegalSource(%q) = error %q, want %q", v, err, want)
		}
	}
}

// No dedicated .NET test class exists for CustomerStatus either (customers
// inventory §7 lists none); this pins its exact messages, which
// UpdatingWithInvalidStatus_ReturnsValidationProblem (customers_test.go)
// otherwise only exercises indirectly through the HTTP layer.
func TestValidateCustomerStatus_ExactMessages(t *testing.T) {
	cases := map[string]string{
		"":        "A customer status cannot be null or empty",
		"   ":     "A customer status cannot be null or empty",
		"deleted": "A customer status must be one of 'active', 'disabled' or 'archived', but was 'deleted'",
	}
	for v, want := range cases {
		if _, err := validateCustomerStatus(v); err != want {
			t.Errorf("validateCustomerStatus(%q) = error %q, want %q", v, err, want)
		}
	}
	for _, v := range []string{"active", "Disabled", " ARCHIVED "} {
		got, err := validateCustomerStatus(v)
		if err != "" {
			t.Errorf("validateCustomerStatus(%q) = error %q, want none", v, err)
		}
		if want := strings.ToLower(strings.TrimSpace(v)); got != want {
			t.Errorf("validateCustomerStatus(%q) = %q, want %q", v, got, want)
		}
	}
}

// Unit-level pin of the normalization
// CreateCustomer_WithValidLegalSource_PersistsNormalizedSource (ported in
// customers_test.go) exercises through the HTTP layer.
func TestValidateLegalSource_ValidValuesNormalize(t *testing.T) {
	for _, v := range []string{"brreg", "Manual"} {
		got, err := validateLegalSource(v)
		if err != "" || got != strings.ToLower(v) {
			t.Errorf("validateLegalSource(%q) = %q, %q, want %q, no error", v, got, err, strings.ToLower(v))
		}
	}
}

// Ported from Domain/Customers/ValueObjects/LegalIdentityTests.cs.
// Construction_WithValidValues_ExposesNormalizedValues.
func TestValidateLegalIdentity_NormalizesValues(t *testing.T) {
	got, errs := validateLegalIdentity("NO", "Business", "923609016", "Acme AS", "Brreg")
	if errs != nil {
		t.Fatalf("validateLegalIdentity: unexpected errors %v", errs)
	}
	want := legalIdentity{Country: "no", Type: "business", ID: "923609016", Name: "Acme AS", Source: "brreg"}
	if got != want {
		t.Errorf("validateLegalIdentity = %+v, want %+v", got, want)
	}
}

// Ported from Domain/Customers/ValueObjects/LegalIdentityTests.cs.
// TryCreate_WithValidValues_ReturnsTrueAndIdentity.
func TestValidateLegalIdentity_ValidValues(t *testing.T) {
	got, errs := validateLegalIdentity("NO", "Business", "923609016", "Acme AS", "brreg")
	if errs != nil {
		t.Fatalf("validateLegalIdentity: unexpected errors %v", errs)
	}
	want := legalIdentity{Country: "no", Type: "business", ID: "923609016", Name: "Acme AS", Source: "brreg"}
	if got != want {
		t.Errorf("validateLegalIdentity = %+v, want %+v", got, want)
	}
}

// Ported from Domain/Customers/ValueObjects/LegalIdentityTests.cs.
// TryCreate_WithMultipleInvalidValues_ReportsAllErrorsAtOnce.
func TestValidateLegalIdentity_MultipleInvalidValuesReportsBoth(t *testing.T) {
	_, errs := validateLegalIdentity("", "business", "  ", "Acme AS", "manual")
	if len(errs) != 2 {
		t.Fatalf("validateLegalIdentity errors = %v, want exactly 2", errs)
	}
	if _, ok := errs["country"]; !ok {
		t.Error(`validateLegalIdentity errors missing "country"`)
	}
	if _, ok := errs["id"]; !ok {
		t.Error(`validateLegalIdentity errors missing "id"`)
	}
}

// Ported from Domain/Customers/ValueObjects/LegalIdentityTests.cs.
// TryCreate_WithAllValuesInvalid_ReportsAnErrorPerField.
func TestValidateLegalIdentity_AllValuesInvalidReportsEveryField(t *testing.T) {
	_, errs := validateLegalIdentity("", "", "", "", "")
	keys := make([]string, 0, len(errs))
	for k := range errs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	want := []string{"country", "id", "name", "source", "type"}
	if !slices.Equal(keys, want) {
		t.Errorf("validateLegalIdentity error keys = %v, want %v", keys, want)
	}
}

// Ported from Domain/Customers/ValueObjects/LegalIdentityTests.cs.
// TryCreate_WithUnknownSource_ReportsSourceError.
func TestValidateLegalIdentity_UnknownSourceReportsSourceOnly(t *testing.T) {
	_, errs := validateLegalIdentity("no", "business", "923609016", "Acme AS", "bogus")
	if len(errs) != 1 {
		t.Fatalf("validateLegalIdentity errors = %v, want exactly 1", errs)
	}
	if _, ok := errs["source"]; !ok {
		t.Error(`validateLegalIdentity errors missing "source"`)
	}
}

// Not a port: customers foundation design D2's controller ruling — the
// Norwegian organisation-number rule lives in validateLegalIdentity, where
// both the normalised country and type are known, not inside validateLegalID
// itself. Covers the valid cases (a plain nine digits, and whitespace
// stripped from "974 760 673"), the invalid ones (wrong check digit, wrong
// digit count, letters, the check-digit-10 case), and the two "unchanged
// rule" cases: a non-Norwegian country, and a Norwegian person, both keep
// today's non-blank/length-only check regardless of the id's shape.
func TestValidateLegalIdentity_NorwegianOrgNumberRule(t *testing.T) {
	const wantErr = "A Norwegian organisation number must be nine digits with a valid check digit, but was '%s'"

	t.Run("valid", func(t *testing.T) {
		got, errs := validateLegalIdentity("no", "business", "923609016", "Acme AS", "manual")
		if errs != nil {
			t.Fatalf("validateLegalIdentity: unexpected errors %v", errs)
		}
		if got.ID != "923609016" {
			t.Errorf("ID = %q, want %q", got.ID, "923609016")
		}
	})

	t.Run("whitespace stripped", func(t *testing.T) {
		got, errs := validateLegalIdentity("no", "business", "974 760 673", "Brønnøysundregistrene", "manual")
		if errs != nil {
			t.Fatalf("validateLegalIdentity: unexpected errors %v", errs)
		}
		if got.ID != "974760673" {
			t.Errorf("ID = %q, want %q", got.ID, "974760673")
		}
	})

	invalid := map[string]string{
		"923609017":  "923609017",  // wrong check digit
		"92360901":   "92360901",   // eight digits
		"9236090166": "9236090166", // ten digits
		"92360901a":  "92360901a",  // letters
		"912345678":  "912345678",  // check-digit-10 case
	}
	for id, raw := range invalid {
		t.Run("invalid/"+id, func(t *testing.T) {
			_, errs := validateLegalIdentity("no", "business", id, "Acme AS", "manual")
			want := fmt.Sprintf(wantErr, raw)
			if got := errs["id"]; len(got) != 1 || got[0] != want {
				t.Errorf("validateLegalIdentity(%q) errors[\"id\"] = %v, want [%q]", id, got, want)
			}
		})
	}

	t.Run("other country keeps the unchanged rule", func(t *testing.T) {
		got, errs := validateLegalIdentity("se", "business", "not-an-org-number", "Acme AB", "manual")
		if errs != nil {
			t.Fatalf("validateLegalIdentity: unexpected errors %v", errs)
		}
		if got.ID != "not-an-org-number" {
			t.Errorf("ID = %q, want unchanged \"not-an-org-number\"", got.ID)
		}
	})

	t.Run("person type keeps the unchanged rule", func(t *testing.T) {
		got, errs := validateLegalIdentity("no", "person", "010170-12345", "Kari Nordmann", "manual")
		if errs != nil {
			t.Fatalf("validateLegalIdentity: unexpected errors %v", errs)
		}
		if got.ID != "010170-12345" {
			t.Errorf("ID = %q, want unchanged \"010170-12345\"", got.ID)
		}
	})
}

// Ported from Domain/Contacts/Common/ContactValueObjectTests.cs.
// PersonNameTests.Construction_TrimsTheValue.
func TestValidatePersonName_Trims(t *testing.T) {
	got, err := validatePersonName("  Anders  ")
	if err != "" || got != "Anders" {
		t.Errorf("validatePersonName(%q) = %q, %q, want \"Anders\", no error", "  Anders  ", got, err)
	}
}

// Ported from Domain/Contacts/Common/ContactValueObjectTests.cs.
// PersonNameTests.TryCreate_WithBlankValue_Fails. Exact message text: this
// module's only error vocabulary (inventory §1/§2.2).
func TestValidatePersonName_BlankIsInvalid(t *testing.T) {
	const want = "A name cannot be null or empty"
	for _, v := range []string{"", "   "} {
		if _, err := validatePersonName(v); err != want {
			t.Errorf("validatePersonName(%q) = error %q, want %q", v, err, want)
		}
	}
}

// Ported from Domain/Contacts/Common/ContactValueObjectTests.cs.
// PersonNameTests.TryCreate_WithTooLongValue_Fails.
func TestValidatePersonName_TooLongIsInvalid(t *testing.T) {
	v := strings.Repeat("a", 101)
	want := "A name cannot be longer than 100 characters, the given value was 101 characters"
	if _, err := validatePersonName(v); err != want {
		t.Errorf("validatePersonName(101 chars) = error %q, want %q", err, want)
	}
}

// Ported from Domain/Contacts/Common/ContactValueObjectTests.cs.
// PhoneNumberTests.TryCreate_WithCommonFormats_Succeeds.
func TestValidatePhoneNumber_CommonFormatsSucceed(t *testing.T) {
	for _, v := range []string{"+47 934 89 731", "(555) 123-4567", "22.86.44.00"} {
		got, err := validatePhoneNumber(v)
		if err != "" || got != v {
			t.Errorf("validatePhoneNumber(%q) = %q, %q, want %q, no error", v, got, err, v)
		}
	}
}

// Not a port: PhoneNumberTests has no dedicated blank/too-long test in .NET,
// but both are real branches in PhoneNumber.Validate with their own exact
// message, so pin them directly.
func TestValidatePhoneNumber_BlankIsInvalid(t *testing.T) {
	const want = "A phone number cannot be null or empty"
	for _, v := range []string{"", "   "} {
		if _, err := validatePhoneNumber(v); err != want {
			t.Errorf("validatePhoneNumber(%q) = error %q, want %q", v, err, want)
		}
	}
}

func TestValidatePhoneNumber_TooLongIsInvalid(t *testing.T) {
	v := strings.Repeat("1", 31)
	want := "A phone number cannot be longer than 30 characters, the given value was 31 characters"
	if _, err := validatePhoneNumber(v); err != want {
		t.Errorf("validatePhoneNumber(31 chars) = error %q, want %q", err, want)
	}
}

// Ported from Domain/Contacts/Common/ContactValueObjectTests.cs.
// PhoneNumberTests.TryCreate_WithInvalidCharactersOrNoDigits_Fails. Exact
// message per case: the two invalid-character values and the
// no-digit-but-otherwise-allowed value fail on different branches with
// different text (PhoneNumber.cs's Validate switch, in order: blank,
// length, character class, digit presence).
func TestValidatePhoneNumber_InvalidCharactersOrNoDigitsFails(t *testing.T) {
	const invalidChars = "A phone number can only contain digits, spaces and the characters + - ( ) ."
	const noDigit = "A phone number must contain at least one digit"
	cases := map[string]string{
		"not a number":         invalidChars,
		"+47 934 89 731 ext#2": invalidChars,
		"+-() .":               noDigit,
	}
	for v, want := range cases {
		if _, err := validatePhoneNumber(v); err != want {
			t.Errorf("validatePhoneNumber(%q) = error %q, want %q", v, err, want)
		}
	}
}

// Not a port: pins the .NET divergence PhoneNumber.Validate's character and
// digit checks must not diverge on (item 5a of the Task 7 fix round).
// char.IsDigit is Unicode-aware in .NET — true for any Unicode decimal
// digit, not only ASCII 0-9 — so a phone number built entirely of
// non-ASCII decimal digits (Arabic-Indic here) must still be both
// "all allowed characters" and "has at least one digit".
func TestValidatePhoneNumber_UnicodeDigitsAreDigits(t *testing.T) {
	v := "٢٢ ٨٦ ٤٤ ٠٠" // Arabic-Indic digits for "22 86 44 00"
	got, err := validatePhoneNumber(v)
	if err != "" || got != v {
		t.Errorf("validatePhoneNumber(%q) = %q, %q, want %q, no error", v, got, err, v)
	}
}

// Ported from Domain/Contacts/Common/ContactValueObjectTests.cs.
// EmailAddressTests.TryCreate_WithValidValue_NormalizesToLowercase.
func TestValidateEmailAddress_NormalizesToLowercase(t *testing.T) {
	cases := map[string]string{
		"anders@vantigo.io":     "anders@vantigo.io",
		"  Anders@Vantigo.IO  ": "anders@vantigo.io",
	}
	for v, want := range cases {
		got, err := validateEmailAddress(v)
		if err != "" || got != want {
			t.Errorf("validateEmailAddress(%q) = %q, %q, want %q, no error", v, got, err, want)
		}
	}
}

// Not a port: EmailAddressTests has no dedicated blank/too-long test in
// .NET, but both are real branches in EmailAddress.Validate with their own
// exact message, so pin them directly.
func TestValidateEmailAddress_BlankIsInvalid(t *testing.T) {
	const want = "An email address cannot be null or empty"
	for _, v := range []string{"", "   "} {
		if _, err := validateEmailAddress(v); err != want {
			t.Errorf("validateEmailAddress(%q) = error %q, want %q", v, err, want)
		}
	}
}

func TestValidateEmailAddress_TooLongIsInvalid(t *testing.T) {
	v := strings.Repeat("a", 251) + "@a.io" // 251 + 5 = 256 characters
	want := "An email address cannot be longer than 255 characters, the given value was 256 characters"
	if _, err := validateEmailAddress(v); err != want {
		t.Errorf("validateEmailAddress(256 chars) = error %q, want %q", err, want)
	}
}

// Ported from Domain/Contacts/Common/ContactValueObjectTests.cs.
// EmailAddressTests.TryCreate_WithInvalidShape_Fails.
func TestValidateEmailAddress_InvalidShapeFails(t *testing.T) {
	const want = "An email address must have the shape 'name@domain.tld'"
	for _, v := range []string{
		"no-at-sign", "@vantigo.io", "anders@", "anders@vantigo", "anders@vantigo.",
		"an ders@vantigo.io", "anders@@vantigo.io",
	} {
		if _, err := validateEmailAddress(v); err != want {
			t.Errorf("validateEmailAddress(%q) = error %q, want %q", v, err, want)
		}
	}
}

// Ported from Domain/Contacts/Common/ContactValueObjectTests.cs.
// NamePartTests.TryCreate_WithTooLongValue_Fails.
func TestValidateNamePart_TooLongIsInvalid(t *testing.T) {
	v := strings.Repeat("a", 21)
	want := "A name part cannot be longer than 20 characters, the given value was 21 characters"
	if _, err := validateNamePart(v); err != want {
		t.Errorf("validateNamePart(21 chars) = error %q, want %q", err, want)
	}
}

// Not a port: NamePartTests has no blank-value test in .NET, but it is a
// real branch in NamePart.Validate with its own exact message.
func TestValidateNamePart_BlankIsInvalid(t *testing.T) {
	const want = "A name part cannot be null or empty"
	for _, v := range []string{"", "   "} {
		if _, err := validateNamePart(v); err != want {
			t.Errorf("validateNamePart(%q) = error %q, want %q", v, err, want)
		}
	}
}

// Ported from Domain/Contacts/Common/ContactValueObjectTests.cs.
// NamePartTests.TryCreate_WithValidValue_Succeeds.
func TestValidateNamePart_ValidValueSucceeds(t *testing.T) {
	got, err := validateNamePart("Dr.")
	if err != "" || got != "Dr." {
		t.Errorf("validateNamePart(%q) = %q, %q, want \"Dr.\", no error", "Dr.", got, err)
	}
}

// Ported from Domain/Contacts/Common/ContactValueObjectTests.cs.
// ContactRoleTests.TryCreate_WithValidValue_TrimsAndSucceeds.
func TestValidateContactRole_TrimsAndSucceeds(t *testing.T) {
	got, err := validateContactRole("  CEO  ")
	if err != "" || got != "CEO" {
		t.Errorf("validateContactRole(%q) = %q, %q, want \"CEO\", no error", "  CEO  ", got, err)
	}
}

// Ported from Domain/Contacts/Common/ContactValueObjectTests.cs.
// ContactRoleTests.TryCreate_WithBlankValue_Fails. Exact message text: the
// mutation a Task 7 review round planted here (paraphrasing this string)
// survived because no test asserted it before.
func TestValidateContactRole_BlankIsInvalid(t *testing.T) {
	const want = "A role cannot be null or empty"
	for _, v := range []string{"", "   "} {
		if _, err := validateContactRole(v); err != want {
			t.Errorf("validateContactRole(%q) = error %q, want %q", v, err, want)
		}
	}
}

// Not a port: ContactRoleTests has no too-long test in .NET, but it is a
// real branch in ContactRole.Validate with its own exact message.
func TestValidateContactRole_TooLongIsInvalid(t *testing.T) {
	v := strings.Repeat("a", 256)
	want := "A role cannot be longer than 255 characters, the given value was 256 characters"
	if _, err := validateContactRole(v); err != want {
		t.Errorf("validateContactRole(256 chars) = error %q, want %q", err, want)
	}
}

// Not a port: pins the .NET divergence GetContactsEndpoint.Handler's search
// term split must not diverge on (item 5b of the Task 7 fix round). .NET
// splits on the ' ' character only
// (`request.Search.Split(' ', RemoveEmptyEntries | TrimEntries)`); a
// strings.Fields-based split would treat any Unicode whitespace, tabs
// included, as a separator, turning "foo\tbar" into two terms ("foo" and
// "bar", each independently required to match) instead of .NET's one
// ("foo\tbar" verbatim).
func TestSearchPatterns_SplitsOnSpaceCharacterOnly(t *testing.T) {
	search := "foo\tbar"
	got := searchPatterns(&search)
	want := []string{likePattern("foo\tbar")}
	if !slices.Equal(got, want) {
		t.Errorf("searchPatterns(%q) = %v, want %v (tab is not a term separator)", search, got, want)
	}
}

// validateCustomerType is the customer type value object
// (00007_customers_type.sql): business or person, case-insensitive, and —
// unlike validateLegalType — an enforced set.
func TestValidateCustomerType_NormalizesAndRejects(t *testing.T) {
	for raw, want := range map[string]string{" Business ": "business", "PERSON": "person"} {
		if got, err := validateCustomerType(raw); err != "" || got != want {
			t.Errorf("validateCustomerType(%q) = %q, %q, want %q, no error", raw, got, err, want)
		}
	}
	if _, err := validateCustomerType("  "); err != "A customer type cannot be null or empty" {
		t.Errorf("validateCustomerType(blank) = error %q, want the blank message", err)
	}
	want := "A customer type must be one of 'business' or 'person', but was 'spaceship'"
	if _, err := validateCustomerType("spaceship"); err != want {
		t.Errorf("validateCustomerType(spaceship) = error %q, want %q", err, want)
	}
}

// identityTypeMismatch reports a legal identity whose type disagrees with
// the customer type it would be attached to, and nothing otherwise.
func TestIdentityTypeMismatch(t *testing.T) {
	if got := identityTypeMismatch("business", nil); got != "" {
		t.Errorf("identityTypeMismatch(business, nil) = %q, want empty", got)
	}
	if got := identityTypeMismatch("business", &legalIdentity{Type: "business"}); got != "" {
		t.Errorf("identityTypeMismatch(business, business) = %q, want empty", got)
	}
	want := "A legal identity's type must match the customer type 'person', but was 'business'"
	if got := identityTypeMismatch("person", &legalIdentity{Type: "business"}); got != want {
		t.Errorf("identityTypeMismatch(person, business) = %q, want %q", got, want)
	}
}

// TestValidateEmail_ValidValuesSucceedWithoutLowercasing pins D2's departure
// from validateEmailAddress: the local part's casing is kept as given, only
// whitespace is trimmed.
func TestValidateEmail_ValidValuesSucceedWithoutLowercasing(t *testing.T) {
	cases := map[string]string{
		"anders@vantigo.io":   "anders@vantigo.io",
		"  Anders@Vantigo.IO": "Anders@Vantigo.IO",
		"a@b.co":              "a@b.co",
	}
	for v, want := range cases {
		if got, err := validateEmail(v); err != "" || got != want {
			t.Errorf("validateEmail(%q) = %q, %q, want %q, no error", v, got, err, want)
		}
	}
}

func TestValidateEmail_InvalidShapeFails(t *testing.T) {
	for _, v := range []string{"no-at-sign", "@vantigo.io", "anders@", "anders@vantigo", "anders@@vantigo.io"} {
		want := fmt.Sprintf("An email address must look like name@example.com, but was '%s'", v)
		if _, err := validateEmail(v); err != want {
			t.Errorf("validateEmail(%q) = error %q, want %q", v, err, want)
		}
	}
}

func TestValidateEmail_TooLongIsInvalid(t *testing.T) {
	v := strings.Repeat("a", 251) + "@a.io" // 256 characters
	want := "An email address cannot be longer than 255 characters, the given value was 256 characters"
	if _, err := validateEmail(v); err != want {
		t.Errorf("validateEmail(256 chars) = error %q, want %q", err, want)
	}
}

func TestValidatePhone_CommonFormatsSucceed(t *testing.T) {
	for _, v := range []string{"+47 934 89 731", "(555) 123-4567", "22864400"} {
		if got, err := validatePhone(v); err != "" || got != v {
			t.Errorf("validatePhone(%q) = %q, %q, want %q, no error", v, got, err, v)
		}
	}
}

// TestValidatePhone_FewerThanFiveDigitsFails pins D2's own floor: at least
// five digits, unlike validatePhoneNumber's "at least one".
func TestValidatePhone_FewerThanFiveDigitsFails(t *testing.T) {
	for _, v := range []string{"", "   ", "1234", "+47 12"} {
		want := fmt.Sprintf("A phone number may only contain digits, spaces and + - ( ), and needs at least five digits, but was '%s'", v)
		if _, err := validatePhone(v); err != want {
			t.Errorf("validatePhone(%q) = error %q, want %q", v, err, want)
		}
	}
}

// TestValidatePhone_DisallowedCharacterFails pins that '.' — allowed by
// validatePhoneNumber — is not allowed here (D2's own character set has no
// dot).
func TestValidatePhone_DisallowedCharacterFails(t *testing.T) {
	for _, v := range []string{"22.86.44.00", "not a number", "+47 934 89 731 ext#2"} {
		want := fmt.Sprintf("A phone number may only contain digits, spaces and + - ( ), and needs at least five digits, but was '%s'", v)
		if _, err := validatePhone(v); err != want {
			t.Errorf("validatePhone(%q) = error %q, want %q", v, err, want)
		}
	}
}

func TestValidatePhone_TooLongIsInvalid(t *testing.T) {
	v := strings.Repeat("1", 31)
	want := "A phone number cannot be longer than 30 characters, the given value was 31 characters"
	if _, err := validatePhone(v); err != want {
		t.Errorf("validatePhone(31 chars) = error %q, want %q", err, want)
	}
}

func TestValidateWebsite_ValidAbsoluteURLSucceeds(t *testing.T) {
	for _, v := range []string{"https://vantigo.io", "http://example.com/path?x=1"} {
		if got, err := validateWebsite(v); err != "" || got != v {
			t.Errorf("validateWebsite(%q) = %q, %q, want %q, no error", v, got, err, v)
		}
	}
}

func TestValidateWebsite_NotAnAbsoluteHttpUrlFails(t *testing.T) {
	for _, v := range []string{"vantigo.io", "ftp://vantigo.io", "https://", "not a url"} {
		want := fmt.Sprintf("A website must be an absolute http or https URL, but was '%s'", v)
		if _, err := validateWebsite(v); err != want {
			t.Errorf("validateWebsite(%q) = error %q, want %q", v, err, want)
		}
	}
}

func TestValidateWebsite_TooLongIsInvalid(t *testing.T) {
	v := "https://" + strings.Repeat("a", 2038) + ".io" // 8 + 2038 + 3 = 2049 characters
	want := "A website cannot be longer than 2048 characters, the given value was 2049 characters"
	if _, err := validateWebsite(v); err != want {
		t.Errorf("validateWebsite(2049 chars) = error %q, want %q", err, want)
	}
}
