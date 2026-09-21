package customers

import (
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/customers/gen"
)

// This file is GET/PUT /customers/{id}/billing-profile's value objects
// (invoice-ready customer design D1, D4): the unit-level coverage of
// billing_values.go's validators, in package customers (like values_test.go)
// so it can call the unexported validators directly — shaped like
// values_test.go's own coverage of this module's other value objects:
// table-driven where a value has several equivalent invalid shapes, one test
// per rule otherwise.

func TestValidateBillingCurrency_UppercasesValidCodes(t *testing.T) {
	got, err := validateBillingCurrency("usd")
	if err != "" {
		t.Fatalf("unexpected error %q", err)
	}
	if got != "USD" {
		t.Errorf("got %q, want USD", got)
	}
}

func TestValidateBillingCurrency_WrongLengthIsInvalid(t *testing.T) {
	_, err := validateBillingCurrency("US")
	want := "A currency must be a three-letter ISO 4217 code, but was 'US'"
	if err != want {
		t.Errorf("err = %q, want %q", err, want)
	}
}

func TestValidateBillingLanguage_NormalizesAndRejects(t *testing.T) {
	if got, err := validateBillingLanguage(" NB "); err != "" || got != "nb" {
		t.Errorf("got %q err %q, want nb, no error", got, err)
	}
	if got, err := validateBillingLanguage("en"); err != "" || got != "en" {
		t.Errorf("got %q err %q, want en, no error", got, err)
	}
	_, err := validateBillingLanguage("fr")
	want := "A document language must be one of 'nb' or 'en', but was 'fr'"
	if err != want {
		t.Errorf("err = %q, want %q", err, want)
	}
}

func TestValidateInvoiceDelivery_NormalizesAndRejects(t *testing.T) {
	for _, v := range []string{"email", "ehf", "efaktura", "paper"} {
		if got, err := validateInvoiceDelivery(v); err != "" || got != v {
			t.Errorf("validateInvoiceDelivery(%q) = %q, %q, want %q, no error", v, got, err, v)
		}
	}
	_, err := validateInvoiceDelivery("fax")
	want := "An invoice delivery method must be one of 'email', 'ehf', 'efaktura' or 'paper', but was 'fax'"
	if err != want {
		t.Errorf("err = %q, want %q", err, want)
	}
}

func TestValidateReminderDelivery_NormalizesAndRejects(t *testing.T) {
	for _, v := range []string{"email", "paper"} {
		if got, err := validateReminderDelivery(v); err != "" || got != v {
			t.Errorf("validateReminderDelivery(%q) = %q, %q, want %q, no error", v, got, err, v)
		}
	}
	_, err := validateReminderDelivery("ehf")
	want := "A reminder delivery method must be one of 'email' or 'paper', but was 'ehf'"
	if err != want {
		t.Errorf("err = %q, want %q", err, want)
	}
}

func TestValidatePeppolID_ValidShapeSucceeds(t *testing.T) {
	got, err := validatePeppolID(" 9908:AbC-123 ")
	if err != "" {
		t.Fatalf("unexpected error %q", err)
	}
	if got != "9908:AbC-123" {
		t.Errorf("got %q, want case preserved 9908:AbC-123", got)
	}
}

func TestValidatePeppolID_WrongShapeIsInvalid(t *testing.T) {
	_, err := validatePeppolID("not-a-peppol-id")
	want := "A Peppol participant id must look like 0192:923609016 (a four-digit scheme, a colon, an identifier), but was 'not-a-peppol-id'"
	if err != want {
		t.Errorf("err = %q, want %q", err, want)
	}
}

func TestValidatePeppolID_Scheme0192_ValidOrgNumberSucceeds(t *testing.T) {
	got, err := validatePeppolID("0192:923609016")
	if err != "" {
		t.Fatalf("unexpected error %q", err)
	}
	if got != "0192:923609016" {
		t.Errorf("got %q, want 0192:923609016", got)
	}
}

// TestValidatePeppolID_Scheme0192_InvalidOrgNumberReusesTheOrgNumberMessage
// pins the controller ruling: scheme 0192 with a bad number reuses the
// organisation-number message (values.go's validNorwegianOrgNumber), quoting
// the identifier alone, not the whole "scheme:identifier" value.
func TestValidatePeppolID_Scheme0192_InvalidOrgNumberReusesTheOrgNumberMessage(t *testing.T) {
	_, err := validatePeppolID("0192:923609017")
	want := "A Norwegian organisation number must be nine digits with a valid check digit, but was '923609017'"
	if err != want {
		t.Errorf("err = %q, want %q", err, want)
	}
}

