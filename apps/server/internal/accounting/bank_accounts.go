package accounting

// Bank accounts (A26, A29): the company's accounts at the bank, each the
// statement of one ledger account. The seller's account in Invoices is
// offered as the first, on the bank role's account. A bank account's
// statement comes from files — camt.053 or CSV, by the column mapping saved
// on it — or from Invoices' bank feed from a start day, when the feed reads
// that account number; the feed is asked with no transaction open.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/accounting/bankstmt"
	"github.com/vantigo-io/vantigo/server/internal/accounting/gen"
	"github.com/vantigo-io/vantigo/server/internal/accounting/store"
	"github.com/vantigo-io/vantigo/server/internal/bankfeed"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// The sources of a bank account's statement.
const (
	sourceFile = "file"
	sourceFeed = "feed"
)

// bankAccountView is a bank account as the contract answers it.
func bankAccountView(r store.GetBankAccountRow) gen.AccountingBankAccount {
	out := gen.AccountingBankAccount{Id: r.ID, Name: r.Name, AccountNumber: r.AccountNumber, Iban: r.Iban, Bic: r.Bic,
		Currency: r.Currency, LedgerAccountId: r.LedgerAccountID, LedgerAccountNumber: r.LedgerNumber,
		LedgerAccountName: r.LedgerName, Source: r.Source, FeedStartDay: apiDatePtr(r.FeedStartDay), Active: r.Active,
		OpenLines: r.OpenLines, Revision: r.Revision}
	if t, err := time.Parse(time.DateOnly, r.LastStatementTo); err == nil {
		out.LastStatementTo = apiDatePtr(&t)
	}
	if r.Source == sourceFeed {
		out.Feed = &gen.AccountingBankFeedState{Status: r.FeedStatus, Detail: r.FeedDetail, LastSyncedAt: r.FeedLastSyncedAt,
			NextSyncAt: r.FeedNextSyncAt}
	}
	if len(r.CsvMapping) > 0 {
		var m gen.AccountingBankCsvMapping
		if json.Unmarshal(r.CsvMapping, &m) == nil {
			out.CsvMapping = &m
		}
	}
	return out
}

// bankAccountOne reads one bank account for an answer.
func (s *server) bankAccountOne(ctx context.Context, q *store.Queries, id int64) (gen.AccountingBankAccount, error) {
	row, err := q.GetBankAccount(ctx, id)
	if noRows(err) {
		return gen.AccountingBankAccount{}, notFound()
	}
	if err != nil {
		return gen.AccountingBankAccount{}, err
	}
	return bankAccountView(row), nil
}

// ListAccountingBankAccounts is every bank account, the seller's account
// offered while none has its number, and what the feed can do.
func (s *server) ListAccountingBankAccounts(ctx context.Context, _ gen.ListAccountingBankAccountsRequestObject) (gen.ListAccountingBankAccountsResponseObject, error) {
	q := store.New(s.deps.Pool)
	rows, err := q.ListBankAccounts(ctx)
	if err != nil {
		return nil, err
	}
	out := gen.ListAccountingBankAccounts200JSONResponse{Items: []gen.AccountingBankAccount{}}
	numbers := map[string]bool{}
	for _, r := range rows {
		out.Items = append(out.Items, bankAccountView(store.GetBankAccountRow(r)))
		numbers[r.AccountNumber] = true
	}
	if s.deps.SalesTax != nil {
		seller, err := s.deps.SalesTax.Seller(ctx)
		if err != nil {
			s.logger().WarnContext(ctx, "accounting: the seller could not be read from Invoices", "error", err.Error())
		} else if bban, ok := bankstmt.NormaliseAccount(seller.BankAccount); ok && !numbers[bban] {
			sug := &gen.AccountingBankAccountSuggestion{Name: seller.LegalName, AccountNumber: bban}
			if seller.IBAN != "" {
				sug.Iban = ptr(seller.IBAN)
			}
			if seller.BIC != "" {
				sug.Bic = ptr(seller.BIC)
			}
			if sug.Name == "" {
				sug.Name = bban
			}
			if id, ok := roleAccount(ctx, q, roleBank); ok {
				if _, err := q.BankAccountByLedger(ctx, id); noRows(err) {
					sug.LedgerAccountId = &id
				}
			}
			out.Suggested = sug
		}
	}
	out.Feed = s.bankFeedInfo(ctx)
	return out, nil
}

