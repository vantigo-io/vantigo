package accounting

// Vouchers (A4, A6, A7): the list, one voucher with its postings and links,
// the manual journal with its tax lines and its preview, and reversal.

import (
	"context"
	"fmt"
	"math/big"
	"strings"

	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/accounting/gen"
	"github.com/vantigo-io/vantigo/server/internal/accounting/store"
)

const (
	defaultVoucherLimit = 50
	maxVoucherLimit     = 200
	maxJournalLines     = 200
)

// ListAccountingVouchers is the vouchers matching the filters, newest first.
func (s *server) ListAccountingVouchers(ctx context.Context, req gen.ListAccountingVouchersRequestObject) (gen.ListAccountingVouchersResponseObject, error) {
	p := req.Params
	limit, offset := int32(defaultVoucherLimit), int32(0)
	if p.Limit != nil {
		limit = min(max(*p.Limit, 1), maxVoucherLimit)
	}
	if p.Offset != nil && *p.Offset > 0 {
		offset = *p.Offset
	}
	params := store.ListVouchersParams{FromDate: datePtr(p.From), ToDate: datePtr(p.To), AccountID: p.AccountId,
		CustomerID: p.CustomerId, SupplierID: p.SupplierId, RowLimit: limit, RowOffset: offset}
	if p.Kind != nil {
		params.Kind = ptr(string(*p.Kind))
	}
	if p.Q != nil && strings.TrimSpace(*p.Q) != "" {
		params.Q = ptr(strings.TrimSpace(*p.Q))
	}
	q := store.New(s.deps.Pool)
	rows, err := q.ListVouchers(ctx, params)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for _, r := range rows {
		ids = append(ids, r.ID)
		if r.ReversesVoucherID != nil {
			ids = append(ids, *r.ReversesVoucherID)
		}
		if r.ReversedByID != nil {
			ids = append(ids, *r.ReversedByID)
		}
	}
	refs, err := voucherRefs(ctx, q, ids)
	if err != nil {
		return nil, err
	}
	out := gen.ListAccountingVouchers200JSONResponse{Items: []gen.AccountingVoucherSummary{}}
	for _, r := range rows {
		out.Total = int32(r.Total)
		out.Items = append(out.Items, gen.AccountingVoucherSummary{Id: r.ID, FiscalYear: r.FiscalYear, Number: r.Number,
			Display: display(r.FiscalYear, r.Number), VoucherDate: apiDate(r.VoucherDate), DocumentDate: apiDate(r.DocumentDate),
			Description: r.Description, Kind: gen.AccountingVoucherKind(r.Kind), Amount: money(mustRat(r.Debit)),
			Source: sourceView(r.SourceModule, r.SourceKind, r.SourceID, r.SourcePart), System: r.SourceModule != nil,
			Reverses: refOf(refs, r.ReversesVoucherID), ReversedBy: refOf(refs, r.ReversedByID), PostedAt: r.PostedAt})
	}
	return out, nil
}

// voucherRefs is the display of each voucher in ids.
func voucherRefs(ctx context.Context, q *store.Queries, ids []int64) (map[int64]string, error) {
	out := map[int64]string{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := q.VoucherNumbers(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.ID] = display(r.FiscalYear, r.Number)
	}
	return out, nil
}

func refOf(refs map[int64]string, id *int64) *gen.AccountingVoucherRef {
	if id == nil {
		return nil
	}
	return &gen.AccountingVoucherRef{Id: *id, Display: refs[*id]}
}

func sourceView(module, kind *string, id *int64, part string) *gen.AccountingSource {
	if module == nil {
		return nil
	}
	return &gen.AccountingSource{Module: *module, Kind: deref(kind), Id: deref(id), Part: part}
}