// TestValidatePeppolID_OtherSchemeAcceptsAnyIdentifierShape proves the
// Norwegian organisation-number rule only applies to scheme 0192: any other
// four-digit scheme accepts an identifier on shape alone.
func TestValidatePeppolID_OtherSchemeAcceptsAnyIdentifierShape(t *testing.T) {
	got, err := validatePeppolID("9908:not-an-org-number")
	if err != "" {
		t.Fatalf("unexpected error %q", err)
	}
	if got != "9908:not-an-org-number" {
		t.Errorf("got %q, want 9908:not-an-org-number", got)
	}
}

// TestValidGLN_ChecksTheGS1CheckDigit pins validGLN against two
// independently verifiable values: 4006381333931 is a widely published GS1
// example GTIN-13 (Ritter Sport's own barcode); 1234567890128 is worked by
// hand in validGLN's own doc comment (weighting 123456789012's twelve
// digits 3,1,3,1,… from the right sums to 92, so the check digit is
// (10 − 92 mod 10) mod 10 = 8). Each is also tried with its last digit
// wrong, which must fail, alongside a too-short, too-long and
// non-digit case.
func TestValidGLN_ChecksTheGS1CheckDigit(t *testing.T) {
	valid := []string{"4006381333931", "1234567890128"}
	for _, s := range valid {
		if !validGLN(s) {
			t.Errorf("validGLN(%q) = false, want true", s)
		}
	}
	invalid := []string{
		"4006381333930",  // wrong check digit
		"1234567890120",  // wrong check digit
		"123456789012",   // twelve digits
		"12345678901288", // fourteen digits
		"123456789012a",  // non-digit
	}
	for _, s := range invalid {
		if validGLN(s) {
			t.Errorf("validGLN(%q) = true, want false", s)
		}
	}
}

func TestValidateGLN_StripsWhitespaceAndReportsRawOnError(t *testing.T) {
	got, err := validateGLN("4006 3813 3393 1")
	if err != "" {
		t.Fatalf("unexpected error %q", err)
	}
	if got != "4006381333931" {
		t.Errorf("got %q, want whitespace stripped 4006381333931", got)
	}

	_, err = validateGLN("not-a-gln")
	want := "A GLN must be 13 digits with a valid check digit, but was 'not-a-gln'"
	if err != want {
		t.Errorf("err = %q, want %q", err, want)
	}
}

func TestValidatePaymentTermsDays_RangeIsZeroTo365(t *testing.T) {
	errs := map[string][]string{}
	zero := int32(0)
	if got := validatePaymentTermsDays(&zero, errs); got == nil || *got != 0 {
		t.Errorf("got %v, want 0", got)
	}
	max := int32(365)
	if got := validatePaymentTermsDays(&max, errs); got == nil || *got != 365 {
		t.Errorf("got %v, want 365", got)
	}
	if len(errs) != 0 {
		t.Fatalf("unexpected errors %v", errs)
	}

	tooHigh := int32(366)
	errs = map[string][]string{}
	if got := validatePaymentTermsDays(&tooHigh, errs); got != nil {
		t.Errorf("got %v, want nil", got)
	}
	want := "Payment terms must be between 0 and 365 days, but was 366"
	if msgs := errs["paymentTermsDays"]; len(msgs) != 1 || msgs[0] != want {
		t.Errorf("errs[paymentTermsDays] = %v, want [%q]", msgs, want)
	}

	negative := int32(-1)
	errs = map[string][]string{}
	if got := validatePaymentTermsDays(&negative, errs); got != nil {
		t.Errorf("got %v, want nil", got)
	}
	want = "Payment terms must be between 0 and 365 days, but was -1"
	if msgs := errs["paymentTermsDays"]; len(msgs) != 1 || msgs[0] != want {
		t.Errorf("errs[paymentTermsDays] = %v, want [%q]", msgs, want)
	}
}

func billingStrPtr(s string) *string { return &s }

