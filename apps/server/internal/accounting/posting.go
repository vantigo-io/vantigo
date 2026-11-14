package accounting

// The posting engine (A4, A8, A10; R3, R5, R10, R13): the one function every
// voucher goes through — a person's manual journal, the opening balances, a
// reversal, a settlement, and from delivery B on every source fact Invoices,
// Expenses, VAT and the bank deliver. It validates, numbers, dates, inserts and
// opens or attaches the party ledgers' items, on the caller's transaction; the
// database's triggers keep the same rules again (00179).

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vantigo-io/vantigo/server/internal/accounting/store"
)

// Source names the fact a system voucher records (A4): one fact, one voucher,
// however often it is delivered. Part tells apart the vouchers one fact makes
// (R2's occurrences); "" when it makes one.
type Source struct {
	Module, Kind string
	ID           int64
	Part         string
}

// Voucher is what Post books.
type Voucher struct {
	// Kind is one of the voucher kinds of A4.
	Kind string
	// Date is the day asked for. A system voucher (one with a Source) dated
	// before books_start is booked on books_start, and one dated on or before
	// the lock on the first open day (A8, R5); a person's is refused instead.
	Date time.Time
	// DocumentDate is the day of what the voucher records; zero is Date. It is
	// kept when the voucher is moved.
	DocumentDate time.Time
	Description  string
	// Source is the fact a system voucher records; nil for a person's.
	Source *Source
	// Rule and RuleVersion name the posting rule that made a system voucher
	// (A14); "" for none.
	Rule        string
	RuleVersion int32
	// Reverses is the voucher this one reverses, with the reason (A7).
	Reverses       *int64
	ReversalReason string
	// PostedBy is the person posting, nil for the system.
	PostedBy *uuid.UUID
	// At is when it is posted; zero is now.
	At    time.Time
	Lines []Line
}

// Line is one posting of a Voucher.
type Line struct {
	// AccountID is the account; when 0, Role names it instead (a system role
	// of A3, resolved when the voucher is posted).
	AccountID int32
	Role      string
	// Amount is signed: debit positive, credit negative. A zero line is left
	// out (R3); its tax code rides on the base posting.
	Amount *big.Rat
	// TaxCodeID and TaxRole ("base" or "tax"; "" is base) carry the line's VAT
	// treatment; TaxBase is, on a tax line, the base it was computed from.
	TaxCodeID *int32
	TaxRole   string
	TaxBase   *big.Rat
	// CustomerID or SupplierID names the party, at most one.
	CustomerID *int32
	SupplierID *int64
	// The open item a receivables or payables line belongs to (A10, R5), the
	// first that applies: OpenItemID; the item standing for SourceDocumentID
	// (Invoices' document), made from Item when there is none; a new item from
	// Item; otherwise the party's unallocated item. A line on another account
	// names none of them.
	OpenItemID       *int64
	SourceDocumentID *int64
	Item             *OpenItemSpec
	ProjectID        *int32
	Text             string

	// origin is the request line this one comes from, plus one, for the
	// field a refusal names; 0 is the line's own position.
	origin int
}

// field is the request path a refusal of the line at position i names.
func (l Line) field(i int) string {
	if l.origin > 0 {
		i = l.origin - 1
	}
	return fmt.Sprintf("lines[%d]", i)
}

// OpenItemSpec is a new open item's document: its number and dates. Its
// original amount is the line's.
type OpenItemSpec struct {
	DocumentNumber string
	DocumentDate   *time.Time
	DueDate        *time.Time
}

// Posted is the voucher Post booked, or the one a repeated source already
// has (Repeat).
type Posted struct {
	ID          int64
	FiscalYear  int32
	Number      int32
	VoucherDate time.Time
	// Moved is true when a system voucher was booked on another day than
	// asked: books_start, or the first open day after the lock.
	Moved bool
	// Repeat is true when the source already had its voucher: nothing was
	// written.
	Repeat bool
}

// The control accounts' roles, whose lines carry a party and an open item.
const (
	roleReceivables = "receivables"
	rolePayables    = "payables"
)