// roleAccount is the account holding a role.
func roleAccount(ctx context.Context, q *store.Queries, role string) (int32, bool) {
	roles, err := q.RoleAccounts(ctx)
	if err != nil {
		return 0, false
	}
	for _, r := range roles {
		if r.Role == role {
			return r.ID, true
		}
	}
	return 0, false
}

// bankFeedInfo is what the feed can do: composed, connected, refused. The
// status is read from Invoices' own tables, no call to the bank.
func (s *server) bankFeedInfo(ctx context.Context) gen.AccountingBankFeedInfo {
	out := gen.AccountingBankFeedInfo{Enabled: s.deps.BankFeed != nil}
	if s.deps.BankFeed == nil {
		return out
	}
	st, err := s.deps.BankFeed.Status(ctx)
	if err != nil {
		s.logger().WarnContext(ctx, "accounting: the bank feed's status could not be read", "error", err.Error())
		return out
	}
	out.Connected, out.Refused = st.Connected, st.Refused
	if st.Provider != "" {
		out.Provider = ptr(st.Provider)
	}
	return out
}

// bankAccountInput is a create or change body checked.
type bankAccountInput struct {
	name, number string
	iban, bic    *string
	ledger       int32
	active       bool
}

// checkBankAccount checks a body field by field (A26): the name, the number
// by mod 11 (a Norwegian IBAN read as its BBAN), NOK, and the ledger account
// an active asset account.
func checkBankAccount(ctx context.Context, q *store.Queries, b gen.AccountingBankAccountInput) (bankAccountInput, error) {
	const title = "The bank account cannot be saved"
	var f fieldErrors
	out := bankAccountInput{name: strings.TrimSpace(b.Name), ledger: b.LedgerAccountId, active: b.Active == nil || *b.Active}
	if out.name == "" || len([]rune(out.name)) > 100 {
		f.add("name", "A name of at most 100 characters.")
	}
	if bban, ok := bankstmt.NormaliseAccount(b.AccountNumber); ok {
		out.number = bban
	} else {
		f.addCoded("accountNumber", codeBankAccountNumberInvalid, "Eleven digits whose last is the mod 11 check digit.")
	}
	if b.Currency != nil && strings.TrimSpace(*b.Currency) != "" && strings.TrimSpace(*b.Currency) != currency {
		f.addCoded("currency", codeBankAccountCurrency, "NOK, the only currency of a bank account in this phase.")
	}
	if b.Iban != nil {
		if v := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(*b.Iban), " ", "")); v != "" {
			if len(v) < 15 || len(v) > 34 {
				f.add("iban", "An IBAN of 15 to 34 characters.")
			}
			out.iban = &v
		}
	}
	if b.Bic != nil {
		if v := strings.ToUpper(strings.TrimSpace(*b.Bic)); v != "" {
			if len(v) != 8 && len(v) != 11 {
				f.add("bic", "A BIC of 8 or 11 characters.")
			}
			out.bic = &v
		}
	}
	accounts, err := q.AccountsForPosting(ctx, []int32{b.LedgerAccountId})
	if err != nil {
		return out, err
	}
	if len(accounts) != 1 || accounts[0].Kind != "asset" || !accounts[0].Active {
		f.addCoded("ledgerAccountId", codeBankAccountInvalid, "An active asset account.")
	}
	return out, f.refusal(title)
}

