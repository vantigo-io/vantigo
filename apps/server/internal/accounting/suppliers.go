package accounting

// Suppliers (A17): this module's own party, numbered gap-free, looked up in
// Brønnøysundregisteret through the platform's internal/brreg, and never
// deleted once the books name it. A supplier who is a person (a sole
// proprietor) is personal data: Customers' personal-data slot
// (contracts.CustomerPersonalData) is keyed by a customer of Customers and
// cannot name a supplier, so the module answers a person's access request
// itself — GET /suppliers/{id}/export — and keeps the supplier, as
// bookkeeping, for five years after the end of the fiscal year of its last
// voucher (bokføringsloven § 13), which the supplier's retainedUntil says.

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vantigo-io/vantigo/server/internal/accounting/gen"
	"github.com/vantigo-io/vantigo/server/internal/accounting/store"
	"github.com/vantigo-io/vantigo/server/internal/brreg"
)

const (
	defaultSupplierLimit = 100
	maxSupplierLimit     = 500
	// supplierCounter is the counter row suppliers' numbers come from.
	supplierCounter = "supplier"
	// retentionYears is how long the bookkeeping keeps a voucher's material
	// after the end of its fiscal year (bokføringsloven § 13).
	retentionYears = 5
	// brregMaxResults is the most a name search answers.
	brregMaxResults = 10
)

var (
	currencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)
	countryPattern  = regexp.MustCompile(`^[A-Z]{2}$`)
	digitsPattern   = regexp.MustCompile(`^[0-9]+$`)
)

// mod11 is the Norwegian mod-11 check over len(weights)+1 digits whose last
// is the check digit, as Invoices checks the seller's account and
// organisation number.
func mod11(digits string, weights []int) bool {
	if len(digits) != len(weights)+1 || !digitsPattern.MatchString(digits) {
		return false
	}
	sum := 0
	for i, w := range weights {
		sum += int(digits[i]-'0') * w
	}
	check := 11 - sum%11
	if check == 11 {
		check = 0
	}
	return check != 10 && check == int(digits[len(weights)]-'0')
}

// validBankAccount is the kontonummer check: eleven digits, mod 11.
func validBankAccount(digits string) bool {
	return mod11(digits, []int{5, 4, 3, 2, 7, 6, 5, 4, 3, 2})
}

// validOrganisationNumber is the organisasjonsnummer check: nine digits,
// mod 11.
func validOrganisationNumber(digits string) bool {
	return mod11(digits, []int{3, 2, 7, 6, 5, 4, 3, 2})
}

// compact is s with its spaces and dots taken out, as an account number or
// an organisation number is often written.
func compact(s string) string {
	return strings.NewReplacer(" ", "", ".", "", " ", "").Replace(strings.TrimSpace(s))
}

// optional is a trimmed text, nil when empty, held to max runes.
func optional(p *string, field string, max int, f *fieldErrors) *string {
	if p == nil {
		return nil
	}
	v := strings.TrimSpace(*p)
	if v == "" {
		return nil
	}
	if len([]rune(v)) > max {
		f.add(field, "At most "+itoa(max)+" characters.")
	}
	return &v
}

// supplierFields is a supplier's input checked and normalised.
type supplierFields struct {
	name, country, currency            string
	organisationNumber, addressLine    *string
	postalCode, city, email            *string
	bankAccount, iban, bic             *string
	paymentTermsDays                   int16
	defaultAccountID, defaultTaxCodeID *int32
	isPerson, active                   bool
}

