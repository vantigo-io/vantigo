package accounting

// The chart of accounts (A3): accounts with their rules, their grouping codes
// per year, and the system roles.

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vantigo-io/vantigo/server/internal/accounting/gen"
	"github.com/vantigo-io/vantigo/server/internal/accounting/store"
)

var accountNumberPattern = regexp.MustCompile(`^[0-9]{4,6}$`)

var accountKinds = []string{"asset", "liability", "equity", "income", "expense"}

// accountView is an account row with its groupings.
func accountView(a store.GetAccountRow, groupings []store.AccountingAccountGrouping) gen.AccountingAccount {
	out := gen.AccountingAccount{Id: a.ID, Number: a.Number, Name: a.Name, Kind: gen.AccountingAccountKind(a.Kind),
		DefaultTaxCodeId: a.DefaultTaxCodeID, Active: a.Active, HasPostings: a.HasPostings, Revision: a.Revision,
		Groupings: []gen.AccountingGrouping{}}
	if a.SystemRole != nil {
		out.SystemRole = ptr(gen.AccountingSystemRole(*a.SystemRole))
	}
	for _, g := range groupings {
		if g.AccountID == a.ID {
			out.Groupings = append(out.Groupings, gen.AccountingGrouping{FromYear: g.FromYear, Category: g.Category, Code: g.Code})
		}
	}
	return out
}

func (s *server) accountList(ctx context.Context, q *store.Queries, active *bool, search *string) ([]gen.AccountingAccount, error) {
	rows, err := q.ListAccounts(ctx, store.ListAccountsParams{Active: active, Q: search})
	if err != nil {
		return nil, err
	}
	groupings, err := q.ListGroupings(ctx, nil)
	if err != nil {
		return nil, err
	}
	out := make([]gen.AccountingAccount, 0, len(rows))
	for _, r := range rows {
		out = append(out, accountView(store.GetAccountRow(r), groupings))
	}
	return out, nil
}

// ListAccountingAccounts is the chart by number.
func (s *server) ListAccountingAccounts(ctx context.Context, req gen.ListAccountingAccountsRequestObject) (gen.ListAccountingAccountsResponseObject, error) {
	var search *string
	if req.Params.Q != nil && strings.TrimSpace(*req.Params.Q) != "" {
		search = ptr(strings.TrimSpace(*req.Params.Q))
	}
	items, err := s.accountList(ctx, store.New(s.deps.Pool), req.Params.Active, search)
	if err != nil {
		return nil, err
	}
	return gen.ListAccountingAccounts200JSONResponse{Items: items}, nil
}

func (s *server) account(ctx context.Context, q *store.Queries, id int32) (gen.AccountingAccount, error) {
	a, err := q.GetAccount(ctx, id)
	if noRows(err) {
		return gen.AccountingAccount{}, notFound()
	}
	if err != nil {
		return gen.AccountingAccount{}, err
	}
	groupings, err := q.ListGroupings(ctx, &id)
	if err != nil {
		return gen.AccountingAccount{}, err
	}
	return accountView(a, groupings), nil
}

// GetAccountingAccount is one account.
func (s *server) GetAccountingAccount(ctx context.Context, req gen.GetAccountingAccountRequestObject) (gen.GetAccountingAccountResponseObject, error) {
	a, err := s.account(ctx, store.New(s.deps.Pool), req.Id)
	if err != nil {
		return nil, err
	}
	return gen.GetAccountingAccount200JSONResponse(a), nil
}

// checkAccountFields holds an account's number, name and kind to A3: the
// kind is the one its number's class gives (kindAllowed), so every account
// lands on its side of the statements.
func checkAccountFields(f *fieldErrors, number, name, kind string) {
	if !accountNumberPattern.MatchString(number) {
		f.add("number", "4 to 6 digits.")
	}
	if strings.TrimSpace(name) == "" || len([]rune(name)) > 200 {
		f.add("name", "A name of at most 200 characters.")
	}
	switch {
	case !slices.Contains(accountKinds, kind):
		f.add("kind", "asset, liability, equity, income or expense.")
	case accountNumberPattern.MatchString(number) && (number[0] == '0' || number[0] == '9'):
		f.addCoded("number", codeAccountKindMismatch, "The chart's classes are 1 to 8.")
	case accountNumberPattern.MatchString(number) && !kindAllowed(number, kind):
		f.addCoded("kind", codeAccountKindMismatch, "Account "+number+"'s class makes it "+allowedKinds(number)+".")
	}
}