// Post books v on tx (A4) and returns what it booked. It refuses, as a
// *refusal the caller may answer or record, a voucher whose lines do not
// balance, name an unknown or inactive account, leave out a control
// account's party or name another party's item; a person's voucher dated
// before books_start or on or before the lock; and a voucher with nothing to
// post once its zero lines are left out (voucher_empty). A source already
// posted answers its voucher with Repeat and writes nothing.
//
// The settings row is read FOR SHARE and held to the caller's commit (R10),
// so the lock cannot move past a voucher in flight; the voucher number's
// counter row is held to the commit too, so a failed post gives its number
// back. Every open item the voucher names is taken FOR UPDATE, in id order.
//
// Post writes inside a savepoint of tx: a refusal, or a source another
// transaction posted at the same moment (the source index's violation, read
// back as Repeat), undoes what it wrote and leaves the caller's transaction
// usable.
func Post(ctx context.Context, tx pgx.Tx, v Voucher) (Posted, error) {
	if v.Source != nil {
		if got, ok, err := postedSource(ctx, store.New(tx), v.Source); err != nil || ok {
			return got, err
		}
	}
	sp, err := tx.Begin(ctx)
	if err != nil {
		return Posted{}, err
	}
	posted, err := post(ctx, sp, v)
	if err != nil {
		if rbErr := sp.Rollback(ctx); rbErr != nil {
			return Posted{}, errors.Join(err, rbErr)
		}
		if v.Source != nil && violates(err, "ux_vouchers_source") {
			if got, ok, err := postedSource(ctx, store.New(tx), v.Source); err != nil || ok {
				return got, err
			}
		}
		return Posted{}, err
	}
	if err := sp.Commit(ctx); err != nil {
		return Posted{}, err
	}
	return posted, nil
}

// postedSource is the voucher a source already has, with Repeat; ok is false
// when it has none.
func postedSource(ctx context.Context, q *store.Queries, src *Source) (Posted, bool, error) {
	got, err := q.VoucherBySource(ctx, store.VoucherBySourceParams{SourceModule: &src.Module,
		SourceKind: &src.Kind, SourceID: &src.ID, SourcePart: src.Part})
	if noRows(err) {
		return Posted{}, false, nil
	}
	if err != nil {
		return Posted{}, false, err
	}
	return Posted{ID: got.ID, FiscalYear: got.FiscalYear, Number: got.Number, VoucherDate: got.VoucherDate, Repeat: true}, true, nil
}

// violates reports whether err is a unique violation of the named index.
func violates(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == constraint
}

