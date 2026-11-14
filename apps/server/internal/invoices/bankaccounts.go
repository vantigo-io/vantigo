package invoices

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
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
// past this one's (plan reading 51) — NULL when it has none. feed is never
// taken here, only set by switching the bank feed on; a change away from it
// is refused while the feed reads the account (bank feed plan finding 9),
// judged under the same lock every switch of the feed takes first.
func (s *server) PutInvoicesBankAccountsByAccountFormat(ctx context.Context, req gen.PutInvoicesBankAccountsByAccountFormatRequestObject) (gen.PutInvoicesBankAccountsByAccountFormatResponseObject, error) {
	format := req.Body.Format
	if format != string(bankfile.FormatOCR) && format != string(bankfile.FormatCamt054) {
		return gen.PutInvoicesBankAccountsByAccountFormat400ApplicationProblemPlusJSONResponse(invalid("Invalid bank account format",
			fieldError("format", fmt.Sprintf("'format' must be 'ocr' or 'camt054', but was '%s'.", format)))), nil
	}
	now := s.deps.Clock()
	by := callerID(ctx)
	var missing, feedOn bool
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
		if current.Format == string(bankfile.FormatFeed) {
			if feedOn, err = bankFeedEnabledOn(ctx, txq, current.Account); err != nil {
				return err
			}
			if feedOn {
				return errRefused
			}
		}
		return changeImportFormat(ctx, txq, current, format, by, now)
	})
	switch {
	case missing:
		return gen.PutInvoicesBankAccountsByAccountFormat404Response{}, nil
	case feedOn:
		return gen.PutInvoicesBankAccountsByAccountFormat409ApplicationProblemPlusJSONResponse(bankFeedEnabledConflict(req.Account)), nil
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

// changeImportFormat changes current's format to format under its lock: the
// old format kept as previous_format with cutover_through the latest booking
// day of the account's own lines in it. A detour through the bank feed loses
// no cutover: when the change goes to or from feed and the old format brought
// the account no lines, the previous format and cutover a file format left
// are carried forward instead (bank feed review) — camt054 → feed → ocr keeps
// camt054's cutover while the feed booked nothing.
func changeImportFormat(ctx context.Context, txq *store.Queries, current store.InvoicesBankImportAccount, format string, by uuid.UUID, now time.Time) error {
	cutover, err := txq.LatestBookedInFormat(ctx, store.LatestBookedInFormatParams{Account: current.Account, Format: current.Format})
	if err != nil {
		return fmt.Errorf("invoices: read account %s's latest booking in %s: %w", current.Account, current.Format, err)
	}
	previous := current.Format
	viaFeed := current.Format == string(bankfile.FormatFeed) || format == string(bankfile.FormatFeed)
	if viaFeed && !cutover.Valid && current.PreviousFormat != nil && *current.PreviousFormat != format {
		previous, cutover = *current.PreviousFormat, current.CutoverThrough
	}
	if _, err := txq.ChangeImportFormat(ctx, store.ChangeImportFormatParams{
		Account: current.Account, Format: format, PreviousFormat: previous, CutoverThrough: cutover, SetByUserID: by, SetAt: now,
	}); err != nil {
		return fmt.Errorf("invoices: change account %s's format: %w", current.Account, err)
	}
	return nil
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