// bankAccountConflict is a unique index's violation as its refusal.
func bankAccountConflict(err error) error {
	switch {
	case violates(err, "ux_bank_accounts_number"):
		return conflict(codeBankAccountNumberTaken, "Another bank account has that number", "")
	case violates(err, "ux_bank_accounts_ledger"):
		return conflict(codeBankAccountLedgerTaken, "Another bank account has that ledger account",
			"A ledger account is the statement of one bank account.")
	}
	return err
}

// CreateAccountingBankAccount adds a bank account.
func (s *server) CreateAccountingBankAccount(ctx context.Context, req gen.CreateAccountingBankAccountRequestObject) (gen.CreateAccountingBankAccountResponseObject, error) {
	if req.Body == nil {
		return nil, invalidField("The bank account cannot be saved", "body", "", "A body is required.")
	}
	var out gen.AccountingBankAccount
	err := s.withTx(ctx, func(tx pgx.Tx, q *store.Queries) error {
		in, err := checkBankAccount(ctx, q, *req.Body)
		if err != nil {
			return err
		}
		sp, err := tx.Begin(ctx)
		if err != nil {
			return err
		}
		id, err := store.New(sp).InsertBankAccount(ctx, store.InsertBankAccountParams{Name: in.name, AccountNumber: in.number,
			Iban: in.iban, Bic: in.bic, LedgerAccountID: in.ledger, Active: in.active, Now: s.now()})
		if err != nil {
			_ = sp.Rollback(ctx)
			return bankAccountConflict(err)
		}
		if err := sp.Commit(ctx); err != nil {
			return err
		}
		out, err = s.bankAccountOne(ctx, q, id)
		return err
	})
	if err != nil {
		return nil, err
	}
	return gen.CreateAccountingBankAccount201JSONResponse(out), nil
}

// GetAccountingBankAccount is one bank account.
func (s *server) GetAccountingBankAccount(ctx context.Context, req gen.GetAccountingBankAccountRequestObject) (gen.GetAccountingBankAccountResponseObject, error) {
	out, err := s.bankAccountOne(ctx, store.New(s.deps.Pool), req.Id)
	if err != nil {
		return nil, err
	}
	return gen.GetAccountingBankAccount200JSONResponse(out), nil
}

// UpdateAccountingBankAccount changes a bank account at the revision read.
func (s *server) UpdateAccountingBankAccount(ctx context.Context, req gen.UpdateAccountingBankAccountRequestObject) (gen.UpdateAccountingBankAccountResponseObject, error) {
	if req.Body == nil {
		return nil, invalidField("The bank account cannot be saved", "body", "", "A body is required.")
	}
	if req.Body.Revision == nil {
		return nil, invalidField("The bank account cannot be saved", "revision", "", "The revision read is required.")
	}
	var out gen.AccountingBankAccount
	err := s.withTx(ctx, func(tx pgx.Tx, q *store.Queries) error {
		if _, err := q.LockBankAccount(ctx, req.Id); noRows(err) {
			return notFound()
		} else if err != nil {
			return err
		}
		in, err := checkBankAccount(ctx, q, *req.Body)
		if err != nil {
			return err
		}
		sp, err := tx.Begin(ctx)
		if err != nil {
			return err
		}
		_, err = store.New(sp).UpdateBankAccount(ctx, store.UpdateBankAccountParams{ID: req.Id, Revision: *req.Body.Revision,
			Name: in.name, AccountNumber: in.number, Iban: in.iban, Bic: in.bic, LedgerAccountID: in.ledger, Active: in.active,
			Now: s.now()})
		if err != nil {
			_ = sp.Rollback(ctx)
			if noRows(err) {
				return conflict(codeRevisionConflict, "The bank account was changed meanwhile", "Read it again.")
			}
			return bankAccountConflict(err)
		}
		if err := sp.Commit(ctx); err != nil {
			return err
		}
		out, err = s.bankAccountOne(ctx, q, req.Id)
		return err
	})
	if err != nil {
		return nil, err
	}
	return gen.UpdateAccountingBankAccount200JSONResponse(out), nil
}

