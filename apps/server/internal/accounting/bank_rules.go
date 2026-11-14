package accounting

// Bank rules (A28): a line whose text or counterparty holds the rule's words,
// in its direction, is proposed as a voucher to the rule's account with its
// tax code — bank fees, interest, a subscription paid by card. A change is a
// new version; a match keeps the version it was made with, and a rule used by
// a match is made inactive rather than deleted.

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/accounting/gen"
	"github.com/vantigo-io/vantigo/server/internal/accounting/store"
)

func bankRuleView(r store.ListBankRulesRow) gen.AccountingBankRule {
	return gen.AccountingBankRule{Id: r.ID, Name: r.Name, BankAccountId: r.BankAccountID, TextContains: r.TextContains,
		CounterpartyContains: r.CounterpartyContains, Direction: r.Direction, AccountId: r.AccountID, AccountNumber: r.AccountNumber,
		AccountName: r.AccountName, TaxCodeId: r.TaxCodeID, TaxCode: r.TaxCode, ProjectId: r.ProjectID, Description: r.Description,
		Active: r.Active, Version: r.Version}
}

// bankRuleOne is one rule for an answer.
func bankRuleOne(ctx context.Context, q *store.Queries, id int64) (gen.AccountingBankRule, error) {
	rows, err := q.ListBankRules(ctx)
	if err != nil {
		return gen.AccountingBankRule{}, err
	}
	for _, r := range rows {
		if r.ID == id {
			return bankRuleView(r), nil
		}
	}
	return gen.AccountingBankRule{}, notFound()
}

// bankRuleInput is a rule's body checked.
type bankRuleInput struct {
	name, direction, description string
	text, party                  *string
	bankAccount                  *int64
	account                      int32
	taxCode, project             *int32
	active                       bool
}

func checkBankRule(ctx context.Context, q *store.Queries, b gen.AccountingBankRuleInput) (bankRuleInput, error) {
	var f fieldErrors
	out := bankRuleInput{name: strings.TrimSpace(b.Name), direction: "any", description: strings.TrimSpace(deref(b.Description)),
		bankAccount: b.BankAccountId, account: b.AccountId, taxCode: b.TaxCodeId, project: b.ProjectId,
		active: b.Active == nil || *b.Active}
	if out.name == "" || len([]rune(out.name)) > 100 {
		f.add("name", "A name of at most 100 characters.")
	}
	if b.Direction != nil {
		switch *b.Direction {
		case "in", "out", "any":
			out.direction = *b.Direction
		default:
			f.add("direction", "in, out or any.")
		}
	}
	words := func(p *string, field string) *string {
		if p == nil || strings.TrimSpace(*p) == "" {
			return nil
		}
		v := strings.TrimSpace(*p)
		if len([]rune(v)) > 140 {
			f.add(field, "At most 140 characters.")
		}
		return &v
	}
	out.text, out.party = words(b.TextContains, "textContains"), words(b.CounterpartyContains, "counterpartyContains")
	if out.text == nil && out.party == nil {
		f.add("textContains", "Words the line's text or its counterparty holds.")
	}
	if len([]rune(out.description)) > 500 {
		f.add("description", "At most 500 characters.")
	}
	accounts, err := q.AccountsForPosting(ctx, []int32{b.AccountId})
	if err != nil {
		return out, err
	}
	if len(accounts) != 1 || !accounts[0].Active {
		f.addCoded("accountId", codeAccountUnknown, "An active account.")
	}
	if b.TaxCodeId != nil {
		codes, err := q.TaxCodesByCode(ctx)
		if err != nil {
			return out, err
		}
		found := false
		for _, c := range codes {
			if c.ID == *b.TaxCodeId && c.Active {
				found = true
			}
		}
		if !found {
			f.addCoded("taxCodeId", codeTaxCodeUnknown, "No such active tax code.")
		}
	}
	if b.BankAccountId != nil {
		if _, err := q.GetBankAccount(ctx, *b.BankAccountId); noRows(err) {
			f.add("bankAccountId", "No such bank account.")
		} else if err != nil {
			return out, err
		}
	}
	return out, f.refusal("The bank rule cannot be saved")
}

