package customers

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/customers/gen"
)

// This file is GET/PUT /customers/{id}/billing-profile's value objects
// (invoice-ready customer design D1, D4): payment terms, currency, document
// language, delivery methods and the identifiers used to send a customer
// invoices. No .NET ancestor, since a billing profile is new to this port;
// shaped like values.go's own address value objects — every field validated
// independently, every error reported together, keyed by the request's own
// JSON field name — and like contact_info.go's normalizedOrNil: blank or
// absent always means "clear to NULL", never a validation error.

// billingProfile is a customer's normalized billing profile: each field nil
// when the customer has none, exactly the shape customers.customers' eleven
// nullable billing columns store. Field names follow
// store.GetCustomerBillingProfileRow's own spelling (PeppolID, Gln — sqlc's
// initialism handling, not oapi-codegen's gen.CustomerBillingProfile.PeppolId)
// since this type sits closer to the database row than to the wire; json
// tags let it double as the timeline payload's before/after shape
// (timeline_events.go's recordCustomerBillingProfileUpdated), the same
// convention contactInfo (contact_info.go) follows.
type billingProfile struct {
	InvoiceEmail     *string `json:"invoiceEmail"`
	ReminderEmail    *string `json:"reminderEmail"`
	PaymentTermsDays *int32  `json:"paymentTermsDays"`
	Currency         *string `json:"currency"`
	Language         *string `json:"language"`
	InvoiceDelivery  *string `json:"invoiceDelivery"`
	ReminderDelivery *string `json:"reminderDelivery"`
	PeppolID         *string `json:"peppolId"`
	Gln              *string `json:"gln"`
	BuyerReference   *string `json:"buyerReference"`
	// DefaultBillRate is the customer's default hourly bill rate (customers
	// bill-rate design D1), quoted in Currency — never set without one — and
	// read by Time's rate chain through the directory. *float64 like every
	// money field a contract carries; the column is numeric(12,2).
	DefaultBillRate *float64 `json:"defaultBillRate"`
}

// billingProfileEqual reports whether a and b are the same billing profile:
// every field equal, nil included. PutCustomersByIdBillingProfile's caller
// uses it to decide whether the request is a no-op (customers foundation
// design D5's no-op rule), the same shape contactInfoEqual gives
// PutCustomersByIdContactInfo.
func billingProfileEqual(a, b billingProfile) bool {
	return stringPtrEqual(a.InvoiceEmail, b.InvoiceEmail) &&
		stringPtrEqual(a.ReminderEmail, b.ReminderEmail) &&
		int32PtrEqual(a.PaymentTermsDays, b.PaymentTermsDays) &&
		stringPtrEqual(a.Currency, b.Currency) &&
		stringPtrEqual(a.Language, b.Language) &&
		stringPtrEqual(a.InvoiceDelivery, b.InvoiceDelivery) &&
		stringPtrEqual(a.ReminderDelivery, b.ReminderDelivery) &&
		stringPtrEqual(a.PeppolID, b.PeppolID) &&
		stringPtrEqual(a.Gln, b.Gln) &&
		stringPtrEqual(a.BuyerReference, b.BuyerReference) &&
		floatPtrEqual(a.DefaultBillRate, b.DefaultBillRate)
}

