package accounting

// The meta and Set up the books (R12): every known module is on by default,
// so an upgrade switches Accounting on, and it does nothing until a person
// with accounting:close sets the books up.

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/accounting/gen"
	"github.com/vantigo-io/vantigo/server/internal/accounting/store"
)

// currency is the books' currency in this phase (§12).
const currency = "NOK"

// GetAccountingMeta is what the caller may do, and whether the books are set up.
func (s *server) GetAccountingMeta(ctx context.Context, _ gen.GetAccountingMetaRequestObject) (gen.GetAccountingMetaResponseObject, error) {
	today, err := s.today(ctx)
	if err != nil {
		return nil, err
	}
	out := gen.GetAccountingMeta200JSONResponse{
		CanPost: s.has(ctx, permPost), CanClose: s.has(ctx, permClose), CanExport: s.has(ctx, permExport),
		Currency: currency, Today: apiDate(today),
		ProjectsEnabled: s.deps.Projects != nil, InvoicesEnabled: s.deps.SalesJournal != nil,
	}
	q := store.New(s.deps.Pool)
	settings, err := q.GetSettings(ctx)
	if noRows(err) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	out.SetUp = true
	out.Settings = ptr(settingsView(settings))
	if out.VouchersThisYear, err = q.CountVouchersInYear(ctx, fiscalYear(today, int32(settings.FiscalYearStartMonth))); err != nil {
		return nil, err
	}
	if out.OpenCustomerItems, err = q.CountOpenCustomerItems(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

// settingsView is the settings row as the contract answers it.
func settingsView(st store.AccountingSetting) gen.AccountingSettings {
	return gen.AccountingSettings{
		BooksStart: apiDate(st.BooksStart), FiscalYearStartMonth: int32(st.FiscalYearStartMonth),
		VatTerm: gen.AccountingVatTerm(st.VatTerm), LockedThrough: apiDatePtr(st.LockedThrough),
		ChartLanguage: gen.AccountingLanguage(st.ChartLanguage), SetUpAt: st.SetUpAt,
	}
}

// SetUpAccountingBooks writes the settings and seeds the chart, the tax codes
// and the roles, once (R12).
func (s *server) SetUpAccountingBooks(ctx context.Context, req gen.SetUpAccountingBooksRequestObject) (gen.SetUpAccountingBooksResponseObject, error) {
	if req.Body == nil {
		return nil, invalidField("Set up the books", "body", "", "A body is required.")
	}
	b := *req.Body
	month := int32(1)
	if b.FiscalYearStartMonth != nil {
		month = *b.FiscalYearStartMonth
	}
	var f fieldErrors
	if month < 1 || month > 12 {
		f.add("fiscalYearStartMonth", "A month, 1 to 12.")
	}
	switch b.VatTerm {
	case gen.AccountingVatTermBimonthly, gen.AccountingVatTermYearly, gen.AccountingVatTermNone:
	default:
		f.add("vatTerm", "bimonthly, yearly or none.")
	}
	switch b.Language {
	case gen.En, gen.Nb:
	default:
		f.add("language", "en or nb.")
	}
	start := day(b.BooksStart)
	if start.Year() < 2000 || start.Year() > 2100 {
		f.add("booksStart", "A day between 2000 and 2100.")
	}
	if err := f.refusal("The books cannot be set up"); err != nil {
		return nil, err
	}
	now := s.now()
	var settings store.AccountingSetting
	err := s.withTx(ctx, func(_ pgx.Tx, q *store.Queries) error {
		var err error
		settings, err = q.InsertSettings(ctx, store.InsertSettingsParams{BooksStart: start, FiscalYearStartMonth: int16(month),
			VatTerm: string(b.VatTerm), ChartLanguage: string(b.Language), Now: now, UserID: callerID(ctx)})
		if noRows(err) {
			return conflict(codeBooksAlreadySetUp, "The books are already set up", "")
		}
		if err != nil {
			return err
		}
		return seedBooks(ctx, q, string(b.Language), start, now)
	})
	if err != nil {
		return nil, err
	}
	s.logger().InfoContext(ctx, "accounting: the books were set up", "books_start", start.Format(time.DateOnly),
		"language", b.Language, "status", http.StatusCreated)
	return gen.SetUpAccountingBooks201JSONResponse(settingsView(settings)), nil
}
