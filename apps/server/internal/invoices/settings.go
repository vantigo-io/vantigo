package invoices

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is the seller record and the series start (D2): one settings row,
// read by everyone with invoices:access and replaced by invoices:manage. Every
// field but the series start stays editable after the first issue, because an
// issued document keeps the seller snapshot it was issued with.

// The codes and titles of this file's 409s.
const (
	codeSeriesLocked  = "series_locked"
	seriesLockedTitle = "The number series has started"
)

// onlyNOK is the one sentence a currency other than NOK is refused with in
// phase 1 (D5): § 5-1-1 nr. 6 wants VAT in NOK at the invoice date's rate,
// which this phase does not model yet.
const onlyNOK = "Only NOK in this phase"

var (
	countryPattern = regexp.MustCompile(`^[A-Z]{2}$`)
	bicPattern     = regexp.MustCompile(`^[A-Z]{4}[A-Z]{2}[A-Z0-9]{2}([A-Z0-9]{3})?$`)
)

// maxLength is the rule for a varchar(n) column: n characters, as Postgres
// counts them.
func maxLength(label string, value string, n int) string {
	if utf8.RuneCountInString(value) > n {
		return fmt.Sprintf("%s holds at most %d characters", label, n)
	}
	return ""
}

// validOrganisationNumber is the Brreg organisasjonsnummer mod-11 check, the
// customers module's validator (values.go validNorwegianOrgNumber) copied here
// because no module imports another: nine digits, the ninth the check digit
// over the first eight with weights 3 2 7 6 5 4 3 2, and a check value of 10
// invalid outright.
func validOrganisationNumber(digits string) bool {
	return mod11(digits, []int{3, 2, 7, 6, 5, 4, 3, 2})
}

// validBankAccount is the Norwegian kontonummer mod-11 check: eleven digits,
// the eleventh the check digit over the first ten with weights 5 4 3 2 7 6 5 4
// 3 2.
func validBankAccount(digits string) bool {
	return mod11(digits, []int{5, 4, 3, 2, 7, 6, 5, 4, 3, 2})
}

// mod11 is the shared rule: len(weights)+1 ASCII digits whose last is 11 less
// the weighted sum mod 11 (0 when that is 11), a check value of 10 invalid.
func mod11(digits string, weights []int) bool {
	if len(digits) != len(weights)+1 {
		return false
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return false
		}
	}
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
	return int(digits[len(weights)]-'0') == check
}

// validIBAN is ISO 13616's check: 15-34 characters, two letters and two
// digits first, and the whole number, rearranged, is 1 mod 97.
func validIBAN(iban string) bool {
	if len(iban) < 15 || len(iban) > 34 {
		return false
	}
	for i, r := range iban {
		switch {
		case i < 2 && (r < 'A' || r > 'Z'):
			return false
		case i >= 2 && i < 4 && (r < '0' || r > '9'):
			return false
		case (r < 'A' || r > 'Z') && (r < '0' || r > '9'):
			return false
		}
	}
	rearranged := iban[4:] + iban[:4]
	remainder := 0
	for _, r := range rearranged {
		value := int(r - '0')
		if r >= 'A' && r <= 'Z' {
			value = int(r-'A') + 10
		}
		if value >= 10 {
			remainder = (remainder*100 + value) % 97
		} else {
			remainder = (remainder*10 + value) % 97
		}
	}
	return remainder == 1
}

// withoutSeparators drops the spaces and dots people write account numbers
// with ("8601 11 17947", "8601.11.17947").
func withoutSeparators(s string) string {
	return strings.NewReplacer(" ", "", ".", "").Replace(s)
}

// parsedSettings is one validated settings body, in the shape the update wants.
type parsedSettings = store.UpdateSettingsParams

// parseSettings runs D2's rules over a settings body, every failure collected.
// An empty text field is "not set" and passes: completeness is issuing's
// question (sellerMissingFields), not saving's.
func parseSettings(body gen.InvoicesSettingsRequest) (parsedSettings, map[string][]string) {
	var errs map[string][]string
	add := func(field, msg string) {
		if msg != "" {
			errs = withFieldError(errs, field, msg)
		}
	}
	optional := func(v *string) string {
		if v == nil {
			return ""
		}
		return strings.TrimSpace(*v)
	}

	p := parsedSettings{
		LegalName:               strings.TrimSpace(body.LegalName),
		OrganisationNumber:      withoutSeparators(strings.TrimSpace(body.OrganisationNumber)),
		VatRegistered:           body.VatRegistered,
		InForetaksregisteret:    body.InForetaksregisteret,
		AddressLine1:            strings.TrimSpace(body.AddressLine1),
		AddressLine2:            optional(body.AddressLine2),
		PostalCode:              strings.TrimSpace(body.PostalCode),
		City:                    strings.TrimSpace(body.City),
		Country:                 strings.ToUpper(strings.TrimSpace(body.Country)),
		BankAccount:             withoutSeparators(strings.TrimSpace(body.BankAccount)),
		Iban:                    strings.ToUpper(strings.ReplaceAll(optional(body.Iban), " ", "")),
		Bic:                     strings.ToUpper(optional(body.Bic)),
		Email:                   optional(body.Email),
		DefaultPaymentTermsDays: body.DefaultPaymentTermsDays,
		DefaultCurrency:         strings.ToUpper(strings.TrimSpace(body.DefaultCurrency)),
		FooterText:              optional(body.FooterText),
		SeriesStart:             body.SeriesStart,
	}

	add("legalName", maxLength("A legal name", p.LegalName, 200))
	if p.OrganisationNumber != "" && !validOrganisationNumber(p.OrganisationNumber) {
		add("organisationNumber", "An organisation number is nine digits with a valid check digit")
	}
	add("addressLine1", maxLength("An address line", p.AddressLine1, 200))
	add("addressLine2", maxLength("An address line", p.AddressLine2, 200))
	add("postalCode", maxLength("A postal code", p.PostalCode, 20))
	add("city", maxLength("A city", p.City, 100))
	if !countryPattern.MatchString(p.Country) {
		add("country", "A country is a two-letter ISO 3166-1 code, such as NO")
	}
	if p.BankAccount != "" && !validBankAccount(p.BankAccount) {
		add("bankAccount", "A bank account number is eleven digits with a valid check digit")
	}
	if p.Iban != "" && !validIBAN(p.Iban) {
		add("iban", "This is not a valid IBAN")
	}
	if p.Bic != "" && !bicPattern.MatchString(p.Bic) {
		add("bic", "A BIC is 8 or 11 letters and digits")
	}
	if p.Email != "" {
		if a, err := mail.ParseAddress(p.Email); err != nil || a.Address != p.Email || len(p.Email) > 254 {
			add("email", "This is not an e-mail address")
		}
	}
	if p.DefaultPaymentTermsDays < 0 || p.DefaultPaymentTermsDays > 365 {
		add("defaultPaymentTermsDays", "Payment terms are between 0 and 365 days")
	}
	if p.DefaultCurrency != "NOK" {
		add("defaultCurrency", onlyNOK)
	}
	add("footerText", maxLength("The footer text", p.FooterText, 500))
	if p.SeriesStart < 1 {
		add("seriesStart", "The series starts at 1 or later")
	}
	return p, errs
}