// int32PtrEqual is stringPtrEqual's *int32 twin, needed for
// paymentTermsDays alone among this profile's eleven fields.
func int32PtrEqual(a, b *int32) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// floatPtrEqual is stringPtrEqual's *float64 twin, for defaultBillRate. Exact
// equality, not a tolerance, is right here: both sides are at most two
// decimals — one read back from numeric(12,2)'s text, the other validated to
// that precision — and the same decimal always parses to the same float64.
func floatPtrEqual(a, b *float64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// billingProfileFromRow is a persisted customer row's billing profile —
// store.GetCustomerBillingProfileRow and store.UpdateCustomerBillingProfileRow
// share this shape field for field, so billing_profile.go calls this with
// either. defaultBillRate arrives already read off its numeric column
// (floatPtrFromNumeric), because that read can fail and this cannot. The Peppol
// lookups pass nil whether or not their row carries the rate: they build the
// profile only to derive a participant from it, which the rate never touches,
// so reading it there would be a conversion that could fail for nothing.
func billingProfileFromRow(invoiceEmail, reminderEmail *string, paymentTermsDays *int32, currency, language, invoiceDelivery, reminderDelivery, peppolID, gln, buyerReference *string, defaultBillRate *float64) billingProfile {
	return billingProfile{
		InvoiceEmail: invoiceEmail, ReminderEmail: reminderEmail, PaymentTermsDays: paymentTermsDays,
		Currency: currency, Language: language, InvoiceDelivery: invoiceDelivery, ReminderDelivery: reminderDelivery,
		PeppolID: peppolID, Gln: gln, BuyerReference: buyerReference, DefaultBillRate: defaultBillRate,
	}
}

// numericFromFloatPtr is a default bill rate on its way into numeric(12,2):
// nil stays an invalid (SQL NULL) pgtype.Numeric, and a value is scanned from
// its shortest decimal text — the digits the caller sent, never the binary
// fraction a float64 happens to hold. internal/projects/values.go's own rule
// for its money columns, duplicated because depguard forbids the import. What
// is not a number is refused before formatting: an infinity would fail Scan
// anyway, but a NaN formats as "NaN" and scans without error into a numeric
// NaN — a valid value to pgtype, and a rate nobody typed. JSON carries neither,
// so this is a floor under the handler, not a path a request takes. Scan's own
// error is returned, not dropped: discarding it would store a NULL for a number
// the caller actually sent.
func numericFromFloatPtr(v *float64) (pgtype.Numeric, error) {
	if v == nil {
		return pgtype.Numeric{}, nil
	}
	if math.IsNaN(*v) || math.IsInf(*v, 0) {
		return pgtype.Numeric{}, fmt.Errorf("customers: %v is not a storable decimal", *v)
	}
	var n pgtype.Numeric
	if err := n.Scan(strconv.FormatFloat(*v, 'f', -1, 64)); err != nil {
		return pgtype.Numeric{}, fmt.Errorf("customers: %v is not a storable decimal: %w", *v, err)
	}
	return n, nil
}

// floatPtrFromNumeric reads a default bill rate back off its column: nil for a
// SQL NULL, never 0, which would be a rate somebody set. A number the column
// holds but Go cannot read is an error rather than a nil — rendering it absent
// would tell the caller the rate was never set — though numeric(12,2) cannot
// hold one, so nothing reachable produces it today. Projects' rule again.
func floatPtrFromNumeric(n pgtype.Numeric) (*float64, error) {
	if !n.Valid {
		return nil, nil
	}
	f, err := n.Float64Value()
	if err != nil {
		return nil, fmt.Errorf("customers: read a stored decimal: %w", err)
	}
	if !f.Valid {
		return nil, nil
	}
	return &f.Float64, nil
}

// derivedPeppolID is the EHF recipient this module can derive from the
// customer's own legal identity, "" when there is none (final review fix
// wave, finding I1): the one predicate both directory.go's
// resolveBillingProfile and billing_profile.go's billingWarnings now share,
// so a stored row can never make the two disagree about whether a recipient
// exists. "0192:" + identity.ID only when identity is set, its country is
// "no", customerType — the customer's own type, not identity.Type, which a
// legacy row can hold NULL while the customer itself is still "business" —
// is "business", and identity.ID itself passes validNorwegianOrgNumber
// (values.go): a row written before this module validated legal ids at all
// can hold something like "NO 923 609 016 MVA", and handing that to
// Invoices as "0192:NO 923 609 016 MVA" would be worse than deriving
// nothing.
func derivedPeppolID(identity *legalIdentity, customerType string) string {
	if identity == nil || identity.Country != "no" || customerType != "business" {
		return ""
	}
	if !validNorwegianOrgNumber(identity.ID) {
		return ""
	}
	return "0192:" + identity.ID
}

// currencyPattern is an ISO-4217 alphabetic code's shape: three letters,
// nothing else — the set of actually-assigned codes is not checked, the same
// shape-only rule internal/projects/values.go's own currencyPattern applies
// (a cross-module duplicate, not a shared import: depguard forbids this
// module importing projects, and the pattern is one line).
var currencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)

// validateBillingCurrency is D4's currency rule: upper-cased before the
// shape check, the message quoting the raw, unnormalized value.
func validateBillingCurrency(raw string) (string, string) {
	upper := strings.ToUpper(strings.TrimSpace(raw))
	if !currencyPattern.MatchString(upper) {
		return "", fmt.Sprintf("A currency must be a three-letter ISO 4217 code, but was '%s'", raw)
	}
	return upper, ""
}

// validateBillingLanguage is D4's document-language rule: one of nb/en,
// lower-cased before the set check, the message quoting the raw value —
// shaped like validateCustomerStatus.
func validateBillingLanguage(raw string) (string, string) {
	lower := strings.ToLower(strings.TrimSpace(raw))
	switch lower {
	case "nb", "en":
		return lower, ""
	default:
		return "", fmt.Sprintf("A document language must be one of 'nb' or 'en', but was '%s'", raw)
	}
}

// validateInvoiceDelivery is D4's invoice-delivery rule: one of email/ehf/
// efaktura/paper, lower-cased before the set check.
func validateInvoiceDelivery(raw string) (string, string) {
	lower := strings.ToLower(strings.TrimSpace(raw))
	switch lower {
	case "email", "ehf", "efaktura", "paper":
		return lower, ""
	default:
		return "", fmt.Sprintf("An invoice delivery method must be one of 'email', 'ehf', 'efaktura' or 'paper', but was '%s'", raw)
	}
}