// voucher is one voucher as the contract answers it, with its postings named:
// accounts, tax codes, customers and projects. The customers' and projects'
// names are read from their contracts after the module's own reads.
func (s *server) voucher(ctx context.Context, id int64) (gen.AccountingVoucher, error) {
	q := store.New(s.deps.Pool)
	v, err := q.GetVoucher(ctx, id)
	if noRows(err) {
		return gen.AccountingVoucher{}, notFound()
	}
	if err != nil {
		return gen.AccountingVoucher{}, err
	}
	postings, err := q.ListPostings(ctx, []int64{id})
	if err != nil {
		return gen.AccountingVoucher{}, err
	}
	attachments, err := q.ListAttachments(ctx, id)
	if err != nil {
		return gen.AccountingVoucher{}, err
	}
	ids := []int64{}
	if v.ReversesVoucherID != nil {
		ids = append(ids, *v.ReversesVoucherID)
	}
	if v.ReversedByID != nil {
		ids = append(ids, *v.ReversedByID)
	}
	refs, err := voucherRefs(ctx, q, ids)
	if err != nil {
		return gen.AccountingVoucher{}, err
	}
	var customerIDs, projectIDs []int32
	for _, p := range postings {
		if p.CustomerID != nil {
			customerIDs = append(customerIDs, *p.CustomerID)
		}
		if p.ProjectID != nil {
			projectIDs = append(projectIDs, *p.ProjectID)
		}
	}
	customers := s.customerNames(ctx, customerIDs)
	projects := s.projectNames(ctx, projectIDs)
	out := gen.AccountingVoucher{Id: v.ID, FiscalYear: v.FiscalYear, Number: v.Number, Display: display(v.FiscalYear, v.Number),
		VoucherDate: apiDate(v.VoucherDate), DocumentDate: apiDate(v.DocumentDate), Description: v.Description,
		Kind: gen.AccountingVoucherKind(v.Kind), Amount: money(mustRat(v.Debit)),
		Source: sourceView(v.SourceModule, v.SourceKind, v.SourceID, v.SourcePart), System: v.SourceModule != nil,
		Rule: v.Rule, RuleVersion: v.RuleVersion, Reverses: refOf(refs, v.ReversesVoucherID), ReversedBy: refOf(refs, v.ReversedByID),
		ReversalReason: v.ReversalReason, PostedAt: v.PostedAt, Postings: []gen.AccountingPosting{}, Attachments: []gen.AccountingAttachment{}}
	if v.PostedByUserID != nil {
		out.PostedByUserId = ptr(openapi_types.UUID(*v.PostedByUserID))
	}
	for _, p := range postings {
		g := gen.AccountingPosting{LineNo: p.LineNo, AccountId: p.AccountID, AccountNumber: p.AccountNumber, AccountName: p.AccountName,
			Amount: money(mustRat(p.Amount)), TaxCodeId: p.TaxCodeID, TaxCode: p.TaxCode, TaxBase: numText(p.TaxBase),
			CustomerId: p.CustomerID, SupplierId: p.SupplierID, OpenItemId: p.OpenItemID, ProjectId: p.ProjectID, Description: p.Description}
		if p.TaxRole != nil {
			g.TaxRole = ptr(gen.AccountingTaxRole(*p.TaxRole))
		}
		if p.CustomerID != nil {
			if name, ok := customers[*p.CustomerID]; ok {
				g.CustomerName = &name
			}
		}
		if p.ProjectID != nil {
			if name, ok := projects[*p.ProjectID]; ok {
				g.ProjectName = &name
			}
		}
		out.Postings = append(out.Postings, g)
	}
	for _, a := range attachments {
		out.Attachments = append(out.Attachments, attachmentView(a))
	}
	return out, nil
}

// customerNames is each customer's name, read from Customers; a failure there
// leaves the names out rather than failing the read.
func (s *server) customerNames(ctx context.Context, ids []int32) map[int32]string {
	out := map[int32]string{}
	if s.deps.Directory == nil || len(ids) == 0 {
		return out
	}
	entries, err := s.deps.Directory.Customers(ctx, ids)
	if err != nil {
		s.logger().WarnContext(ctx, "accounting: customers' names could not be read", "error", err.Error())
		return out
	}
	for _, e := range entries {
		out[e.ID] = e.Name
	}
	return out
}

// projectNames is each project's code and name, read from Projects.
func (s *server) projectNames(ctx context.Context, ids []int32) map[int32]string {
	out := map[int32]string{}
	if s.deps.Projects == nil || len(ids) == 0 {
		return out
	}
	entries, err := s.deps.Projects.Projects(ctx, ids)
	if err != nil {
		s.logger().WarnContext(ctx, "accounting: projects' names could not be read", "error", err.Error())
		return out
	}
	for _, e := range entries {
		out[e.ID] = strings.TrimSpace(e.Code + " " + e.Name)
	}
	return out
}