// kindAllowed reports whether kind is one number's class allows (A3): class
// 1 asset; 20 equity and 21–29 liability; 3 income; 4–7 expense; 8 income or
// expense.
func kindAllowed(number, kind string) bool {
	if number[0] == '8' {
		return kind == "income" || kind == "expense"
	}
	return kind == kindOf(number)
}

// allowedKinds is the kinds number's class allows, as a refusal says them.
func allowedKinds(number string) string {
	if number[0] == '8' {
		return "income or expense"
	}
	return kindOf(number)
}

// roleKinds is the kind of account each system role may sit on (A3): a role
// posts as its kind does, so it never moves to the other side.
var roleKinds = map[string]string{
	"bank": "asset", "receivables": "asset", "clearing_vipps": "asset",
	"payables": "liability", "vat_settlement": "liability", "employee_payables": "liability",
	"opening": "equity", "year_result": "equity",
	"charge_income": "income", "currency_gain": "income",
	"rounding": "expense", "bad_debt": "expense", "currency_loss": "expense", "payment_fees": "expense",
}

// numberTaken reports whether err is the unique number's violation.
func numberTaken(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "ux_accounts_number"
}

// unknownForeignKey reports whether err is a foreign key violation: an id the
// request named that does not exist.
func unknownForeignKey(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}

// CreateAccountingAccount adds an account to the chart.
func (s *server) CreateAccountingAccount(ctx context.Context, req gen.CreateAccountingAccountRequestObject) (gen.CreateAccountingAccountResponseObject, error) {
	if req.Body == nil {
		return nil, invalidField("The account cannot be created", "body", "", "A body is required.")
	}
	b := *req.Body
	var f fieldErrors
	checkAccountFields(&f, b.Number, b.Name, string(b.Kind))
	if b.Grouping != nil && !validGrouping(int(b.Grouping.FromYear), b.Grouping.Category, b.Grouping.Code) {
		f.add("grouping.code", "Not a code of Skatteetaten's list for that year.")
	}
	if err := f.refusal("The account cannot be created"); err != nil {
		return nil, err
	}
	var out gen.AccountingAccount
	err := s.withTx(ctx, func(_ pgx.Tx, q *store.Queries) error {
		id, err := q.InsertAccount(ctx, store.InsertAccountParams{Number: b.Number, Name: strings.TrimSpace(b.Name),
			Kind: string(b.Kind), DefaultTaxCodeID: b.DefaultTaxCodeId, Now: s.now()})
		switch {
		case numberTaken(err):
			return conflict(codeAccountNumberTaken, "The number is taken", "Account "+b.Number+" exists.")
		case unknownForeignKey(err):
			return invalidField("The account cannot be created", "defaultTaxCodeId", codeTaxCodeUnknown, "No such tax code.")
		case err != nil:
			return err
		}
		if b.Grouping != nil {
			if err := q.InsertGrouping(ctx, store.InsertGroupingParams{AccountID: id, FromYear: b.Grouping.FromYear,
				Category: b.Grouping.Category, Code: b.Grouping.Code}); err != nil {
				return err
			}
		}
		out, err = s.account(ctx, q, id)
		return err
	})
	if err != nil {
		return nil, err
	}
	return gen.CreateAccountingAccount201JSONResponse(out), nil
}

