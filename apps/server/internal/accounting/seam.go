package accounting

// The seam of delivery F: the bank operations its lanes fill in.

import (
	"context"
	"errors"

	"github.com/vantigo-io/vantigo/server/internal/accounting/gen"
)

var errNotYet = errors.New("accounting: not built yet")

func (s *server) ListAccountingBankAccounts(context.Context, gen.ListAccountingBankAccountsRequestObject) (gen.ListAccountingBankAccountsResponseObject, error) {
	return nil, errNotYet
}

func (s *server) CreateAccountingBankAccount(context.Context, gen.CreateAccountingBankAccountRequestObject) (gen.CreateAccountingBankAccountResponseObject, error) {
	return nil, errNotYet
}

func (s *server) DeleteAccountingBankAccount(context.Context, gen.DeleteAccountingBankAccountRequestObject) (gen.DeleteAccountingBankAccountResponseObject, error) {
	return nil, errNotYet
}

func (s *server) GetAccountingBankAccount(context.Context, gen.GetAccountingBankAccountRequestObject) (gen.GetAccountingBankAccountResponseObject, error) {
	return nil, errNotYet
}

func (s *server) UpdateAccountingBankAccount(context.Context, gen.UpdateAccountingBankAccountRequestObject) (gen.UpdateAccountingBankAccountResponseObject, error) {
	return nil, errNotYet
}

func (s *server) PutAccountingBankCsvMapping(context.Context, gen.PutAccountingBankCsvMappingRequestObject) (gen.PutAccountingBankCsvMappingResponseObject, error) {
	return nil, errNotYet
}

func (s *server) PreviewAccountingBankCsv(context.Context, gen.PreviewAccountingBankCsvRequestObject) (gen.PreviewAccountingBankCsvResponseObject, error) {
	return nil, errNotYet
}

func (s *server) GetAccountingBankReconciliation(context.Context, gen.GetAccountingBankReconciliationRequestObject) (gen.GetAccountingBankReconciliationResponseObject, error) {
	return nil, errNotYet
}

func (s *server) SetAccountingBankAccountSource(context.Context, gen.SetAccountingBankAccountSourceRequestObject) (gen.SetAccountingBankAccountSourceResponseObject, error) {
	return nil, errNotYet
}

func (s *server) ListAccountingBankFeedAccounts(context.Context, gen.ListAccountingBankFeedAccountsRequestObject) (gen.ListAccountingBankFeedAccountsResponseObject, error) {
	return nil, errNotYet
}

func (s *server) ImportAccountingBankStatement(context.Context, gen.ImportAccountingBankStatementRequestObject) (gen.ImportAccountingBankStatementResponseObject, error) {
	return nil, errNotYet
}

func (s *server) AcceptAccountingBankExact(context.Context, gen.AcceptAccountingBankExactRequestObject) (gen.AcceptAccountingBankExactResponseObject, error) {
	return nil, errNotYet
}

func (s *server) AcceptAccountingBankSuggestion(context.Context, gen.AcceptAccountingBankSuggestionRequestObject) (gen.AcceptAccountingBankSuggestionResponseObject, error) {
	return nil, errNotYet
}

func (s *server) CodeAccountingBankLine(context.Context, gen.CodeAccountingBankLineRequestObject) (gen.CodeAccountingBankLineResponseObject, error) {
	return nil, errNotYet
}

func (s *server) UndoAccountingBankMatch(context.Context, gen.UndoAccountingBankMatchRequestObject) (gen.UndoAccountingBankMatchResponseObject, error) {
	return nil, errNotYet
}

func (s *server) ListAccountingBankRules(context.Context, gen.ListAccountingBankRulesRequestObject) (gen.ListAccountingBankRulesResponseObject, error) {
	return nil, errNotYet
}

func (s *server) CreateAccountingBankRule(context.Context, gen.CreateAccountingBankRuleRequestObject) (gen.CreateAccountingBankRuleResponseObject, error) {
	return nil, errNotYet
}

func (s *server) DeleteAccountingBankRule(context.Context, gen.DeleteAccountingBankRuleRequestObject) (gen.DeleteAccountingBankRuleResponseObject, error) {
	return nil, errNotYet
}

func (s *server) UpdateAccountingBankRule(context.Context, gen.UpdateAccountingBankRuleRequestObject) (gen.UpdateAccountingBankRuleResponseObject, error) {
	return nil, errNotYet
}

func (s *server) ListAccountingBankStatements(context.Context, gen.ListAccountingBankStatementsRequestObject) (gen.ListAccountingBankStatementsResponseObject, error) {
	return nil, errNotYet
}

func (s *server) DownloadAccountingBankStatementFile(context.Context, gen.DownloadAccountingBankStatementFileRequestObject) (gen.DownloadAccountingBankStatementFileResponseObject, error) {
	return nil, errNotYet
}

func (s *server) GetAccountingBankStatus(context.Context, gen.GetAccountingBankStatusRequestObject) (gen.GetAccountingBankStatusResponseObject, error) {
	return nil, errNotYet
}