// DeleteAccountingBankAccount deletes a bank account with no statement.
func (s *server) DeleteAccountingBankAccount(ctx context.Context, req gen.DeleteAccountingBankAccountRequestObject) (gen.DeleteAccountingBankAccountResponseObject, error) {
	err := s.withTx(ctx, func(_ pgx.Tx, q *store.Queries) error {
		if _, err := q.LockBankAccount(ctx, req.Id); noRows(err) {
			return notFound()
		} else if err != nil {
			return err
		}
		used, err := q.BankAccountHasStatements(ctx, req.Id)
		if err != nil {
			return err
		}
		if used {
			return conflict(codeBankAccountHasStatements, "The bank account has statements",
				"Its statements are part of the books: make it inactive instead.")
		}
		if used, err := q.BankRulesOfAccount(ctx, &req.Id); err != nil {
			return err
		} else if used {
			return conflict(codeBankAccountHasStatements, "The bank account has bank rules",
				"Delete its bank rules first, or make the account inactive.")
		}
		_, err = q.DeleteBankAccount(ctx, req.Id)
		return err
	})
	if err != nil {
		return nil, err
	}
	return gen.DeleteAccountingBankAccount204Response{}, nil
}

// feedRefusal is a BankFeed call's error as the refusal the screen shows.
func feedRefusal(err error) error {
	var limited *bankfeed.ErrRateLimited
	switch {
	case errors.Is(err, contracts.ErrBankFeedNotConnected):
		return conflict(codeBankFeedNotConnected, "No bank is connected",
			"Connect the bank in Invoices, under Payments → Bank integration, first.")
	case errors.Is(err, bankfeed.ErrUnauthorized):
		return conflict(codeBankFeedRefused, "The bank refused the key",
			"The bank refused the API key Invoices holds. Store a new key in Invoices' Bank integration.")
	case errors.As(err, &limited):
		return refuse(http.StatusServiceUnavailable, codeBankFeedUnreachable, "The bank asks for a later read",
			"The bank is limiting requests; try again in a little while.")
	}
	return refuse(http.StatusServiceUnavailable, codeBankFeedUnreachable, "The bank cannot be reached",
		"The bank could not be reached, or did not answer as it should. Nothing was changed; try again.")
}

// feedAccounts is the accounts the feed reads, from the bank, with no
// transaction open; its error is the refusal to answer.
func (s *server) feedAccounts(ctx context.Context) ([]bankfeed.Account, error) {
	if s.deps.BankFeed == nil {
		return nil, conflict(codeBankFeedOff, "The bank feed is switched off",
			"The bank feed needs Invoices, and INVOICES_BANK_FEED_ENABLED on this installation.")
	}
	st, err := s.deps.BankFeed.Status(ctx)
	if err != nil {
		return nil, err
	}
	switch {
	case !st.Connected:
		return nil, feedRefusal(contracts.ErrBankFeedNotConnected)
	case st.Refused:
		return nil, feedRefusal(bankfeed.ErrUnauthorized)
	}
	accounts, err := s.deps.BankFeed.Accounts(ctx)
	if err != nil {
		s.logger().WarnContext(ctx, "accounting: the bank feed's accounts could not be read", "error", err.Error())
		return nil, feedRefusal(err)
	}
	return accounts, nil
}

// feedNumber is an account the bank answers as its 11 digits, or as written
// when it does not read as a Norwegian account number.
func feedNumber(a bankfeed.Account) string {
	if bban, ok := bankstmt.NormaliseAccount(a.Number); ok {
		return bban
	}
	return a.Number
}

