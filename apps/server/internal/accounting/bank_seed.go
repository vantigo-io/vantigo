package accounting

// The bank's entry points for a program that is not a request — the demo
// seed — each the same path the API takes: a bank account checked and added,
// a statement file read and written with its duplicate guard, and a line's
// suggestion accepted in one transaction under the line's lock.

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/accounting/gen"
	"github.com/vantigo-io/vantigo/server/internal/accounting/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// AddBankAccount adds a bank account on a ledger account (A26), checked as
// the API checks it; it answers its id.
func AddBankAccount(ctx context.Context, d module.Deps, in gen.AccountingBankAccountInput) (int64, error) {
	s := newServer(d)
	var id int64
	err := s.withTx(ctx, func(_ pgx.Tx, q *store.Queries) error {
		checked, err := checkBankAccount(ctx, q, in)
		if err != nil {
			return err
		}
		id, err = q.InsertBankAccount(ctx, store.InsertBankAccountParams{Name: checked.name, AccountNumber: checked.number,
			Iban: checked.iban, Bic: checked.bic, LedgerAccountID: checked.ledger, Active: checked.active, Now: s.now()})
		return bankAccountConflict(err)
	})
	return id, err
}

// ImportBankStatement imports a camt.053 or CSV file (A27) as by, as the API
// does: kept in the object store, routed by account, the duplicate guard
// held. bankAccountID names the bank account a CSV file is of.
func ImportBankStatement(ctx context.Context, d module.Deps, name string, data []byte, bankAccountID *int64, by uuid.UUID) (gen.AccountingBankImportResult, error) {
	return newServer(d).importStatement(ctx, statementUpload{name: name, data: data}, bankAccountID, by)
}

// BankSuggestionKeys is an open line's suggestions' keys, most certain first.
func BankSuggestionKeys(ctx context.Context, d module.Deps, lineID int64) ([]string, error) {
	q := store.New(d.Pool)
	row, err := q.GetStatementLine(ctx, lineID)
	if err != nil {
		return nil, err
	}
	l := lineFromRow(row)
	account, err := accountOf(ctx, q, l.bankAccountID)
	if err != nil {
		return nil, err
	}
	sg, err := newSuggester(ctx, q, account)
	if err != nil {
		return nil, err
	}
	list, err := sg.suggest(ctx, l)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(list))
	for _, x := range list {
		keys = append(keys, x.key)
	}
	return keys, nil
}

// AcceptBankSuggestion accepts a line's suggestion by its key as by (A28),
// in one transaction under the line's lock, as the API does.
func AcceptBankSuggestion(ctx context.Context, d module.Deps, lineID int64, key string, by uuid.UUID) error {
	s := newServer(d)
	return s.withTx(ctx, func(tx pgx.Tx, q *store.Queries) error { return s.accept(ctx, tx, q, lineID, key, acceptOptions{}, by) })
}