// UpdateAccountingAccount changes an account: its name always, its number and
// kind only before its first posting (A3).
func (s *server) UpdateAccountingAccount(ctx context.Context, req gen.UpdateAccountingAccountRequestObject) (gen.UpdateAccountingAccountResponseObject, error) {
	if req.Body == nil {
		return nil, invalidField("The account cannot be changed", "body", "", "A body is required.")
	}
	b := *req.Body
	var f fieldErrors
	checkAccountFields(&f, b.Number, b.Name, string(b.Kind))
	if err := f.refusal("The account cannot be changed"); err != nil {
		return nil, err
	}
	var out gen.AccountingAccount
	err := s.withTx(ctx, func(_ pgx.Tx, q *store.Queries) error {
		cur, err := q.LockAccount(ctx, req.Id)
		if noRows(err) {
			return notFound()
		}
		if err != nil {
			return err
		}
		if cur.Revision != b.Revision {
			return conflict(codeRevisionConflict, "The account has changed", "Reload it and try again.")
		}
		if cur.SystemRole != nil && !b.Active {
			return conflict(codeAccountHasRole, "The account holds a system role",
				"Move the role "+*cur.SystemRole+" to another account before making this one inactive.")
		}
		a, err := q.GetAccount(ctx, req.Id)
		if err != nil {
			return err
		}
		if a.HasPostings && (a.Number != b.Number || a.Kind != string(b.Kind)) {
			return conflict(codeAccountHasPostings, "The account has postings", "Its number and kind are fixed; its name may change.")
		}
		err = q.UpdateAccount(ctx, store.UpdateAccountParams{ID: req.Id, Number: b.Number, Name: strings.TrimSpace(b.Name),
			Kind: string(b.Kind), DefaultTaxCodeID: b.DefaultTaxCodeId, Active: b.Active, Now: s.now()})
		switch {
		case numberTaken(err):
			return conflict(codeAccountNumberTaken, "The number is taken", "Account "+b.Number+" exists.")
		case unknownForeignKey(err):
			return invalidField("The account cannot be changed", "defaultTaxCodeId", codeTaxCodeUnknown, "No such tax code.")
		case err != nil:
			return err
		}
		out, err = s.account(ctx, q, req.Id)
		return err
	})
	if err != nil {
		return nil, err
	}
	return gen.UpdateAccountingAccount200JSONResponse(out), nil
}