// ListAccountingBankRules is the bank rules.
func (s *server) ListAccountingBankRules(ctx context.Context, _ gen.ListAccountingBankRulesRequestObject) (gen.ListAccountingBankRulesResponseObject, error) {
	rows, err := store.New(s.deps.Pool).ListBankRules(ctx)
	if err != nil {
		return nil, err
	}
	out := gen.ListAccountingBankRules200JSONResponse{Items: []gen.AccountingBankRule{}}
	for _, r := range rows {
		out.Items = append(out.Items, bankRuleView(r))
	}
	return out, nil
}

// CreateAccountingBankRule adds a bank rule.
func (s *server) CreateAccountingBankRule(ctx context.Context, req gen.CreateAccountingBankRuleRequestObject) (gen.CreateAccountingBankRuleResponseObject, error) {
	if req.Body == nil {
		return nil, invalidField("The bank rule cannot be saved", "body", "", "A body is required.")
	}
	var out gen.AccountingBankRule
	err := s.withTx(ctx, func(_ pgx.Tx, q *store.Queries) error {
		in, err := checkBankRule(ctx, q, *req.Body)
		if err != nil {
			return err
		}
		id, err := q.InsertBankRule(ctx, store.InsertBankRuleParams{Name: in.name, BankAccountID: in.bankAccount, TextContains: in.text,
			CounterpartyContains: in.party, Direction: in.direction, AccountID: in.account, TaxCodeID: in.taxCode,
			ProjectID: in.project, Description: in.description, Active: in.active, Now: s.now()})
		if err != nil {
			return err
		}
		out, err = bankRuleOne(ctx, q, id)
		return err
	})
	if err != nil {
		return nil, err
	}
	return gen.CreateAccountingBankRule201JSONResponse(out), nil
}

// UpdateAccountingBankRule changes a bank rule, a new version.
func (s *server) UpdateAccountingBankRule(ctx context.Context, req gen.UpdateAccountingBankRuleRequestObject) (gen.UpdateAccountingBankRuleResponseObject, error) {
	if req.Body == nil {
		return nil, invalidField("The bank rule cannot be saved", "body", "", "A body is required.")
	}
	var out gen.AccountingBankRule
	err := s.withTx(ctx, func(_ pgx.Tx, q *store.Queries) error {
		in, err := checkBankRule(ctx, q, *req.Body)
		if err != nil {
			return err
		}
		n, err := q.UpdateBankRule(ctx, store.UpdateBankRuleParams{ID: req.Id, Name: in.name, BankAccountID: in.bankAccount,
			TextContains: in.text, CounterpartyContains: in.party, Direction: in.direction, AccountID: in.account,
			TaxCodeID: in.taxCode, ProjectID: in.project, Description: in.description, Active: in.active, Now: s.now()})
		if err != nil {
			return err
		}
		if n == 0 {
			return notFound()
		}
		out, err = bankRuleOne(ctx, q, req.Id)
		return err
	})
	if err != nil {
		return nil, err
	}
	return gen.UpdateAccountingBankRule200JSONResponse(out), nil
}

// DeleteAccountingBankRule deletes a rule no match was made with.
func (s *server) DeleteAccountingBankRule(ctx context.Context, req gen.DeleteAccountingBankRuleRequestObject) (gen.DeleteAccountingBankRuleResponseObject, error) {
	err := s.withTx(ctx, func(_ pgx.Tx, q *store.Queries) error {
		if _, err := q.GetBankRule(ctx, req.Id); noRows(err) {
			return notFound()
		} else if err != nil {
			return err
		}
		used, err := q.BankRuleUsed(ctx, req.Id)
		if err != nil {
			return err
		}
		if used {
			return conflict(codeBankRuleInUse, "The bank rule was used", "A match was made with it: make it inactive instead.")
		}
		_, err = q.DeleteBankRule(ctx, req.Id)
		return err
	})
	if err != nil {
		return nil, err
	}
	return gen.DeleteAccountingBankRule204Response{}, nil
}