// validateReminderDelivery is D4's reminder-delivery rule: one of email/
// paper — reminders cannot travel as EHF or eFaktura (D4), so this is its
// own, narrower set than validateInvoiceDelivery's.
func validateReminderDelivery(raw string) (string, string) {
	lower := strings.ToLower(strings.TrimSpace(raw))
	switch lower {
	case "email", "paper":
		return lower, ""
	default:
		return "", fmt.Sprintf("A reminder delivery method must be one of 'email' or 'paper', but was '%s'", raw)
	}
}

// peppolIDPattern is <4-digit scheme>:<identifier> (D4): the identifier is
// 1-50 characters of letters, digits and hyphens, case preserved.
var peppolIDPattern = regexp.MustCompile(`^([0-9]{4}):([A-Za-z0-9-]{1,50})$`)

// validatePeppolID is D4's Peppol participant id rule: trimmed, then matched
// whole against peppolIDPattern. Scheme 0192 (Norway's organisasjonsnummer
// scheme) additionally requires the identifier to be a valid Norwegian
// organisation number (validNorwegianOrgNumber, values.go) — reusing that
// same check's own message, quoting the identifier alone, not the whole
// "scheme:identifier" value, since the identifier is the part that failed.
// Every other scheme's identifier is accepted on shape alone, the same
// "not every rule needs a registry" stance validateLegalID takes for a
// non-Norwegian legal id.
func validatePeppolID(raw string) (string, string) {
	trimmed := strings.TrimSpace(raw)
	m := peppolIDPattern.FindStringSubmatch(trimmed)
	if m == nil {
		return "", fmt.Sprintf("A Peppol participant id must look like 0192:923609016 (a four-digit scheme, a colon, an identifier), but was '%s'", raw)
	}
	scheme, identifier := m[1], m[2]
	if scheme == "0192" && !validNorwegianOrgNumber(identifier) {
		return "", fmt.Sprintf("A Norwegian organisation number must be nine digits with a valid check digit, but was '%s'", identifier)
	}
	return scheme + ":" + identifier, ""
}

// validGLN is the GS1 mod-10 check-digit test for a GLN (Global Location
// Number, D4): s must already be exactly 13 ASCII digits (validateGLN below
// strips whitespace and checks that before ever calling this). The check
// digit — s's last character — is verified against the twelve digits before
// it, weighted 3, 1, 3, 1, … starting from the digit immediately to its
// left (i.e. from the right, excluding the check digit itself); check =
// (10 − sum mod 10) mod 10. 1234567890128 is a worked example: weighting
// 123456789012's twelve digits 3,1,3,1,3,1,3,1,3,1,3,1 from the right gives
// a weighted sum of 92, so (10 − 92 mod 10) mod 10 = (10 − 2) mod 10 = 8 —
// the trailing digit above.
func validGLN(s string) bool {
	if len(s) != 13 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	sum := 0
	weight := 3
	for i := 11; i >= 0; i-- {
		sum += int(s[i]-'0') * weight
		if weight == 3 {
			weight = 1
		} else {
			weight = 3
		}
	}
	check := (10 - sum%10) % 10
	return int(s[12]-'0') == check
}

// validateGLN is D4's field-level GLN rule: every whitespace character
// stripped (stripWhitespace, values.go — not merely trimmed, the same
// treatment an organisasjonsnummer gets), then validGLN's shape-and-checksum
// test. The message quotes the raw, unnormalized value, as every other
// enforced-set/checksum validator in this module does.
func validateGLN(raw string) (string, string) {
	stripped := stripWhitespace(raw)
	if !validGLN(stripped) {
		return "", fmt.Sprintf("A GLN must be 13 digits with a valid check digit, but was '%s'", raw)
	}
	return stripped, ""
}

// validatePaymentTermsDays is D4's payment-terms rule: 0-365 inclusive, absent
// left nil (the request never sends a "blank" integer the way a string field
// can be blank). The message is not the "but was '%s'" shape every string
// validator in this module uses — there is no raw string to quote, only the
// integer itself.
//
// field is the request's own name for the value, because the rule now has two
// callers with two spellings: the billing profile's own paymentTermsDays and a
// group's defaultPaymentTermsDays (customer groups design D2). One rule, one
// message, keyed under whichever field the caller actually sent — a second copy
// of "between 0 and 365" would be the thing that drifts the day the range moves.
func validatePaymentTermsDays(raw *int32, field string, errs map[string][]string) *int32 {
	if raw == nil {
		return nil
	}
	v := *raw
	if v < 0 || v > 365 {
		errs[field] = []string{fmt.Sprintf("Payment terms must be between 0 and 365 days, but was %d", v)}
		return nil
	}
	return &v
}