func TestValidateBillingProfile_ValidRequestNormalizesEveryField(t *testing.T) {
	terms := int32(30)
	req := gen.PutCustomerBillingProfileRequest{
		InvoiceEmail:     billingStrPtr("invoice@example.com"),
		ReminderEmail:    billingStrPtr("reminders@example.com"),
		PaymentTermsDays: &terms,
		Currency:         billingStrPtr("nok"),
		Language:         billingStrPtr("NB"),
		InvoiceDelivery:  billingStrPtr("EHF"),
		ReminderDelivery: billingStrPtr("Email"),
		PeppolId:         billingStrPtr("0192:923609016"),
		Gln:              billingStrPtr("4006381333931"),
		BuyerReference:   billingStrPtr("PO-123"),
	}
	got, errs := validateBillingProfile(req)
	if errs != nil {
		t.Fatalf("unexpected errors %v", errs)
	}
	if got.Currency == nil || *got.Currency != "NOK" {
		t.Errorf("Currency = %v, want NOK", got.Currency)
	}
	if got.Language == nil || *got.Language != "nb" {
		t.Errorf("Language = %v, want nb", got.Language)
	}
	if got.InvoiceDelivery == nil || *got.InvoiceDelivery != "ehf" {
		t.Errorf("InvoiceDelivery = %v, want ehf", got.InvoiceDelivery)
	}
	if got.ReminderDelivery == nil || *got.ReminderDelivery != "email" {
		t.Errorf("ReminderDelivery = %v, want email", got.ReminderDelivery)
	}
	if got.PeppolID == nil || *got.PeppolID != "0192:923609016" {
		t.Errorf("PeppolID = %v, want 0192:923609016", got.PeppolID)
	}
	if got.Gln == nil || *got.Gln != "4006381333931" {
		t.Errorf("Gln = %v, want 4006381333931", got.Gln)
	}
	if got.PaymentTermsDays == nil || *got.PaymentTermsDays != 30 {
		t.Errorf("PaymentTermsDays = %v, want 30", got.PaymentTermsDays)
	}
	if got.BuyerReference == nil || *got.BuyerReference != "PO-123" {
		t.Errorf("BuyerReference = %v, want PO-123", got.BuyerReference)
	}
}

// TestValidateBillingProfile_BlankAndAbsentFieldsClearToNull pins the
// controller ruling: blank/whitespace-only, explicit null and an absent key
// all mean the same thing — clear the field — for every string field, the
// same rule contact_info.go's validateContactInfo applies.
func TestValidateBillingProfile_BlankAndAbsentFieldsClearToNull(t *testing.T) {
	req := gen.PutCustomerBillingProfileRequest{
		InvoiceEmail: billingStrPtr("   "),
		Currency:     billingStrPtr(""),
		// every other field left absent (nil).
	}
	got, errs := validateBillingProfile(req)
	if errs != nil {
		t.Fatalf("unexpected errors %v", errs)
	}
	if got.InvoiceEmail != nil {
		t.Errorf("InvoiceEmail = %v, want nil", got.InvoiceEmail)
	}
	if got.Currency != nil {
		t.Errorf("Currency = %v, want nil", got.Currency)
	}
	if got.PaymentTermsDays != nil {
		t.Errorf("PaymentTermsDays = %v, want nil", got.PaymentTermsDays)
	}
}

// TestValidateBillingProfile_MultipleInvalidValuesReportsEachField proves
// every field is validated independently and every error reported together
// — never short-circuited on the first failure — the same shape
// validateLegalIdentity/validateAddress give their own multi-field requests.
func TestValidateBillingProfile_MultipleInvalidValuesReportsEachField(t *testing.T) {
	badTerms := int32(400)
	req := gen.PutCustomerBillingProfileRequest{
		Currency:         billingStrPtr("US"),
		Language:         billingStrPtr("fr"),
		PaymentTermsDays: &badTerms,
		Gln:              billingStrPtr("not-a-gln"),
	}
	_, errs := validateBillingProfile(req)
	for _, field := range []string{"currency", "language", "paymentTermsDays", "gln"} {
		if len(errs[field]) != 1 {
			t.Errorf("errs[%q] = %v, want exactly one error", field, errs[field])
		}
	}
	if len(errs) != 4 {
		t.Errorf("errs has %d keys, want 4: %v", len(errs), errs)
	}
}

func TestBillingProfileEqual(t *testing.T) {
	a := billingProfile{Currency: billingStrPtr("NOK")}
	b := billingProfile{Currency: billingStrPtr("NOK")}
	if !billingProfileEqual(a, b) {
		t.Errorf("billingProfileEqual(%+v, %+v) = false, want true", a, b)
	}
	c := billingProfile{Currency: billingStrPtr("SEK")}
	if billingProfileEqual(a, c) {
		t.Errorf("billingProfileEqual(%+v, %+v) = true, want false", a, c)
	}
	terms1 := int32(30)
	terms2 := int32(60)
	d := billingProfile{PaymentTermsDays: &terms1}
	e := billingProfile{PaymentTermsDays: &terms2}
	if billingProfileEqual(d, e) {
		t.Errorf("billingProfileEqual with different PaymentTermsDays = true, want false")
	}
}

func TestBillingWarnings_FixedOrder(t *testing.T) {
	// A profile that raises all four warnings at once must report them in
	// the brief's fixed order: ehf_without_recipient, email_without_address,
	// efaktura_for_business, no_invoice_address. invoiceDelivery can only be
	// one value at a time, so ehf/email/efaktura are exercised individually
	// below (no_invoice_address is independent of delivery and always
	// checked); this test only pins that no_invoice_address is always last
	// when it fires alongside one of the other three.
	ehf := "ehf"
	got := billingWarnings(billingProfile{InvoiceDelivery: &ehf}, "business", nil, nil, false)
	want := []string{"ehf_without_recipient", "no_invoice_address"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("billingWarnings = %v, want %v", got, want)
	}
}