// ListAccountingBankFeedAccounts is the accounts the feed reads, every kind.
func (s *server) ListAccountingBankFeedAccounts(ctx context.Context, _ gen.ListAccountingBankFeedAccountsRequestObject) (gen.ListAccountingBankFeedAccountsResponseObject, error) {
	accounts, err := s.feedAccounts(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := store.New(s.deps.Pool).ListBankAccounts(ctx)
	if err != nil {
		return nil, err
	}
	byNumber := map[string]int64{}
	for _, r := range rows {
		byNumber[r.AccountNumber] = r.ID
	}
	out := gen.ListAccountingBankFeedAccounts200JSONResponse{Items: []gen.AccountingBankFeedAccount{}}
	for _, a := range accounts {
		n := feedNumber(a)
		item := gen.AccountingBankFeedAccount{Number: n, Name: a.Name, Kind: string(a.Kind)}
		if id, ok := byNumber[n]; ok {
			item.BankAccountId = &id
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}

// SetAccountingBankAccountSource switches the statement's source (A29): to
// the feed when the bank reads the account's number, from a start day, due
// at once; back to files, the feed's state cleared and its lines kept.
func (s *server) SetAccountingBankAccountSource(ctx context.Context, req gen.SetAccountingBankAccountSourceRequestObject) (gen.SetAccountingBankAccountSourceResponseObject, error) {
	const title = "The source cannot be switched"
	if req.Body == nil {
		return nil, invalidField(title, "body", "", "A body is required.")
	}
	b := *req.Body
	if b.Source != sourceFile && b.Source != sourceFeed {
		return nil, invalidField(title, "source", "", "file or feed.")
	}
	q := store.New(s.deps.Pool)
	current, err := q.GetBankAccount(ctx, req.Id)
	if noRows(err) {
		return nil, notFound()
	}
	if err != nil {
		return nil, err
	}
	var start *time.Time
	if b.Source == sourceFeed {
		today, err := s.today(ctx)
		if err != nil {
			return nil, err
		}
		start = &today
		if b.StartDay != nil {
			start = ptr(day(*b.StartDay))
		}
		if start.After(today) || start.Before(today.AddDate(-1, -1, 0)) {
			return nil, invalidField(title, "startDay", "", "A day from 13 months ago to today.")
		}
		// The bank is asked before any transaction opens (A29).
		accounts, err := s.feedAccounts(ctx)
		if err != nil {
			return nil, err
		}
		offered := false
		for _, a := range accounts {
			if feedNumber(a) == current.AccountNumber {
				offered = true
			}
		}
		if !offered {
			return nil, conflict(codeBankFeedNotOffered, "The bank feed does not read this account",
				"The bank connected in Invoices does not offer account "+current.AccountNumber+".")
		}
	}
	var out gen.AccountingBankAccount
	err = s.withTx(ctx, func(_ pgx.Tx, q *store.Queries) error {
		if _, err := q.LockBankAccount(ctx, req.Id); noRows(err) {
			return notFound()
		} else if err != nil {
			return err
		}
		p := store.SetBankAccountSourceParams{ID: req.Id, Source: b.Source, Now: s.now()}
		if b.Source == sourceFeed {
			p.FeedStartDay, p.FeedNextSyncAt = start, ptr(s.now())
		}
		if err := q.SetBankAccountSource(ctx, p); err != nil {
			return err
		}
		out, err = s.bankAccountOne(ctx, q, req.Id)
		return err
	})
	if err != nil {
		return nil, err
	}
	return gen.SetAccountingBankAccountSource200JSONResponse(out), nil
}

// csvMapping is a contract mapping as the reader's.
func csvMapping(m gen.AccountingBankCsvMapping) bankstmt.Mapping {
	col := func(p *int32) *int {
		if p == nil {
			return nil
		}
		v := int(*p)
		return &v
	}
	delim, _ := firstRune(m.Delimiter)
	if m.Delimiter == `\t` || strings.EqualFold(m.Delimiter, "tab") {
		delim = '\t'
	}
	c := m.Columns
	return bankstmt.Mapping{Delimiter: delim, Encoding: m.Encoding, HeaderRows: int(m.HeaderRows), DateFormat: m.DateFormat,
		DecimalSeparator: m.DecimalSeparator, ThousandsSeparator: m.ThousandsSeparator, InvertSign: m.InvertSign,
		Columns: bankstmt.Columns{Date: int(c.Date), ValueDate: col(c.ValueDate), Amount: col(c.Amount), Debit: col(c.Debit),
			Credit: col(c.Credit), Text: col(c.Text), Reference: col(c.Reference), KID: col(c.Kid),
			CounterpartyName: col(c.CounterpartyName), CounterpartyAccount: col(c.CounterpartyAccount), BankID: col(c.BankId)}}
}

func firstRune(s string) (rune, bool) {
	for _, r := range s {
		return r, true
	}
	return 0, false
}

// mappingRefusal is a mapping's own rule as its 400.
func mappingRefusal(err error) error {
	var inv *bankstmt.InvalidError
	if errors.As(err, &inv) {
		return invalidField("The CSV mapping cannot be used", "mapping", codeBankCsvMappingInvalid, upperFirst(inv.Reason)+".")
	}
	return err
}

// upperFirst is s with its first letter a capital.
func upperFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	return strings.ToUpper(string(r[0])) + string(r[1:])
}

// PutAccountingBankCsvMapping saves the account's CSV mapping.
func (s *server) PutAccountingBankCsvMapping(ctx context.Context, req gen.PutAccountingBankCsvMappingRequestObject) (gen.PutAccountingBankCsvMappingResponseObject, error) {
	if req.Body == nil {
		return nil, invalidField("The CSV mapping cannot be saved", "body", "", "A body is required.")
	}
	if err := csvMapping(*req.Body).Check(); err != nil {
		return nil, mappingRefusal(err)
	}
	data, err := json.Marshal(req.Body)
	if err != nil {
		return nil, err
	}
	var out gen.AccountingBankAccount
	err = s.withTx(ctx, func(_ pgx.Tx, q *store.Queries) error {
		if _, err := q.LockBankAccount(ctx, req.Id); noRows(err) {
			return notFound()
		} else if err != nil {
			return err
		}
		if err := q.SetBankAccountMapping(ctx, store.SetBankAccountMappingParams{ID: req.Id, CsvMapping: data, Now: s.now()}); err != nil {
			return err
		}
		out, err = s.bankAccountOne(ctx, q, req.Id)
		return err
	})
	if err != nil {
		return nil, err
	}
	return gen.PutAccountingBankCsvMapping200JSONResponse(out), nil
}

// statementUpload is an import's or a preview's multipart body: the file and
// the mapping field, when sent.
type statementUpload struct {
	name    string
	data    []byte
	mapping string
}

// readStatementUpload reads the body's file, at most the statement's bound,
// and its mapping field.
func readStatementUpload(mr *multipart.Reader) (statementUpload, error) {
	const title = "The file cannot be read"
	if mr == nil {
		return statementUpload{}, invalidField(title, attachmentFormField, "", "Send one file in the multipart field file.")
	}
	var out statementUpload
	files := 0
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				return statementUpload{}, statementTooLarge()
			}
			return statementUpload{}, invalidField(title, attachmentFormField, "", "The body is not a readable multipart form.")
		}
		switch part.FormName() {
		case attachmentFormField:
			files++
			data, err := io.ReadAll(io.LimitReader(part, bankstmt.DefaultLimits.Bytes+1))
			_ = part.Close()
			if err != nil {
				var tooLarge *http.MaxBytesError
				if errors.As(err, &tooLarge) {
					return statementUpload{}, statementTooLarge()
				}
				return statementUpload{}, invalidField(title, attachmentFormField, "", "The file could not be read.")
			}
			if int64(len(data)) > bankstmt.DefaultLimits.Bytes {
				return statementUpload{}, statementTooLarge()
			}
			out.name, out.data = part.FileName(), data
		case "mapping":
			data, err := io.ReadAll(io.LimitReader(part, 64<<10))
			_ = part.Close()
			if err != nil {
				return statementUpload{}, invalidField(title, "mapping", "", "The mapping could not be read.")
			}
			out.mapping = string(data)
		default:
			_ = part.Close()
		}
	}
	if files != 1 || len(out.data) == 0 {
		return statementUpload{}, invalidField(title, attachmentFormField, "", "Send one file, not empty, in the multipart field file.")
	}
	return out, nil
}