// checkSupplier holds a supplier's input to its rules: a name, a country and
// a currency of the right shape, an organisation number of nine digits
// passing mod 11 for a Norwegian entity, a bank account of eleven passing
// mod 11, an e-mail with an @, terms of 0 to 365 days, and a default account
// and tax code that exist and are active — the tax code a purchase's.
func checkSupplier(ctx context.Context, q *store.Queries, b gen.AccountingSupplierInput) (supplierFields, error) {
	const title = "The supplier cannot be saved"
	var f fieldErrors
	out := supplierFields{name: strings.TrimSpace(b.Name), country: "NO", currency: currency, paymentTermsDays: 14,
		isPerson: b.IsPerson != nil && *b.IsPerson, active: b.Active == nil || *b.Active}
	if out.name == "" || len([]rune(out.name)) > 200 {
		f.add("name", "A name of 1 to 200 characters.")
	}
	if b.Country != nil && strings.TrimSpace(*b.Country) != "" {
		out.country = strings.ToUpper(strings.TrimSpace(*b.Country))
		if !countryPattern.MatchString(out.country) {
			f.add("country", "A country code of two letters (ISO 3166).")
		}
	}
	if b.Currency != nil && strings.TrimSpace(*b.Currency) != "" {
		out.currency = strings.ToUpper(strings.TrimSpace(*b.Currency))
		if !currencyPattern.MatchString(out.currency) {
			f.add("currency", "A currency code of three letters (ISO 4217).")
		}
	}
	if b.OrganisationNumber != nil && compact(*b.OrganisationNumber) != "" {
		v := compact(*b.OrganisationNumber)
		if out.country == "NO" && !validOrganisationNumber(v) {
			f.add("organisationNumber", "A Norwegian organisation number is nine digits passing mod 11.")
		} else if len(v) > 20 {
			f.add("organisationNumber", "At most 20 characters.")
		}
		out.organisationNumber = &v
	}
	out.addressLine = optional(b.AddressLine, "addressLine", 300, &f)
	out.postalCode = optional(b.PostalCode, "postalCode", 20, &f)
	out.city = optional(b.City, "city", 100, &f)
	if out.email = optional(b.Email, "email", 254, &f); out.email != nil && !strings.Contains(*out.email, "@") {
		f.add("email", "An e-mail address.")
	}
	if b.BankAccount != nil && compact(*b.BankAccount) != "" {
		v := compact(*b.BankAccount)
		if !validBankAccount(v) {
			f.addCoded("bankAccount", codeSupplierBankAccount, "A Norwegian account number is eleven digits passing mod 11.")
		}
		out.bankAccount = &v
	}
	if b.Iban != nil && compact(*b.Iban) != "" {
		v := strings.ToUpper(compact(*b.Iban))
		if len(v) < 15 || len(v) > 34 {
			f.add("iban", "An IBAN of 15 to 34 characters.")
		}
		out.iban = &v
	}
	if b.Bic != nil && compact(*b.Bic) != "" {
		v := strings.ToUpper(compact(*b.Bic))
		if len(v) != 8 && len(v) != 11 {
			f.add("bic", "A BIC of 8 or 11 characters.")
		}
		out.bic = &v
	}
	if b.PaymentTermsDays != nil {
		if *b.PaymentTermsDays < 0 || *b.PaymentTermsDays > 365 {
			f.add("paymentTermsDays", "0 to 365 days.")
		}
		out.paymentTermsDays = int16(min(max(*b.PaymentTermsDays, 0), 365))
	}
	if b.DefaultAccountId != nil {
		accounts, err := q.AccountsForPosting(ctx, []int32{*b.DefaultAccountId})
		if err != nil {
			return out, err
		}
		if len(accounts) != 1 {
			f.addCoded("defaultAccountId", codeAccountUnknown, "No such account.")
		} else if !accounts[0].Active {
			f.addCoded("defaultAccountId", codeAccountInactive, "Account "+accounts[0].Number+" is inactive.")
		}
		out.defaultAccountID = b.DefaultAccountId
	}
	if b.DefaultTaxCodeId != nil {
		codes, err := q.TaxCodesForPosting(ctx, store.TaxCodesForPostingParams{Ids: []int32{*b.DefaultTaxCodeId}, Day: time.Now()})
		if err != nil {
			return out, err
		}
		switch {
		case len(codes) != 1 || !codes[0].Active:
			f.addCoded("defaultTaxCodeId", codeTaxCodeUnknown, "No such active tax code.")
		case codes[0].Direction == "output":
			f.addCoded("defaultTaxCodeId", codeTaxCodeNotPurchase, "An output code is a sale's, not a purchase's.")
		}
		out.defaultTaxCodeID = b.DefaultTaxCodeId
	}
	return out, f.refusal(title)
}