// DeleteAccountingAccount deletes an account no posting names and no role
// holds (A3); one with postings is only made inactive.
func (s *server) DeleteAccountingAccount(ctx context.Context, req gen.DeleteAccountingAccountRequestObject) (gen.DeleteAccountingAccountResponseObject, error) {
	err := s.withTx(ctx, func(_ pgx.Tx, q *store.Queries) error {
		cur, err := q.LockAccount(ctx, req.Id)
		if noRows(err) {
			return notFound()
		}
		if err != nil {
			return err
		}
		if cur.SystemRole != nil {
			return conflict(codeAccountHasRole, "The account holds a system role", "Move the role "+*cur.SystemRole+" first.")
		}
		a, err := q.GetAccount(ctx, req.Id)
		if err != nil {
			return err
		}
		if a.HasPostings {
			return conflict(codeAccountHasPostings, "The account has postings", "Make it inactive instead.")
		}
		err = q.DeleteAccount(ctx, req.Id)
		if unknownForeignKey(err) {
			return conflict(codeAccountHasPostings, "The account is in use", "A tax code posts to it. Make it inactive instead.")
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return gen.DeleteAccountingAccount204Response{}, nil
}

// PutAccountingAccountGroupings replaces an account's groupings, one per year,
// each a pair of that year's list.
func (s *server) PutAccountingAccountGroupings(ctx context.Context, req gen.PutAccountingAccountGroupingsRequestObject) (gen.PutAccountingAccountGroupingsResponseObject, error) {
	if req.Body == nil {
		return nil, invalidField("The groupings cannot be saved", "body", "", "A body is required.")
	}
	var f fieldErrors
	seen := map[int32]bool{}
	for i, g := range req.Body.Groupings {
		field := "groupings[" + itoa(i) + "]"
		if seen[g.FromYear] {
			f.add(field+".fromYear", "One grouping per year.")
		}
		seen[g.FromYear] = true
		if !validGrouping(int(g.FromYear), g.Category, g.Code) {
			f.add(field+".code", "Not a code of Skatteetaten's list for that year.")
		}
	}
	if err := f.refusal("The groupings cannot be saved"); err != nil {
		return nil, err
	}
	var out gen.AccountingAccount
	err := s.withTx(ctx, func(_ pgx.Tx, q *store.Queries) error {
		if _, err := q.LockAccount(ctx, req.Id); noRows(err) {
			return notFound()
		} else if err != nil {
			return err
		}
		if err := q.DeleteGroupings(ctx, req.Id); err != nil {
			return err
		}
		for _, g := range req.Body.Groupings {
			if err := q.InsertGrouping(ctx, store.InsertGroupingParams{AccountID: req.Id, FromYear: g.FromYear,
				Category: g.Category, Code: g.Code}); err != nil {
				return err
			}
		}
		var err error
		out, err = s.account(ctx, q, req.Id)
		return err
	})
	if err != nil {
		return nil, err
	}
	return gen.PutAccountingAccountGroupings200JSONResponse(out), nil
}

// AssignAccountingRole moves a system role to an active account (A3): the
// role leaves the account that held it, and is never removed.
func (s *server) AssignAccountingRole(ctx context.Context, req gen.AssignAccountingRoleRequestObject) (gen.AssignAccountingRoleResponseObject, error) {
	if req.Body == nil {
		return nil, invalidField("The role cannot be moved", "body", "", "A body is required.")
	}
	role := string(req.Role)
	if !slices.Contains(systemRoles, role) {
		return nil, invalidField("The role cannot be moved", "role", "", "No such role.")
	}
	var items []gen.AccountingAccount
	err := s.withTx(ctx, func(_ pgx.Tx, q *store.Queries) error {
		a, err := q.GetAccount(ctx, req.Body.AccountId)
		if noRows(err) {
			return invalidField("The role cannot be moved", "accountId", codeAccountUnknown, "No such account.")
		}
		if err != nil {
			return err
		}
		if !a.Active {
			return invalidField("The role cannot be moved", "accountId", codeAccountInactive, "The account is inactive.")
		}
		if a.SystemRole != nil && *a.SystemRole != role {
			return invalidField("The role cannot be moved", "accountId", codeAccountHasRole,
				"The account holds the role "+*a.SystemRole+"; an account holds one role.")
		}
		if want := roleKinds[role]; a.Kind != want {
			return invalidField("The role cannot be moved", "accountId", codeAccountKindMismatch,
				"The role "+role+" sits on an account of kind "+want+".")
		}
		// The control accounts' open items are the postings on them: moving
		// receivables or payables off an account with postings would leave
		// the party ledgers on an account the module no longer reads.
		if role == roleReceivables || role == rolePayables {
			holders, err := q.RoleAccounts(ctx)
			if err != nil {
				return err
			}
			for _, h := range holders {
				if h.Role != role || h.ID == a.ID {
					continue
				}
				holder, err := q.GetAccount(ctx, h.ID)
				if err != nil {
					return err
				}
				if holder.HasPostings {
					return conflict(codeAccountHasPostings, "The role's account has postings",
						"Account "+holder.Number+" has postings on "+role+"; the role stays on it.")
				}
			}
		}
		if err := q.ClearRole(ctx, store.ClearRoleParams{Role: &role, Now: s.now()}); err != nil {
			return err
		}
		if err := q.SetRole(ctx, store.SetRoleParams{ID: a.ID, Role: &role, Now: s.now()}); err != nil {
			return err
		}
		items, err = s.accountList(ctx, q, nil, nil)
		return err
	})
	if err != nil {
		return nil, err
	}
	return gen.AssignAccountingRole200JSONResponse{Items: items}, nil
}

// ListAccountingGroupingCodes is the year's list for the picker (R13).
func (s *server) ListAccountingGroupingCodes(ctx context.Context, req gen.ListAccountingGroupingCodesRequestObject) (gen.ListAccountingGroupingCodesResponseObject, error) {
	year := s.now().Year()
	if req.Params.Year != nil {
		year = int(*req.Params.Year)
	}
	list, err := groupingsFor(year)
	if err != nil {
		return nil, err
	}
	search := ""
	if req.Params.Q != nil {
		search = strings.ToLower(strings.TrimSpace(*req.Params.Q))
	}
	items := []gen.AccountingGroupingCode{}
	for _, c := range list.codes {
		if search != "" && !strings.HasPrefix(c.Code, search) && !strings.Contains(strings.ToLower(c.NameNb), search) &&
			!strings.Contains(strings.ToLower(c.NameEn), search) {
			continue
		}
		items = append(items, gen.AccountingGroupingCode{Category: c.Category, Code: c.Code, NameNb: c.NameNb, NameEn: c.NameEn})
	}
	return gen.ListAccountingGroupingCodes200JSONResponse{Year: int32(list.year), Items: items}, nil
}