func statementTooLarge() error {
	return invalidField("The file cannot be read", attachmentFormField, codeBankStatementTooLarge,
		"A statement file is at most 20 MB and 100 000 lines.")
}

// statementRefusal is a reader's error as its 400.
func statementRefusal(err error) error {
	var inv *bankstmt.InvalidError
	switch {
	case errors.Is(err, bankstmt.ErrTooLarge):
		return statementTooLarge()
	case errors.As(err, &inv):
		return invalidField("The file cannot be imported", attachmentFormField, codeBankStatementInvalid,
			"This is not a statement Vantigo can read: "+inv.Reason+".")
	}
	return err
}

// accountMapping is the mapping a CSV file of the account is read with: the
// one sent, else the one saved.
func accountMapping(row store.GetBankAccountRow, sent string) (bankstmt.Mapping, error) {
	raw := []byte(sent)
	if strings.TrimSpace(sent) == "" {
		raw = row.CsvMapping
	}
	if len(raw) == 0 {
		return bankstmt.Mapping{}, conflict(codeBankCsvMappingRequired, "The bank account has no CSV mapping",
			"Save how the bank's CSV file is read on the bank account first.")
	}
	var m gen.AccountingBankCsvMapping
	if err := json.Unmarshal(raw, &m); err != nil {
		return bankstmt.Mapping{}, invalidField("The CSV mapping cannot be used", "mapping", codeBankCsvMappingInvalid,
			"The mapping is not readable JSON.")
	}
	out := csvMapping(m)
	if err := out.Check(); err != nil {
		return bankstmt.Mapping{}, mappingRefusal(err)
	}
	return out, nil
}