// supplierView is a supplier as the contract answers it.
func supplierView(r store.GetSupplierRow, startMonth int32) gen.AccountingSupplier {
	out := gen.AccountingSupplier{Id: r.ID, Number: r.Number, Name: r.Name, OrganisationNumber: r.OrganisationNumber,
		Country: r.Country, AddressLine: r.AddressLine, PostalCode: r.PostalCode, City: r.City, Email: r.Email,
		BankAccount: r.BankAccount, Iban: r.Iban, Bic: r.Bic, PaymentTermsDays: int32(r.PaymentTermsDays),
		DefaultAccountId: r.DefaultAccountID, DefaultTaxCodeId: r.DefaultTaxCodeID, Currency: r.Currency,
		IsPerson: r.IsPerson, Active: r.Active, HasPostings: r.HasPostings, Balance: money(mustRat(r.Balance)),
		Revision: r.Revision, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
	if last, err := time.Parse(time.DateOnly, r.LastVoucherDate); err == nil {
		out.LastVoucherDate = ptr(apiDate(last))
		if r.IsPerson {
			out.RetainedUntil = ptr(apiDate(retainedUntil(last, startMonth)))
		}
	}
	return out
}

// retainedUntil is the last day the bookkeeping keeps a voucher dated last:
// five years after the end of its fiscal year.
func retainedUntil(last time.Time, startMonth int32) time.Time {
	end := fiscalYearStart(last, startMonth).AddDate(1, 0, -1)
	return end.AddDate(retentionYears, 0, 0)
}

// supplier is one supplier as the contract answers it; 404 when none.
func (s *server) supplier(ctx context.Context, q *store.Queries, id int64) (gen.AccountingSupplier, error) {
	row, err := q.GetSupplier(ctx, id)
	if noRows(err) {
		return gen.AccountingSupplier{}, notFound()
	}
	if err != nil {
		return gen.AccountingSupplier{}, err
	}
	settings, err := q.GetSettings(ctx)
	if err != nil {
		return gen.AccountingSupplier{}, err
	}
	return supplierView(row, int32(settings.FiscalYearStartMonth)), nil
}

// supplierConflict is the 409 of a unique index a supplier's write met.
func supplierConflict(err error) error {
	if violates(err, "ux_suppliers_organisation_number") {
		return conflict(codeSupplierOrgNumberTaken, "Another supplier has that organisation number", "")
	}
	return nil
}

// ListAccountingSuppliers is a page of the suppliers by number.
func (s *server) ListAccountingSuppliers(ctx context.Context, req gen.ListAccountingSuppliersRequestObject) (gen.ListAccountingSuppliersResponseObject, error) {
	p := req.Params
	limit, offset := int32(defaultSupplierLimit), int32(0)
	if p.Limit != nil {
		limit = min(max(*p.Limit, 1), maxSupplierLimit)
	}
	if p.Offset != nil && *p.Offset > 0 {
		offset = *p.Offset
	}
	params := store.ListSuppliersParams{Active: p.Active, RowLimit: limit, RowOffset: offset}
	if p.Q != nil && strings.TrimSpace(*p.Q) != "" {
		params.Q = ptr(strings.TrimSpace(*p.Q))
	}
	q := store.New(s.deps.Pool)
	settings, err := q.GetSettings(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := q.ListSuppliers(ctx, params)
	if err != nil {
		return nil, err
	}
	out := gen.ListAccountingSuppliers200JSONResponse{Items: []gen.AccountingSupplier{}}
	for _, r := range rows {
		out.Total = int32(r.Total)
		out.Items = append(out.Items, supplierView(store.GetSupplierRow{ID: r.ID, Number: r.Number, Name: r.Name,
			OrganisationNumber: r.OrganisationNumber, Country: r.Country, AddressLine: r.AddressLine, PostalCode: r.PostalCode,
			City: r.City, Email: r.Email, BankAccount: r.BankAccount, Iban: r.Iban, Bic: r.Bic,
			PaymentTermsDays: r.PaymentTermsDays, DefaultAccountID: r.DefaultAccountID, DefaultTaxCodeID: r.DefaultTaxCodeID,
			Currency: r.Currency, IsPerson: r.IsPerson, Active: r.Active, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
			Revision: r.Revision, HasPostings: r.HasPostings, Balance: r.Balance, LastVoucherDate: r.LastVoucherDate},
			int32(settings.FiscalYearStartMonth)))
	}
	return out, nil
}

// GetAccountingSupplier is one supplier.
func (s *server) GetAccountingSupplier(ctx context.Context, req gen.GetAccountingSupplierRequestObject) (gen.GetAccountingSupplierResponseObject, error) {
	out, err := s.supplier(ctx, store.New(s.deps.Pool), req.Id)
	if err != nil {
		return nil, err
	}
	return gen.GetAccountingSupplier200JSONResponse(out), nil
}

// CreateAccountingSupplier adds a supplier with the next number.
func (s *server) CreateAccountingSupplier(ctx context.Context, req gen.CreateAccountingSupplierRequestObject) (gen.CreateAccountingSupplierResponseObject, error) {
	if req.Body == nil {
		return nil, invalidField("The supplier cannot be saved", "body", "", "A body is required.")
	}
	var id int64
	err := s.withTx(ctx, func(_ pgx.Tx, q *store.Queries) error {
		f, err := checkSupplier(ctx, q, *req.Body)
		if err != nil {
			return err
		}
		id, err = insertSupplier(ctx, q, f, s.now())
		return err
	})
	if c := supplierConflict(err); c != nil {
		return nil, c
	}
	if err != nil {
		return nil, err
	}
	out, err := s.supplier(ctx, store.New(s.deps.Pool), id)
	if err != nil {
		return nil, err
	}
	return gen.CreateAccountingSupplier201JSONResponse(out), nil
}

// insertSupplier writes a new supplier with the next number of the series,
// on the caller's transaction: a failed write gives the number back.
func insertSupplier(ctx context.Context, q *store.Queries, f supplierFields, now time.Time) (int64, error) {
	number, err := q.AllocateVoucherNumber(ctx, supplierCounter)
	if err != nil {
		return 0, err
	}
	return q.InsertSupplier(ctx, store.InsertSupplierParams{Number: number, Name: f.name,
		OrganisationNumber: f.organisationNumber, Country: f.country, AddressLine: f.addressLine, PostalCode: f.postalCode,
		City: f.city, Email: f.email, BankAccount: f.bankAccount, Iban: f.iban, Bic: f.bic,
		PaymentTermsDays: f.paymentTermsDays, DefaultAccountID: f.defaultAccountID, DefaultTaxCodeID: f.defaultTaxCodeID,
		Currency: f.currency, IsPerson: f.isPerson, Now: now})
}

// UpdateAccountingSupplier changes a supplier under its revision.
func (s *server) UpdateAccountingSupplier(ctx context.Context, req gen.UpdateAccountingSupplierRequestObject) (gen.UpdateAccountingSupplierResponseObject, error) {
	const title = "The supplier cannot be saved"
	if req.Body == nil {
		return nil, invalidField(title, "body", "", "A body is required.")
	}
	if req.Body.Revision == nil {
		return nil, invalidField(title, "revision", "", "The revision read is required.")
	}
	err := s.withTx(ctx, func(_ pgx.Tx, q *store.Queries) error {
		if _, err := q.GetSupplier(ctx, req.Id); noRows(err) {
			return notFound()
		} else if err != nil {
			return err
		}
		f, err := checkSupplier(ctx, q, *req.Body)
		if err != nil {
			return err
		}
		_, err = q.UpdateSupplier(ctx, store.UpdateSupplierParams{ID: req.Id, Revision: *req.Body.Revision, Name: f.name,
			OrganisationNumber: f.organisationNumber, Country: f.country, AddressLine: f.addressLine,
			PostalCode: f.postalCode, City: f.city, Email: f.email, BankAccount: f.bankAccount, Iban: f.iban, Bic: f.bic,
			PaymentTermsDays: f.paymentTermsDays, DefaultAccountID: f.defaultAccountID,
			DefaultTaxCodeID: f.defaultTaxCodeID, Currency: f.currency, IsPerson: f.isPerson, Active: f.active, Now: s.now()})
		if noRows(err) {
			return conflict(codeRevisionConflict, "The supplier has changed", "Read it again and repeat the change.")
		}
		return err
	})
	if c := supplierConflict(err); c != nil {
		return nil, c
	}
	if err != nil {
		return nil, err
	}
	out, err := s.supplier(ctx, store.New(s.deps.Pool), req.Id)
	if err != nil {
		return nil, err
	}
	return gen.UpdateAccountingSupplier200JSONResponse(out), nil
}

// DeleteAccountingSupplier deletes a supplier the books do not name: no
// posting, no open item, no invoice. Any other is made inactive instead.
func (s *server) DeleteAccountingSupplier(ctx context.Context, req gen.DeleteAccountingSupplierRequestObject) (gen.DeleteAccountingSupplierResponseObject, error) {
	err := s.withTx(ctx, func(_ pgx.Tx, q *store.Queries) error {
		if _, err := q.GetSupplier(ctx, req.Id); noRows(err) {
			return notFound()
		} else if err != nil {
			return err
		}
		used, err := q.SupplierHasHistory(ctx, req.Id)
		if err != nil {
			return err
		}
		if used {
			return conflict(codeSupplierHasPostings, "The supplier is in the books",
				"A supplier with postings or invoices is kept; make it inactive instead.")
		}
		_, err = q.DeleteSupplier(ctx, req.Id)
		return err
	})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" {
		return nil, conflict(codeSupplierHasPostings, "The supplier is in the books", "")
	}
	if err != nil {
		return nil, err
	}
	return gen.DeleteAccountingSupplier204Response{}, nil
}

// ExportAccountingSupplier is everything the books hold about one supplier:
// the record, its invoices with their lines and payments, and every posting
// naming it — what a person's access request is answered with (A17).
func (s *server) ExportAccountingSupplier(ctx context.Context, req gen.ExportAccountingSupplierRequestObject) (gen.ExportAccountingSupplierResponseObject, error) {
	q := store.New(s.deps.Pool)
	sup, err := s.supplier(ctx, q, req.Id)
	if err != nil {
		return nil, err
	}
	ids, err := q.ListSupplierInvoiceIDs(ctx, store.ListSupplierInvoiceIDsParams{SupplierID: &req.Id, RowLimit: 100000,
		Today: s.now()})
	if err != nil {
		return nil, err
	}
	invoiceIDs := make([]int64, 0, len(ids))
	for _, r := range ids {
		invoiceIDs = append(invoiceIDs, r.ID)
	}
	invoices, err := s.supplierInvoices(ctx, invoiceIDs)
	if err != nil {
		return nil, err
	}
	rows, err := q.SupplierPostings(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	out := gen.ExportAccountingSupplier200JSONResponse{Supplier: sup, Invoices: invoices,
		Postings: []gen.AccountingSupplierExportPosting{}, ProducedAt: s.now()}
	for _, r := range rows {
		out.Postings = append(out.Postings, gen.AccountingSupplierExportPosting{
			Voucher:     gen.AccountingVoucherRef{Id: r.VoucherID, Display: display(r.FiscalYear, r.Number)},
			VoucherDate: apiDate(r.VoucherDate), AccountNumber: r.AccountNumber, Amount: money(mustRat(r.Amount)),
			Description: r.Description})
	}
	s.logger().InfoContext(ctx, "accounting: a supplier's data was exported", "supplier_id", req.Id)
	return out, nil
}

// LookupAccountingSupplierBrreg looks a supplier up in Brønnøysundregisteret:
// by organisation number the entity itself, with its business address (its
// postal address when it has none); by name a search of at most ten.
func (s *server) LookupAccountingSupplierBrreg(ctx context.Context, req gen.LookupAccountingSupplierBrregRequestObject) (gen.LookupAccountingSupplierBrregResponseObject, error) {
	const title = "The lookup needs an organisation number or a name"
	orgnr := compact(deref(req.Params.OrganisationNumber))
	name := strings.TrimSpace(deref(req.Params.Q))
	switch {
	case orgnr != "" && (len(orgnr) != 9 || !digitsPattern.MatchString(orgnr)):
		return nil, invalidField(title, "organisationNumber", "", "Nine digits.")
	case orgnr == "" && len([]rune(name)) < 2:
		return nil, invalidField(title, "q", "", "A name of at least two characters, or an organisation number.")
	}
	unavailable := func(err error) error {
		s.logger().WarnContext(ctx, "accounting: the Brønnøysund lookup failed", "error", err.Error())
		return refuse(http.StatusBadGateway, codeBrregUnavailable, "Brønnøysundregisteret could not be reached",
			"Try again in a moment, or fill the supplier in by hand.")
	}
	out := gen.LookupAccountingSupplierBrreg200JSONResponse{Items: []gen.AccountingBrregEntity{}}
	if orgnr != "" {
		entity, outcome, err := s.brreg.Entity(ctx, orgnr)
		if err != nil {
			return nil, unavailable(err)
		}
		if outcome == brreg.Found || outcome == brreg.Deleted {
			out.Items = append(out.Items, brregView(entity, outcome == brreg.Deleted))
		}
		return out, nil
	}
	matches, err := s.brreg.Search(ctx, brreg.SearchQuery{Name: name, Size: brregMaxResults})
	if err != nil {
		if errors.Is(err, brreg.ErrUnavailable) {
			return nil, unavailable(err)
		}
		return nil, err
	}
	for _, m := range matches {
		out.Items = append(out.Items, gen.AccountingBrregEntity{OrganisationNumber: m.OrganisationNumber, Name: m.Name})
	}
	return out, nil
}

// brregView is an entity as the lookup answers it.
func brregView(e brreg.Entity, deleted bool) gen.AccountingBrregEntity {
	out := gen.AccountingBrregEntity{OrganisationNumber: e.OrganisationNumber, Name: e.Name, Deleted: deleted}
	if !deleted {
		out.VatRegistered = ptr(e.VATRegistered)
	}
	addr := e.BusinessAddress
	if addr == nil {
		addr = e.PostalAddress
	}
	if addr != nil {
		if line := strings.Join(addr.Lines, ", "); line != "" {
			out.AddressLine = &line
		}
		if addr.PostalCode != "" {
			out.PostalCode = &addr.PostalCode
		}
		if addr.City != "" {
			out.City = &addr.City
		}
		if addr.CountryCode != "" {
			out.Country = &addr.CountryCode
		}
	}
	return out
}