// maxBillRate is numeric(12,2)'s ceiling, default_bill_rate's column (migration
// 00028) — the width, and the number, internal/projects' maxAmount12 gives its
// own default bill rate. A rate past it is refused here rather than left to
// Postgres, whose 22003 the handler could only answer as a 500.
const maxBillRate = 9999999999.99

// validateDefaultBillRate is the customer default bill rate's own rule
// (customers bill-rate design D1): absent left nil — clearing it — and a value
// greater than zero, at most maxBillRate and at most two decimals. The
// precision half is projects' argument for its own money: the column is scale
// 2, so a third decimal would be rounded away silently, and a customer billed
// at a rate nobody typed is worse than a refusal. Projects' validatePositiveAmount
// rule, mirrored in this module's wording ("…, but was …") since depguard
// forbids sharing it; the number is quoted as its shortest decimal text, the
// digits the caller sent. Keyed like validatePaymentTermsDays, into the same
// errs map, so it is reported beside every other field's problem.
func validateDefaultBillRate(raw *float64, errs map[string][]string) *float64 {
	if raw == nil {
		return nil
	}
	v := *raw
	text := strconv.FormatFloat(v, 'f', -1, 64)
	switch {
	case v <= 0:
		errs["defaultBillRate"] = []string{fmt.Sprintf("A default bill rate must be greater than zero, but was %s", text)}
	case v > maxBillRate:
		errs["defaultBillRate"] = []string{fmt.Sprintf("A default bill rate must be at most %.2f, but was %s", maxBillRate, text)}
	case decimalPlaces(text) > 2:
		errs["defaultBillRate"] = []string{fmt.Sprintf("A default bill rate must have at most two decimals, but was %s", text)}
	default:
		return &v
	}
	return nil
}

// decimalPlaces is how many digits follow the point in text, a number's
// shortest 'f' formatting (never an exponent) — internal/projects'
// decimalPlaces, taking the text the message quotes anyway.
func decimalPlaces(text string) int {
	point := strings.IndexByte(text, '.')
	if point < 0 {
		return 0
	}
	return len(text) - point - 1
}

// validateBillingProfile is PutCustomersByIdBillingProfile's validator
// (invoice-ready customer design D1, D4): every field validated
// independently and every error reported together, keyed by the request's
// own JSON field name — never short-circuited on the first failure, the
// same shape validateContactInfo/validateAddress give their own requests.
// Blank or whitespace-only is stored as NULL, never a validation error (the
// controller ruling for this sub-resource, same as contact info and
// addresses): normalizedOrNil applies that rule to every string field before
// its own validator ever sees a value.
func validateBillingProfile(req gen.PutCustomerBillingProfileRequest) (billingProfile, map[string][]string) {
	errs := map[string][]string{}
	profile := billingProfile{
		InvoiceEmail:     normalizedOrNil(req.InvoiceEmail, "invoiceEmail", validateEmail, errs),
		ReminderEmail:    normalizedOrNil(req.ReminderEmail, "reminderEmail", validateEmail, errs),
		Currency:         normalizedOrNil(req.Currency, "currency", validateBillingCurrency, errs),
		Language:         normalizedOrNil(req.Language, "language", validateBillingLanguage, errs),
		InvoiceDelivery:  normalizedOrNil(req.InvoiceDelivery, "invoiceDelivery", validateInvoiceDelivery, errs),
		ReminderDelivery: normalizedOrNil(req.ReminderDelivery, "reminderDelivery", validateReminderDelivery, errs),
		PeppolID:         normalizedOrNil(req.PeppolId, "peppolId", validatePeppolID, errs),
		Gln:              normalizedOrNil(req.Gln, "gln", validateGLN, errs),
		BuyerReference:   validateOptionalAddressText(req.BuyerReference, "buyerReference", "A buyer reference", 100, errs),
		PaymentTermsDays: validatePaymentTermsDays(req.PaymentTermsDays, "paymentTermsDays", errs),
		DefaultBillRate:  validateDefaultBillRate(req.DefaultBillRate, errs),
	}
	// The one cross-field rule on this profile (customers bill-rate design D1):
	// a rate is quoted in the profile's own currency, so a rate with none — the
	// currency absent or blank, which clear alike, including a full replace that
	// clears it while sending the rate — is refused on the rate. Not when the
	// currency was sent and is invalid: that field already carries its own error,
	// and a second on the rate would say the same thing twice.
	if profile.DefaultBillRate != nil && profile.Currency == nil && len(errs["currency"]) == 0 {
		errs["defaultBillRate"] = []string{"A default bill rate needs the billing profile's currency to be quoted in"}
	}
	if len(errs) > 0 {
		return billingProfile{}, errs
	}
	return profile, nil
}