// post is Post's work, on the savepoint tx.
func post(ctx context.Context, tx pgx.Tx, v Voucher) (Posted, error) {
	q := store.New(tx)
	settings, err := q.GetSettingsForShare(ctx)
	if noRows(err) {
		return Posted{}, conflict(codeBooksNotSetUp, "The books are not set up", "")
	}
	if err != nil {
		return Posted{}, err
	}

	date := v.Date
	documentDate := v.DocumentDate
	if documentDate.IsZero() {
		documentDate = date
	}
	moved := false
	if date.Before(settings.BooksStart) {
		if v.Source == nil {
			return Posted{}, invalidField("The voucher cannot be posted", "voucherDate", codeDateBeforeBooks,
				"The books start on "+settings.BooksStart.Format(time.DateOnly)+"; nothing is posted before it.")
		}
		date, moved = settings.BooksStart, true
	}
	if settings.LockedThrough != nil && !date.After(*settings.LockedThrough) {
		if v.Source == nil {
			return Posted{}, conflict(codePeriodLocked, "The period is locked",
				"The books are locked through "+settings.LockedThrough.Format(time.DateOnly)+". Date the voucher after it, or move the lock back.")
		}
		date, moved = settings.LockedThrough.AddDate(0, 0, 1), true
	}

	lines, err := resolveLines(ctx, q, v.Lines, v.Reverses != nil)
	if err != nil {
		return Posted{}, err
	}
	if len(lines) == 0 {
		return Posted{}, refuse(http.StatusBadRequest, codeVoucherEmpty, "The voucher has nothing to post",
			"Every line is zero.")
	}
	total := new(big.Rat)
	for _, l := range lines {
		total.Add(total, l.Amount)
	}
	if total.Sign() != 0 {
		return Posted{}, refuse(http.StatusBadRequest, codeVoucherUnbalanced, "The voucher does not balance",
			"The lines sum to "+money(total)+", not zero.")
	}

	now := v.At
	if now.IsZero() {
		now = time.Now()
	}
	// A person's voucher names no item that stands for a document in
	// Invoices: that item moves with Invoices' own facts (M5). The opening
	// reaches one by its source document, and a reversal turns back what was.
	personal := v.Source == nil && v.Reverses == nil
	var items []int64
	for i := range lines {
		if err := attachOpenItem(ctx, tx, &lines[i], documentDate, now, personal); err != nil {
			return Posted{}, err
		}
		if id := lines[i].OpenItemID; id != nil && !slices.Contains(items, *id) {
			items = append(items, *id)
		}
	}
	if len(items) > 0 {
		slices.Sort(items)
		if err := q.LockOpenItems(ctx, items); err != nil {
			return Posted{}, err
		}
	}

	year := fiscalYear(date, int32(settings.FiscalYearStartMonth))
	number, err := q.AllocateVoucherNumber(ctx, fmt.Sprintf("voucher:%d", year))
	if err != nil {
		return Posted{}, fmt.Errorf("accounting: allocate a voucher number: %w", err)
	}
	params := store.InsertVoucherParams{FiscalYear: year, Number: number, VoucherDate: date, DocumentDate: documentDate,
		Description: v.Description, Kind: v.Kind, ReversesVoucherID: v.Reverses, PostedAt: now, PostedByUserID: v.PostedBy}
	if v.Source != nil {
		params.SourceModule, params.SourceKind, params.SourceID = &v.Source.Module, &v.Source.Kind, &v.Source.ID
		params.SourcePart = v.Source.Part
	}
	if v.Rule != "" {
		params.Rule, params.RuleVersion = &v.Rule, &v.RuleVersion
	}
	if v.Reverses != nil {
		params.ReversalReason = &v.ReversalReason
	}
	id, err := q.InsertVoucher(ctx, params)
	if err != nil {
		if violates(err, "ux_vouchers_reverses") {
			return Posted{}, conflict(codeAlreadyReversed, "The voucher is already reversed", "")
		}
		return Posted{}, fmt.Errorf("accounting: insert the voucher: %w", err)
	}
	for i, l := range lines {
		p := store.InsertPostingParams{VoucherID: id, LineNo: int32(i + 1), AccountID: l.AccountID, Amount: money(l.Amount),
			TaxCodeID: l.TaxCodeID, CustomerID: l.CustomerID, SupplierID: l.SupplierID, OpenItemID: l.OpenItemID,
			ProjectID: l.ProjectID, Description: l.Text}
		if l.TaxCodeID != nil {
			role := l.TaxRole
			if role == "" {
				role = "base"
			}
			p.TaxRole = &role
			if role == "tax" && l.TaxBase != nil {
				p.TaxBase = ptr(money(l.TaxBase))
			}
		}
		if err := q.InsertPosting(ctx, p); err != nil {
			return Posted{}, fmt.Errorf("accounting: insert posting %d: %w", i+1, err)
		}
	}
	return Posted{ID: id, FiscalYear: year, Number: number, VoucherDate: date, Moved: moved}, nil
}

// resolvedLine is a Line with its account resolved and its control role.
type resolvedLine struct {
	Line
	controlRole string
	// index is the line's position in the voucher as given, zero lines
	// counted, for the field a refusal names.
	index int
}

