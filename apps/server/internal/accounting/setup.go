package accounting

// The meta and Set up the books (R12): every known module is on by default,
// so an upgrade switches Accounting on, and it does nothing until a person
// with accounting:close sets the books up.

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/accounting/gen"
	"github.com/vantigo-io/vantigo/server/internal/accounting/store"
)

// currency is the books' currency in this phase (§12); a purchase in another
// currency is kept beside it on its postings (R8).
const currency = "NOK"

// payablesSoonDays is how far ahead the meta counts supplier invoices due.
const payablesSoonDays = 7

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
		ExpensesEnabled: s.deps.ExpenseJournal != nil, AttachmentsEnabled: s.storageConfigured,
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
	if out.Unposted, err = q.CountUnposted(ctx); err != nil {
		return nil, err
	}
	if out.Informational, err = q.CountInformational(ctx); err != nil {
		return nil, err
	}
	due, err := q.PayablesDue(ctx, store.PayablesDueParams{Today: today, Soon: today.AddDate(0, 0, payablesSoonDays)})
	if err != nil {
		return nil, err
	}
	out.PayablesOverdue, out.PayablesDueSoon = due.Overdue, due.DueSoon
	if err := s.vatMeta(ctx, q, settings, today, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// vatMeta fills the meta's VAT terms (A22): the next term due and the terms
// that wait to be closed or filed, and whether the books' VAT term disagrees
// with the seller's registration in Invoices (R10), read with no
// transaction open.
func (s *server) vatMeta(ctx context.Context, q *store.Queries, settings store.AccountingSetting, today time.Time,
	out *gen.GetAccountingMeta200JSONResponse) error {
	if s.deps.SalesTax != nil {
		seller, err := s.deps.SalesTax.Seller(ctx)
		if err != nil {
			return fmt.Errorf("accounting: read the seller: %w", err)
		}
		out.VatRegistrationMismatch = seller.VATRegistered != (settings.VatTerm != "none")
	}
	terms := termsThrough(settings.VatTerm, settings.BooksStart, today)
	if len(terms) == 0 {
		return nil
	}
	// States and dates only (L6): the figures are the VAT pages' to compute.
	rows, err := termRows(ctx, q, settings.VatTerm)
	if err != nil {
		return err
	}
	next := -1
	for i, t := range terms {
		r, ok := rows[[2]int32{t.Year, t.Number}]
		item := termItem(t, listState(r, ok, nil), settings, today, nil, nil)
		if ok && r.State == "closed" {
			item.State = gen.AccountingVatTermStateClosed
			item.NeedsClosing = false
		}
		if item.NeedsClosing {
			out.VatTermsToClose++
		}
		if item.NeedsFiling {
			out.VatTermsToFile++
		}
		if next < 0 && item.FiledAt == nil && t.End.Before(today) {
			next = i
		}
	}
	if next < 0 {
		next = len(terms) - 1
	}
	t := terms[next]
	r, ok := rows[[2]int32{t.Year, t.Number}]
	var closing *store.AccountingVatTermClosing
	var total *string
	if ok && r.CurrentClosingID != nil {
		c, err := q.GetVatTermClosing(ctx, *r.CurrentClosingID)
		if err != nil {
			return err
		}
		closing, total = &c, &r.ClosedTotal
	}
	var ids []int64
	if closing != nil && closing.SettlementVoucherID != nil {
		ids = append(ids, *closing.SettlementVoucherID)
	}
	refs, err := voucherRefs(ctx, q, ids)
	if err != nil {
		return err
	}
	item := termItem(t, listState(r, ok, closing), settings, today, total, refs)
	out.VatNextTerm = &item
	return nil
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
	switch {
	case start.Before(firstRateDay):
		f.addCoded("booksStart", codeBooksStartTooEarly,
			"The books start on "+firstRateDay.Format(time.DateOnly)+" or later: the tax codes' rates are known from then.")
	case start.Year() > 2100:
		f.add("booksStart", "A day before 2101.")
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
		if err := seedBooks(ctx, q, string(b.Language), start, now); err != nil {
			return err
		}
		return seedDefaultRules(ctx, q, string(b.Language), now)
	})
	if err != nil {
		return nil, err
	}
	s.logger().InfoContext(ctx, "accounting: the books were set up", "books_start", start.Format(time.DateOnly),
		"language", b.Language, "status", http.StatusCreated)
	return gen.SetUpAccountingBooks201JSONResponse(settingsView(settings)), nil
}