// GetAccountingVoucher is one voucher.
func (s *server) GetAccountingVoucher(ctx context.Context, req gen.GetAccountingVoucherRequestObject) (gen.GetAccountingVoucherResponseObject, error) {
	v, err := s.voucher(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	return gen.GetAccountingVoucher200JSONResponse(v), nil
}

// journalPosting is one posting of a manual journal as expanded: the line it
// comes from, and the rate a tax line was computed at.
type journalPosting struct {
	line     Line
	fromLine int
	rate     *string
}

// expandJournal turns a manual journal's lines into postings (A6): each line
// as entered, its base when it has a tax code; and per line with a tax code
// whose direction is not none and whose rate is above zero, its tax line(s),
// computed from the net or the gross with docmath's rounding. An output or
// input code posts its tax on the code's VAT account with the base's sign; a
// reverse-charge code posts both sides — the deductible side on the VAT
// account (or, without deduction, the line's own account) and the calculated
// side on the counter account — and is always entered net. It reads the
// codes on q; it checks every field it can before the engine checks the rest.
func expandJournal(ctx context.Context, q *store.Queries, in gen.AccountingJournalInput) ([]journalPosting, error) {
	var f fieldErrors
	if strings.TrimSpace(in.Description) == "" || len([]rune(in.Description)) > 500 {
		f.add("description", "A description of at most 500 characters.")
	}
	if len(in.Lines) == 0 {
		f.addCoded("lines", codeVoucherEmpty, "A voucher has lines.")
	}
	if len(in.Lines) > maxJournalLines {
		f.add("lines", fmt.Sprintf("At most %d lines.", maxJournalLines))
	}
	gross := in.Entry != nil && *in.Entry == gen.Gross
	date := day(in.VoucherDate)
	var codeIDs []int32
	amounts := make([]*big.Rat, len(in.Lines))
	for i, l := range in.Lines {
		a, ok := parseAmount(l.Amount)
		if !ok || a.Sign() == 0 {
			f.addCoded(fmt.Sprintf("lines[%d].amount", i), codeAmountInvalid, "An amount other than zero, with at most two decimals.")
			continue
		}
		amounts[i] = a
		if l.TaxCodeId != nil {
			codeIDs = append(codeIDs, *l.TaxCodeId)
		}
	}
	codes := map[int32]store.TaxCodesForPostingRow{}
	if len(codeIDs) > 0 {
		rows, err := q.TaxCodesForPosting(ctx, store.TaxCodesForPostingParams{Ids: codeIDs, Day: date})
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			codes[r.ID] = r
		}
	}
	var out []journalPosting
	for i, l := range in.Lines {
		if amounts[i] == nil {
			continue
		}
		base := Line{AccountID: l.AccountId, Amount: amounts[i], CustomerID: l.CustomerId, SupplierID: l.SupplierId,
			OpenItemID: l.OpenItemId, ProjectID: l.ProjectId, Text: strings.TrimSpace(deref(l.Text)), origin: i + 1}
		if l.TaxCodeId == nil {
			out = append(out, journalPosting{line: base, fromLine: i})
			continue
		}
		field := fmt.Sprintf("lines[%d].taxCodeId", i)
		code, ok := codes[*l.TaxCodeId]
		if !ok || !code.Active {
			f.addCoded(field, codeTaxCodeUnknown, "No such active tax code.")
			continue
		}
		base.TaxCodeID, base.TaxRole = l.TaxCodeId, "base"
		rate := numText(code.RatePercent)
		if code.Direction == "none" || rate == nil {
			if rate == nil && code.Direction != "none" {
				f.addCoded(field, codeTaxCodeNoRate, "No rate of the code covers "+date.Format("2006-01-02")+".")
				continue
			}
			out = append(out, journalPosting{line: base, fromLine: i})
			continue
		}
		r := ratPercent(*rate)
		netBase, tax := taxOf(amounts[i], r, gross && !code.ReverseCharge)
		base.Amount = netBase
		out = append(out, journalPosting{line: base, fromLine: i, rate: rate})
		if tax.Sign() == 0 {
			continue
		}
		taxLine := func(account int32, amount *big.Rat) journalPosting {
			return journalPosting{line: Line{AccountID: account, Amount: amount, TaxCodeID: l.TaxCodeId, TaxRole: "tax",
				TaxBase: netBase, ProjectID: l.ProjectId, Text: base.Text, origin: i + 1}, fromLine: i, rate: rate}
		}
		if code.ReverseCharge {
			deductible := l.AccountId
			if code.VatAccountID != nil {
				deductible = *code.VatAccountID
			}
			if code.CounterVatAccountID == nil {
				f.addCoded(field, codeTaxCodeUnknown, "The reverse-charge code has no counter account.")
				continue
			}
			out = append(out, taxLine(deductible, tax), taxLine(*code.CounterVatAccountID, neg(tax)))
			continue
		}
		if code.VatAccountID == nil {
			f.addCoded(field, codeTaxCodeUnknown, "The tax code has no VAT account.")
			continue
		}
		out = append(out, taxLine(*code.VatAccountID, tax))
	}
	if err := f.refusal("The voucher cannot be posted"); err != nil {
		return nil, err
	}
	return out, nil
}

// journalVoucher is a manual journal as the engine posts it.
func journalVoucher(in gen.AccountingJournalInput, postings []journalPosting, s *server, ctx context.Context) Voucher {
	v := Voucher{Kind: "manual", Date: day(in.VoucherDate), Description: strings.TrimSpace(in.Description),
		PostedBy: ptr(callerID(ctx)), At: s.now()}
	for _, p := range postings {
		v.Lines = append(v.Lines, p.line)
	}
	return v
}