// resolveLines resolves every line's account, leaves out the zero lines and
// holds each line to its account: known and active (inactive too for a
// reversal, which turns back what was posted, M8), a control account's party
// named and of the right kind, an open item only on a control account.
func resolveLines(ctx context.Context, q *store.Queries, in []Line, reversal bool) ([]resolvedLine, error) {
	roles, err := q.RoleAccounts(ctx)
	if err != nil {
		return nil, err
	}
	byRole := map[string]int32{}
	roleOf := map[int32]string{}
	for _, r := range roles {
		byRole[r.Role] = r.ID
		roleOf[r.ID] = r.Role
	}
	var ids []int32
	var f fieldErrors
	out := make([]resolvedLine, 0, len(in))
	for i, l := range in {
		field := l.field(i)
		if l.AccountID == 0 {
			id, ok := byRole[l.Role]
			if !ok {
				f.addCoded(field+".accountId", codeAccountUnknown, "No account holds the role "+l.Role+".")
				continue
			}
			l.AccountID = id
		}
		if l.Amount == nil {
			f.addCoded(field+".amount", codeAmountInvalid, "An amount is required.")
			continue
		}
		if !new(big.Rat).Mul(l.Amount, big.NewRat(100, 1)).IsInt() {
			f.addCoded(field+".amount", codeAmountInvalid, "At most two decimals.")
			continue
		}
		if l.Amount.Sign() == 0 {
			continue
		}
		ids = append(ids, l.AccountID)
		out = append(out, resolvedLine{Line: l, controlRole: roleOf[l.AccountID], index: i})
	}
	accounts, err := q.AccountsForPosting(ctx, ids)
	if err != nil {
		return nil, err
	}
	known := map[int32]store.AccountsForPostingRow{}
	for _, a := range accounts {
		known[a.ID] = a
	}
	for _, l := range out {
		field := l.field(l.index)
		a, ok := known[l.AccountID]
		switch {
		case !ok:
			f.addCoded(field+".accountId", codeAccountUnknown, "No such account.")
			continue
		case !a.Active && !reversal:
			f.addCoded(field+".accountId", codeAccountInactive, "Account "+a.Number+" is inactive.")
		}
		if l.CustomerID != nil && l.SupplierID != nil {
			f.addCoded(field+".supplierId", codePartyNotAllowed, "A line names a customer or a supplier, not both.")
		}
		switch l.controlRole {
		case roleReceivables:
			if l.CustomerID == nil {
				f.addCoded(field+".customerId", codePartyRequired, "A line on receivables names its customer.")
			}
			if l.SupplierID != nil {
				f.addCoded(field+".supplierId", codePartyNotAllowed, "Receivables are a customer's.")
			}
		case rolePayables:
			if l.SupplierID == nil {
				f.addCoded(field+".supplierId", codePartyRequired, "A line on payables names its supplier.")
			}
			if l.CustomerID != nil {
				f.addCoded(field+".customerId", codePartyNotAllowed, "Payables are a supplier's.")
			}
		default:
			if l.OpenItemID != nil || l.SourceDocumentID != nil || l.Item != nil {
				f.addCoded(field+".openItemId", codeOpenItemUnknown, "Only a line on receivables or payables belongs to an open item.")
			}
		}
		if l.TaxRole != "" && !slices.Contains([]string{"base", "tax"}, l.TaxRole) {
			return nil, fmt.Errorf("accounting: tax role %q", l.TaxRole)
		}
	}
	if err := f.refusal("The voucher cannot be posted"); err != nil {
		return nil, err
	}
	return out, nil
}

