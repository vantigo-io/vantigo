package invoices

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/invoices/bankfile"
	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is the bank import accounts (invoices payments and reminders
// design D3): every receiving account a file was imported for, in the format
// its first import set, and the manager's change of that format, which keeps
// the old format and the account's own last booking day in it — the cutover
// matching holds back what the old format may already have registered by
// (D4). A change of bank is a new account, with its own row.

// GetInvoicesBankAccounts List the bank import accounts
// (GET /api/v1/invoices/bank-accounts)
func (s *server) GetInvoicesBankAccounts(ctx context.Context, _ gen.GetInvoicesBankAccountsRequestObject) (gen.GetInvoicesBankAccountsResponseObject, error) {
	rows, err := store.New(s.deps.Pool).ListImportAccounts(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("invoices: list the bank import accounts: %w", err)
	}
	data := make([]gen.InvoicesBankAccount, 0, len(rows))
	for _, r := range rows {
		data = append(data, bankAccountResponse(r))
	}
	return gen.GetInvoicesBankAccounts200JSONResponse(gen.InvoicesBankAccountsResponse{Data: data}), nil
}

// PutInvoicesBankAccountsByAccountFormat Change an account's bank file format
// (PUT /api/v1/invoices/bank-accounts/{account}/format)
//
// The account's row FOR UPDATE, its only lock (D18): the same format leaves
// it as it is; another keeps the old one as previous_format with
// cutover_through the latest booking day of the account's own lines in it —
// never a file's last_booked_on, which a file naming two accounts can push
// past this one's (plan reading 51) — NULL when it has none.
func (s *server) PutInvoicesBankAccountsByAccountFormat(ctx context.Context, req gen.PutInvoicesBankAccountsByAccountFormatRequestObject) (gen.PutInvoicesBankAccountsByAccountFormatResponseObject, error) {
	format := req.Body.Format
	if format != string(bankfile.FormatOCR) && format != string(bankfile.FormatCamt054) {
		return gen.PutInvoicesBankAccountsByAccountFormat400ApplicationProblemPlusJSONResponse(invalid("Invalid bank account format",
			fieldError("format", fmt.Sprintf("'format' must be 'ocr' or 'camt054', but was '%s'.", format)))), nil
	}
	now := s.deps.Clock()
	by := callerID(ctx)
	var missing bool
	err := s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		current, err := lockImportAccount(ctx, txq, req.Account)
		if errors.Is(err, pgx.ErrNoRows) {
			missing = true
			return errRefused
		}
		if err != nil {
			return fmt.Errorf("invoices: lock bank import account %s: %w", req.Account, err)
		}
		if current.Format == format {
			return nil
		}
		cutover, err := txq.LatestBookedInFormat(ctx, store.LatestBookedInFormatParams{Account: current.Account, Format: current.Format})
		if err != nil {
			return fmt.Errorf("invoices: read account %s's latest booking in %s: %w", current.Account, current.Format, err)
		}
		if _, err := txq.ChangeImportFormat(ctx, store.ChangeImportFormatParams{
			Account: current.Account, Format: format, CutoverThrough: cutover, SetByUserID: by, SetAt: now,
		}); err != nil {
			return fmt.Errorf("invoices: change account %s's format: %w", current.Account, err)
		}
		return nil
	})
	switch {
	case missing:
		return gen.PutInvoicesBankAccountsByAccountFormat404Response{}, nil
	case err != nil:
		return nil, err
	}
	rows, err := store.New(s.deps.Pool).ListImportAccounts(ctx, &req.Account)
	if err != nil {
		return nil, fmt.Errorf("invoices: re-read bank import account %s: %w", req.Account, err)
	}
	if len(rows) != 1 {
		return nil, fmt.Errorf("invoices: re-read bank import account %s: %d rows, want one", req.Account, len(rows))
	}
	return gen.PutInvoicesBankAccountsByAccountFormat200JSONResponse(bankAccountResponse(rows[0])), nil
}

// bankAccountResponse is one account on the wire.
func bankAccountResponse(r store.ListImportAccountsRow) gen.InvoicesBankAccount {
	a := gen.InvoicesBankAccount{
		Account: r.Account, Format: r.Format, PreviousFormat: r.PreviousFormat, CutoverThrough: wireDateOf(r.CutoverThrough),
		SetBy: r.SetByUserID, SetAt: r.SetAt, LastBookedOn: wireDateOf(r.LastBookedOn),
	}
	if r.LastFileID != 0 {
		a.LastFileId, a.LastUploadedAt = &r.LastFileID, &r.LastUploadedAt
	}
	return a
}