// maxPreviewRows is how many rows a preview answers.
const maxPreviewRows = 20

// PreviewAccountingBankCsv reads a CSV file as it would be imported.
func (s *server) PreviewAccountingBankCsv(ctx context.Context, req gen.PreviewAccountingBankCsvRequestObject) (gen.PreviewAccountingBankCsvResponseObject, error) {
	up, err := readStatementUpload(req.Body)
	if err != nil {
		return nil, err
	}
	row, err := store.New(s.deps.Pool).GetBankAccount(ctx, req.Id)
	if noRows(err) {
		return nil, notFound()
	}
	if err != nil {
		return nil, err
	}
	m, err := accountMapping(row, up.mapping)
	if err != nil {
		return nil, err
	}
	rows, err := bankstmt.ReadCSV(up.data, m, bankstmt.DefaultLimits)
	if err != nil {
		return nil, statementRefusal(err)
	}
	out := gen.PreviewAccountingBankCsv200JSONResponse{Rows: []gen.AccountingBankCsvPreviewRow{}, Total: int32(len(rows))}
	for _, r := range rows {
		if r.Err != nil {
			out.Errors++
		}
		if len(out.Rows) >= maxPreviewRows {
			continue
		}
		pr := gen.AccountingBankCsvPreviewRow{Row: int32(r.N)}
		if r.Err != nil {
			pr.Error = ptr(r.Err.Error())
		} else {
			pr.BookedOn, pr.Amount = apiDatePtr(&r.Line.BookedOn), ptr(money(r.Line.Amount))
			pr.Text, pr.Reference, pr.CounterpartyName = r.Line.Text, r.Line.Reference, r.Line.CounterpartyName
			if r.Line.KID != "" {
				pr.Kid = ptr(r.Line.KID)
			}
		}
		out.Rows = append(out.Rows, pr)
	}
	return out, nil
}