func TestBillingWarnings_EHFWithoutRecipient(t *testing.T) {
	ehf := "ehf"
	profile := billingProfile{InvoiceDelivery: &ehf}

	// No peppolId, no Norwegian business identity: the warning fires.
	got := billingWarnings(profile, "business", nil, nil, true)
	if !contains(got, "ehf_without_recipient") {
		t.Errorf("warnings = %v, want ehf_without_recipient", got)
	}

	// A peppolId set clears it, identity absent.
	peppol := "0192:923609016"
	withPeppol := billingProfile{InvoiceDelivery: &ehf, PeppolID: &peppol}
	got = billingWarnings(withPeppol, "business", nil, nil, true)
	if contains(got, "ehf_without_recipient") {
		t.Errorf("warnings = %v, want no ehf_without_recipient (peppolId set)", got)
	}

	// A Norwegian business legal identity clears it even without a peppolId.
	identity := &legalIdentity{Country: "no", Type: "business", ID: "923609016", Name: "Acme AS", Source: "manual"}
	got = billingWarnings(profile, "business", identity, nil, true)
	if contains(got, "ehf_without_recipient") {
		t.Errorf("warnings = %v, want no ehf_without_recipient (NO business identity)", got)
	}

	// A Norwegian *person* identity does not derive a recipient: the warning
	// still fires.
	personIdentity := &legalIdentity{Country: "no", Type: "person", ID: "010170-12345", Name: "Kari Nordmann", Source: "manual"}
	got = billingWarnings(profile, "person", personIdentity, nil, true)
	if !contains(got, "ehf_without_recipient") {
		t.Errorf("warnings = %v, want ehf_without_recipient (NO person identity does not count)", got)
	}
}

func TestBillingWarnings_EmailWithoutAddress(t *testing.T) {
	email := "email"
	profile := billingProfile{InvoiceDelivery: &email}

	got := billingWarnings(profile, "business", nil, nil, true)
	if !contains(got, "email_without_address") {
		t.Errorf("warnings = %v, want email_without_address", got)
	}

	// invoiceEmail set on the profile satisfies it.
	invoiceEmail := "invoice@example.com"
	withInvoiceEmail := billingProfile{InvoiceDelivery: &email, InvoiceEmail: &invoiceEmail}
	got = billingWarnings(withInvoiceEmail, "business", nil, nil, true)
	if contains(got, "email_without_address") {
		t.Errorf("warnings = %v, want no email_without_address (invoiceEmail set)", got)
	}

	// The contact-info email alone also satisfies it.
	contactEmail := "contact@example.com"
	got = billingWarnings(profile, "business", nil, &contactEmail, true)
	if contains(got, "email_without_address") {
		t.Errorf("warnings = %v, want no email_without_address (contact email set)", got)
	}
}

func TestBillingWarnings_EfakturaForBusiness(t *testing.T) {
	efaktura := "efaktura"
	profile := billingProfile{InvoiceDelivery: &efaktura}

	got := billingWarnings(profile, "business", nil, nil, true)
	if !contains(got, "efaktura_for_business") {
		t.Errorf("warnings = %v, want efaktura_for_business", got)
	}

	got = billingWarnings(profile, "person", nil, nil, true)
	if contains(got, "efaktura_for_business") {
		t.Errorf("warnings = %v, want no efaktura_for_business for a person customer", got)
	}
}

func TestBillingWarnings_NoInvoiceAddress(t *testing.T) {
	got := billingWarnings(billingProfile{}, "business", nil, nil, false)
	if !contains(got, "no_invoice_address") {
		t.Errorf("warnings = %v, want no_invoice_address", got)
	}
	got = billingWarnings(billingProfile{}, "business", nil, nil, true)
	if contains(got, "no_invoice_address") {
		t.Errorf("warnings = %v, want no no_invoice_address (has one)", got)
	}
}

// TestBillingWarnings_EmptyProfileReturnsAnEmptyNonNilSlice proves the JSON
// response encodes warnings as [] rather than null when nothing fires.
func TestBillingWarnings_EmptyProfileReturnsAnEmptyNonNilSlice(t *testing.T) {
	got := billingWarnings(billingProfile{}, "business", nil, nil, true)
	if got == nil {
		t.Fatal("warnings = nil, want a non-nil empty slice")
	}
	if len(got) != 0 {
		t.Errorf("warnings = %v, want empty", got)
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
