package accounting

// The party ledgers' open items (A10, R5) and their settlements: a receipt,
// a payment or a refund registered against an item, which is how an opening
// balance is paid and a credit balance refunded. An item that stands for a
// document in Invoices is paid there, never here.

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/accounting/gen"
	"github.com/vantigo-io/vantigo/server/internal/accounting/store"
)

const (
	defaultOpenItemLimit = 200
	maxOpenItemLimit     = 1000
)

// ListAccountingOpenItems is a page of the open items, by party and due
// date, with the count of every match.
func (s *server) ListAccountingOpenItems(ctx context.Context, req gen.ListAccountingOpenItemsRequestObject) (gen.ListAccountingOpenItemsResponseObject, error) {
	p := req.Params
	limit, offset := int32(defaultOpenItemLimit), int32(0)
	if p.Limit != nil {
		limit = min(max(*p.Limit, 1), maxOpenItemLimit)
	}
	if p.Offset != nil && *p.Offset > 0 {
		offset = *p.Offset
	}
	params := store.ListOpenItemsParams{CustomerID: p.CustomerId, SupplierID: p.SupplierId, OnlyOpen: p.Open == nil || *p.Open,
		RowLimit: limit, RowOffset: offset}
	if p.Kind != nil {
		params.Kind = ptr(string(*p.Kind))
	}
	rows, err := store.New(s.deps.Pool).ListOpenItems(ctx, params)
	if err != nil {
		return nil, err
	}
	var ids []int32
	for _, r := range rows {
		if r.CustomerID != nil {
			ids = append(ids, *r.CustomerID)
		}
	}
	names := s.customerNames(ctx, ids)
	out := gen.ListAccountingOpenItems200JSONResponse{Items: []gen.AccountingOpenItem{}}
	for _, r := range rows {
		out.Total = int32(r.Total)
		out.Items = append(out.Items, openItemView(store.GetOpenItemRow{ID: r.ID, Kind: r.Kind, CustomerID: r.CustomerID,
			SupplierID: r.SupplierID, Unallocated: r.Unallocated, DocumentNumber: r.DocumentNumber, DocumentDate: r.DocumentDate,
			DueDate: r.DueDate, OriginalAmount: r.OriginalAmount, SourceDocumentID: r.SourceDocumentID, Balance: r.Balance}, names))
	}
	return out, nil
}

func openItemView(r store.GetOpenItemRow, names map[int32]string) gen.AccountingOpenItem {
	balance := mustRat(r.Balance)
	out := gen.AccountingOpenItem{Id: r.ID, Kind: gen.AccountingPartyKind(r.Kind), CustomerId: r.CustomerID, SupplierId: r.SupplierID,
		Unallocated: r.Unallocated, DocumentNumber: r.DocumentNumber, DocumentDate: apiDatePtr(r.DocumentDate),
		DueDate: apiDatePtr(r.DueDate), OriginalAmount: money(mustRat(r.OriginalAmount)), Balance: money(balance),
		Settled: balance.Sign() == 0, SourceDocumentId: r.SourceDocumentID}
	if r.CustomerID != nil {
		if name, ok := names[*r.CustomerID]; ok {
			out.CustomerName = &name
		}
	}
	return out
}

// SettleAccountingOpenItem registers money moved against an item (R5): for a
// customer's item a receipt (the balance owed) or a refund (a credit balance),
// for a supplier's a payment or a refund received. amount is positive and at
// most the balance; the bank side is the given asset account or the bank
// role's.
func (s *server) SettleAccountingOpenItem(ctx context.Context, req gen.SettleAccountingOpenItemRequestObject) (gen.SettleAccountingOpenItemResponseObject, error) {
	const title = "The settlement cannot be registered"
	if req.Body == nil {
		return nil, invalidField(title, "body", "", "A body is required.")
	}
	b := *req.Body
	amount, ok := parseAmount(b.Amount)
	if !ok || amount.Sign() <= 0 {
		return nil, invalidField(title, "amount", codeAmountInvalid, "A positive amount with at most two decimals.")
	}
	text := strings.TrimSpace(deref(b.Text))
	if len([]rune(text)) > 500 {
		return nil, invalidField(title, "text", "", "At most 500 characters.")
	}
	var posted Posted
	err := s.withTx(ctx, func(tx pgx.Tx, q *store.Queries) error {
		if err := q.LockOpenItem(ctx, req.Id); err != nil {
			return err
		}
		item, err := q.GetOpenItem(ctx, req.Id)
		if noRows(err) {
			return notFound()
		}
		if err != nil {
			return err
		}
		if item.SourceDocumentID != nil {
			return conflict(codeSettlementSourceDoc, "The item is paid in Invoices",
				"It stands for an invoice in Invoices: register the payment on the invoice there.")
		}
		balance := mustRat(item.Balance)
		if balance.Sign() == 0 {
			return conflict(codeSettlementSettled, "The item is settled", "Nothing is left on it.")
		}
		if amount.Cmp(abs(balance)) > 0 {
			return conflict(codeSettlementExceeds, "More than the item's balance", "The balance is "+money(abs(balance))+".")
		}
		bank := Line{Role: "bank"}
		if b.BankAccountId != nil {
			accounts, err := q.AccountsForPosting(ctx, []int32{*b.BankAccountId})
			if err != nil {
				return err
			}
			if len(accounts) != 1 || accounts[0].Kind != "asset" || !accounts[0].Active {
				return invalidField(title, "bankAccountId", codeBankAccountInvalid, "An active asset account.")
			}
			bank = Line{AccountID: *b.BankAccountId}
		}
		// The item moves toward zero by amount; the bank takes the other side.
		toItem := neg(amount)
		if balance.Sign() < 0 {
			toItem = amount
		}
		role, kind := roleReceivables, "customer_payment"
		if item.Kind == "supplier" {
			role, kind = rolePayables, "supplier_payment"
		}
		settings, err := q.GetSettings(ctx)
		if err != nil {
			return err
		}
		description := text
		if description == "" {
			description = settlementDescription(item, settings.ChartLanguage)
		}
		bank.Amount, bank.Text = neg(toItem), text
		v := Voucher{Kind: kind, Date: day(b.Date), Description: description, PostedBy: ptr(callerID(ctx)), At: s.now(),
			Lines: []Line{
				{Role: role, Amount: toItem, CustomerID: item.CustomerID, SupplierID: item.SupplierID, OpenItemID: &item.ID, Text: text},
				bank,
			}}
		posted, err = Post(ctx, tx, v)
		return err
	})
	if err != nil {
		return nil, err
	}
	out, err := s.voucher(ctx, posted.ID)
	if err != nil {
		return nil, err
	}
	return gen.SettleAccountingOpenItem201JSONResponse(out), nil
}

// settlementDescription is a settlement voucher's description when the
// person gave none.
func settlementDescription(item store.GetOpenItemRow, language string) string {
	doc := deref(item.DocumentNumber)
	if language == "nb" {
		if doc == "" {
			return "Oppgjør av åpen post"
		}
		return "Oppgjør av " + doc
	}
	if doc == "" {
		return "Settlement of an open item"
	}
	return "Settlement of " + doc
}
