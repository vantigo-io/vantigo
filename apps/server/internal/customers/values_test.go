package customers

import (
	"slices"
	"sort"
	"strings"
	"testing"
)

// Ported from Domain/Customers/Common/FriendlyNameTests.cs.
// Constructor_WithNullOrWhitespace_ThrowsDomainException.
func TestValidateFriendlyName_BlankIsInvalid(t *testing.T) {
	for _, v := range []string{"", "   "} {
		if _, err := validateFriendlyName(v); err == "" {
			t.Errorf("validateFriendlyName(%q) = no error, want one", v)
		}
	}
}

// Ported from Domain/Customers/Common/FriendlyNameTests.cs.
// Constructor_WithValueLongerThanMaxLength_ThrowsDomainException.
func TestValidateFriendlyName_TooLongIsInvalid(t *testing.T) {
	v := strings.Repeat("a", 256)
	if _, err := validateFriendlyName(v); err == "" {
		t.Error("validateFriendlyName(256 chars) = no error, want one")
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
	for _, v := range []string{"", "   "} {
		if _, err := validateCountryCode(v); err == "" {
			t.Errorf("validateCountryCode(%q) = no error, want one", v)
		}
	}
}

// Ported from Domain/Customers/Common/CountryCodeTests.cs.
// Constructor_TrimsAndLowercasesValue.
func TestValidateCountryCode_TrimsAndLowercases(t *testing.T) {
	got, err := validateCountryCode(" NO ")
	if err != "" || got != "no" {
		t.Errorf("validateCountryCode(%q) = %q, %q, want \"no\", no error", " NO ", got, err)
	}
}

// Ported from Domain/Customers/Common/LegalIdTests.cs.
// Constructor_WithNullOrWhitespace_ThrowsDomainException.
func TestValidateLegalID_BlankIsInvalid(t *testing.T) {
	for _, v := range []string{"", "   "} {
		if _, err := validateLegalID(v); err == "" {
			t.Errorf("validateLegalID(%q) = no error, want one", v)
		}
	}
}

// Ported from Domain/Customers/Common/LegalIdTests.cs.
// Constructor_WithValueLongerThanMaxLength_ThrowsDomainException.
func TestValidateLegalID_TooLongIsInvalid(t *testing.T) {
	v := strings.Repeat("1", 51)
	if _, err := validateLegalID(v); err == "" {
		t.Error("validateLegalID(51 chars) = no error, want one")
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

// Ported from Domain/Customers/Common/LegalNameTests.cs.
// Constructor_WithNullOrWhitespace_ThrowsDomainException.
func TestValidateLegalName_BlankIsInvalid(t *testing.T) {
	for _, v := range []string{"", "   "} {
		if _, err := validateLegalName(v); err == "" {
			t.Errorf("validateLegalName(%q) = no error, want one", v)
		}
	}
}

// Ported from Domain/Customers/Common/LegalNameTests.cs.
// Constructor_WithValueLongerThanMaxLength_ThrowsDomainException.
func TestValidateLegalName_TooLongIsInvalid(t *testing.T) {
	v := strings.Repeat("a", 256)
	if _, err := validateLegalName(v); err == "" {
		t.Error("validateLegalName(256 chars) = no error, want one")
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
	for _, v := range []string{"", "   "} {
		if _, err := validateLegalType(v); err == "" {
			t.Errorf("validateLegalType(%q) = no error, want one", v)
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
// otherwise only exercises indirectly through the HTTP layer.
func TestValidateLegalSource_InvalidIsRejected(t *testing.T) {
	for _, v := range []string{"", "   ", "bogus", "BRREG!"} {
		if _, err := validateLegalSource(v); err == "" {
			t.Errorf("validateLegalSource(%q) = no error, want one", v)
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

// Ported from Domain/Contacts/Common/ContactValueObjectTests.cs.
// PersonNameTests.Construction_TrimsTheValue.
func TestValidatePersonName_Trims(t *testing.T) {
	got, err := validatePersonName("  Anders  ")
	if err != "" || got != "Anders" {
		t.Errorf("validatePersonName(%q) = %q, %q, want \"Anders\", no error", "  Anders  ", got, err)
	}
}

// Ported from Domain/Contacts/Common/ContactValueObjectTests.cs.
// PersonNameTests.TryCreate_WithBlankValue_Fails.
func TestValidatePersonName_BlankIsInvalid(t *testing.T) {
	for _, v := range []string{"", "   "} {
		if _, err := validatePersonName(v); err == "" {
			t.Errorf("validatePersonName(%q) = no error, want one", v)
		}
	}
}

// Ported from Domain/Contacts/Common/ContactValueObjectTests.cs.
// PersonNameTests.TryCreate_WithTooLongValue_Fails.
func TestValidatePersonName_TooLongIsInvalid(t *testing.T) {
	v := strings.Repeat("a", 101)
	if _, err := validatePersonName(v); err == "" {
		t.Error("validatePersonName(101 chars) = no error, want one")
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

// Ported from Domain/Contacts/Common/ContactValueObjectTests.cs.
// PhoneNumberTests.TryCreate_WithInvalidCharactersOrNoDigits_Fails.
func TestValidatePhoneNumber_InvalidCharactersOrNoDigitsFails(t *testing.T) {
	for _, v := range []string{"not a number", "+47 934 89 731 ext#2", "+-() ."} {
		if _, err := validatePhoneNumber(v); err == "" {
			t.Errorf("validatePhoneNumber(%q) = no error, want one", v)
		}
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

// Ported from Domain/Contacts/Common/ContactValueObjectTests.cs.
// EmailAddressTests.TryCreate_WithInvalidShape_Fails.
func TestValidateEmailAddress_InvalidShapeFails(t *testing.T) {
	for _, v := range []string{
		"no-at-sign", "@vantigo.io", "anders@", "anders@vantigo", "anders@vantigo.",
		"an ders@vantigo.io", "anders@@vantigo.io",
	} {
		if _, err := validateEmailAddress(v); err == "" {
			t.Errorf("validateEmailAddress(%q) = no error, want one", v)
		}
	}
}

// Ported from Domain/Contacts/Common/ContactValueObjectTests.cs.
// NamePartTests.TryCreate_WithTooLongValue_Fails.
func TestValidateNamePart_TooLongIsInvalid(t *testing.T) {
	v := strings.Repeat("a", 21)
	if _, err := validateNamePart(v); err == "" {
		t.Error("validateNamePart(21 chars) = no error, want one")
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
// ContactRoleTests.TryCreate_WithBlankValue_Fails.
func TestValidateContactRole_BlankIsInvalid(t *testing.T) {
	for _, v := range []string{"", "   "} {
		if _, err := validateContactRole(v); err == "" {
			t.Errorf("validateContactRole(%q) = no error, want one", v)
		}
	}
}