// settingsResponse renders the settings row for the wire.
func settingsResponse(row store.InvoicesSetting, locked bool) gen.InvoicesSettingsResponse {
	return gen.InvoicesSettingsResponse{
		LegalName: row.LegalName, OrganisationNumber: row.OrganisationNumber,
		VatRegistered: row.VatRegistered, InForetaksregisteret: row.InForetaksregisteret,
		AddressLine1: row.AddressLine1, AddressLine2: row.AddressLine2,
		PostalCode: row.PostalCode, City: row.City, Country: row.Country,
		BankAccount: row.BankAccount, Iban: row.Iban, Bic: row.Bic, Email: row.Email,
		DefaultPaymentTermsDays: row.DefaultPaymentTermsDays, DefaultCurrency: row.DefaultCurrency,
		FooterText: row.FooterText, SeriesStart: row.SeriesStart, SeriesLocked: locked,
		MissingSellerFields: sellerMissingFields(row),
		Revision:            row.Revision, UpdatedAt: row.UpdatedAt,
	}
}

// GetInvoicesSettings Get the invoice settings
// (GET /api/v1/invoices/settings)
func (s *server) GetInvoicesSettings(ctx context.Context, _ gen.GetInvoicesSettingsRequestObject) (gen.GetInvoicesSettingsResponseObject, error) {
	q := store.New(s.deps.Pool)
	row, err := q.GetSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the settings: %w", err)
	}
	locked, err := anythingIssued(ctx, q)
	if err != nil {
		return nil, err
	}
	return gen.GetInvoicesSettings200JSONResponse(settingsResponse(row, locked)), nil
}

// errRefused stops a locked transaction whose rule refused the request: the
// transaction rolls back and the handler answers the refusal it recorded.
var errRefused = errors.New("invoices: refused")

// PutInvoicesSettings Change the invoice settings
// (PUT /api/v1/invoices/settings)
//
// The row is taken FOR UPDATE, so a replace waits behind an issue in flight
// (which holds it FOR SHARE) and then sees the counter row that issue made: a
// changed series start is refused from the first issue on, and the settings
// never show a start that was not used (D2).
func (s *server) PutInvoicesSettings(ctx context.Context, req gen.PutInvoicesSettingsRequestObject) (gen.PutInvoicesSettingsResponseObject, error) {
	parsed, errs := parseSettings(*req.Body)
	if len(errs) > 0 {
		return gen.PutInvoicesSettings400ApplicationProblemPlusJSONResponse(invalid(invalidSettingsTitle, errs)), nil
	}
	parsed.Now = s.deps.Clock()

	var refusal *gen.InvoicesConflictProblem
	var saved store.InvoicesSetting
	var locked bool
	err := s.withLockedTx(ctx, func(ctx context.Context, txq *store.Queries) error {
		current, err := txq.LockSettings(ctx)
		if err != nil {
			return fmt.Errorf("invoices: lock the settings: %w", err)
		}
		if current.Revision != req.Body.Revision {
			refusal = ptr(revisionConflict("Invoice settings", current.Revision, req.Body.Revision))
			return errRefused
		}
		locked, err = anythingIssued(ctx, txq)
		if err != nil {
			return err
		}
		if locked && parsed.SeriesStart != current.SeriesStart {
			refusal = ptr(conflict(codeSeriesLocked, seriesLockedTitle, fmt.Sprintf(
				"The series has started at %d and something is issued from it, so its start can no longer change.",
				current.SeriesStart)))
			return errRefused
		}
		saved, err = txq.UpdateSettings(ctx, parsed)
		if err != nil {
			return fmt.Errorf("invoices: change the settings: %w", err)
		}
		return nil
	})
	if refusal != nil {
		return gen.PutInvoicesSettings409ApplicationProblemPlusJSONResponse(*refusal), nil
	}
	if err != nil {
		return nil, err
	}
	return gen.PutInvoicesSettings200JSONResponse(settingsResponse(saved, locked)), nil
}

// ptr is a pointer to a copy of v.
func ptr[T any](v T) *T { return &v }