// attachOpenItem sets a control line's open item (A10, R5): the one named,
// the one standing for its source document (made when missing), a new one,
// or the party's unallocated item. An item named, or found by its source
// document, is the line's party's; on a person's voucher (personal) an item
// named stands for no document in Invoices.
func attachOpenItem(ctx context.Context, tx pgx.Tx, l *resolvedLine, documentDate, now time.Time, personal bool) error {
	if l.controlRole != roleReceivables && l.controlRole != rolePayables {
		return nil
	}
	q := store.New(tx)
	field := l.field(l.index)
	kind, party := "customer", int64(deref(l.CustomerID))
	if l.controlRole == rolePayables {
		kind, party = "supplier", deref(l.SupplierID)
	}
	partys := func(customer *int32, supplier *int64) bool {
		if kind == "customer" {
			return deref(customer) == int32(party)
		}
		return deref(supplier) == party
	}
	if l.OpenItemID != nil {
		items, err := q.OpenItemsForPosting(ctx, []int64{*l.OpenItemID})
		if err != nil {
			return err
		}
		if len(items) != 1 || items[0].Kind != kind || !partys(items[0].CustomerID, items[0].SupplierID) {
			return invalidField("The voucher cannot be posted", field+".openItemId", codeOpenItemUnknown,
				"The open item is not this party's.")
		}
		if personal && items[0].SourceDocumentID != nil {
			return invalidField("The voucher cannot be posted", field+".openItemId", codeOpenItemSourceDocument,
				"The open item stands for an invoice in Invoices: register the payment or the credit note there.")
		}
		return nil
	}
	bySource := func() (bool, error) {
		got, err := q.OpenItemBySourceDocument(ctx, store.OpenItemBySourceDocumentParams{Kind: kind, SourceDocumentID: l.SourceDocumentID})
		if noRows(err) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if !partys(got.CustomerID, got.SupplierID) {
			return false, invalidField("The voucher cannot be posted", field+".sourceDocumentId", codeOpenItemUnknown,
				"The document's open item is another party's.")
		}
		l.OpenItemID = &got.ID
		return true, nil
	}
	if l.SourceDocumentID != nil {
		if found, err := bySource(); err != nil || found {
			return err
		}
	}
	if l.Item != nil || l.SourceDocumentID != nil {
		spec := OpenItemSpec{}
		if l.Item != nil {
			spec = *l.Item
		}
		docDate := spec.DocumentDate
		if docDate == nil {
			docDate = &documentDate
		}
		var number *string
		if spec.DocumentNumber != "" {
			number = &spec.DocumentNumber
		}
		params := store.InsertOpenItemParams{Kind: kind, DocumentNumber: number, DocumentDate: docDate, DueDate: spec.DueDate,
			OriginalAmount: money(l.Amount), SourceDocumentID: l.SourceDocumentID, Now: now}
		params.CustomerID, params.SupplierID = l.CustomerID, l.SupplierID
		// A savepoint, so another transaction's item for the same document,
		// committed while this one waited on the index, is read back (L4).
		sp, err := tx.Begin(ctx)
		if err != nil {
			return err
		}
		id, err := store.New(sp).InsertOpenItem(ctx, params)
		if err != nil {
			if rbErr := sp.Rollback(ctx); rbErr != nil {
				return errors.Join(err, rbErr)
			}
			if l.SourceDocumentID != nil && violates(err, "ux_open_items_source_document") {
				if found, err := bySource(); err != nil || found {
					return err
				}
			}
			return fmt.Errorf("accounting: open an item: %w", err)
		}
		if err := sp.Commit(ctx); err != nil {
			return err
		}
		l.OpenItemID = &id
		return nil
	}
	ensure := store.EnsureUnallocatedItemParams{Kind: kind, CustomerID: l.CustomerID, SupplierID: l.SupplierID, Now: now}
	if err := q.EnsureUnallocatedItem(ctx, ensure); err != nil {
		return err
	}
	id, err := q.UnallocatedItem(ctx, store.UnallocatedItemParams{Kind: kind, PartyID: party})
	if err != nil {
		return err
	}
	l.OpenItemID = &id
	return nil
}

// reversal is the voucher that reverses the one id names (A7): every posting
// with its sign turned, on the same accounts, parties, items, tax codes and
// projects, dated date.
func reversal(ctx context.Context, q *store.Queries, id int64, date time.Time, reason, language string, by *uuid.UUID) (Voucher, error) {
	v, err := q.GetVoucher(ctx, id)
	if err != nil {
		return Voucher{}, err
	}
	postings, err := q.ListPostings(ctx, []int64{id})
	if err != nil {
		return Voucher{}, err
	}
	prefix := "Reversal of "
	if language == "nb" {
		prefix = "Tilbakeføring av "
	}
	out := Voucher{Kind: v.Kind, Date: date, DocumentDate: date, Description: prefix + display(v.FiscalYear, v.Number) + ": " + v.Description,
		Reverses: &id, ReversalReason: reason, PostedBy: by}
	if r := []rune(out.Description); len(r) > 500 {
		out.Description = string(r[:500])
	}
	for _, p := range postings {
		l := Line{AccountID: p.AccountID, Amount: neg(mustRat(p.Amount)), TaxCodeID: p.TaxCodeID, CustomerID: p.CustomerID,
			SupplierID: p.SupplierID, OpenItemID: p.OpenItemID, ProjectID: p.ProjectID, Text: p.Description}
		if p.TaxRole != nil {
			l.TaxRole = *p.TaxRole
		}
		if base := numText(p.TaxBase); base != nil {
			l.TaxBase = neg(mustRat(*base))
		}
		out.Lines = append(out.Lines, l)
	}
	return out, nil
}