// PostAccountingVoucher posts a manual journal (A6).
func (s *server) PostAccountingVoucher(ctx context.Context, req gen.PostAccountingVoucherRequestObject) (gen.PostAccountingVoucherResponseObject, error) {
	if req.Body == nil {
		return nil, invalidField("The voucher cannot be posted", "body", "", "A body is required.")
	}
	var posted Posted
	err := s.withTx(ctx, func(tx pgx.Tx, q *store.Queries) error {
		postings, err := expandJournal(ctx, q, *req.Body)
		if err != nil {
			return err
		}
		posted, err = Post(ctx, tx, journalVoucher(*req.Body, postings, s, ctx))
		return err
	})
	if err != nil {
		return nil, err
	}
	v, err := s.voucher(ctx, posted.ID)
	if err != nil {
		return nil, err
	}
	return gen.PostAccountingVoucher201JSONResponse(v), nil
}

// PreviewAccountingVoucher is a manual journal's postings and tax lines, and
// whether they balance; it writes nothing.
func (s *server) PreviewAccountingVoucher(ctx context.Context, req gen.PreviewAccountingVoucherRequestObject) (gen.PreviewAccountingVoucherResponseObject, error) {
	if req.Body == nil {
		return nil, invalidField("The voucher cannot be previewed", "body", "", "A body is required.")
	}
	postings, err := expandJournal(ctx, store.New(s.deps.Pool), *req.Body)
	if err != nil {
		return nil, err
	}
	debit, credit := new(big.Rat), new(big.Rat)
	out := gen.PreviewAccountingVoucher200JSONResponse{Lines: []gen.AccountingPreviewLine{}}
	n := int32(0)
	for _, p := range postings {
		if p.line.Amount.Sign() == 0 {
			continue
		}
		n++
		if p.line.Amount.Sign() > 0 {
			debit.Add(debit, p.line.Amount)
		} else {
			credit.Sub(credit, p.line.Amount)
		}
		pl := gen.AccountingPreviewLine{LineNo: n, FromLine: int32(p.fromLine), AccountId: p.line.AccountID,
			Amount: money(p.line.Amount), TaxCodeId: p.line.TaxCodeID, RatePercent: p.rate}
		if p.line.TaxCodeID != nil {
			pl.TaxRole = ptr(gen.AccountingTaxRole(p.line.TaxRole))
		}
		if p.line.TaxBase != nil {
			pl.TaxBase = ptr(money(p.line.TaxBase))
		}
		out.Lines = append(out.Lines, pl)
	}
	diff := sub(debit, credit)
	out.Debit, out.Credit, out.Difference, out.Balanced = money(debit), money(credit), money(diff), diff.Sign() == 0
	return out, nil
}

// ReverseAccountingVoucher reverses a voucher a person posted (A7): a system
// voucher is reversed only by its source, an opening only until the first
// lock (A9).
func (s *server) ReverseAccountingVoucher(ctx context.Context, req gen.ReverseAccountingVoucherRequestObject) (gen.ReverseAccountingVoucherResponseObject, error) {
	if req.Body == nil {
		return nil, invalidField("The voucher cannot be reversed", "body", "", "A body is required.")
	}
	reason := strings.TrimSpace(req.Body.Reason)
	if reason == "" || len([]rune(reason)) > 500 {
		return nil, invalidField("The voucher cannot be reversed", "reason", "", "A reason of at most 500 characters.")
	}
	var posted Posted
	err := s.withTx(ctx, func(tx pgx.Tx, q *store.Queries) error {
		v, err := q.GetVoucher(ctx, req.Id)
		if noRows(err) {
			return notFound()
		}
		if err != nil {
			return err
		}
		if v.SourceModule != nil {
			return conflict(codeOwnedBySource, "The voucher belongs to its source",
				"A voucher the system posted is reversed by its source: a credit note, a removed payment.")
		}
		if v.ReversedByID != nil {
			return conflict(codeAlreadyReversed, "The voucher is already reversed", "")
		}
		if v.ReversesVoucherID != nil {
			return conflict(codeAlreadyReversed, "The voucher is a reversal", "Post a new voucher instead.")
		}
		settings, err := q.GetSettingsForShare(ctx)
		if err != nil {
			return err
		}
		date := day(req.Body.Date)
		if v.Kind == "opening" {
			if settings.LockedThrough != nil {
				return conflict(codeOpeningLocked, "The opening balances are final", "They cannot be reversed after the first lock.")
			}
			date = settings.BooksStart
		}
		rev, err := reversal(ctx, q, req.Id, date, reason, settings.ChartLanguage, ptr(callerID(ctx)))
		if err != nil {
			return err
		}
		rev.At = s.now()
		posted, err = Post(ctx, tx, rev)
		return err
	})
	if err != nil {
		return nil, err
	}
	out, err := s.voucher(ctx, posted.ID)
	if err != nil {
		return nil, err
	}
	return gen.ReverseAccountingVoucher201JSONResponse(out), nil
}
