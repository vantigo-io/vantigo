package invoices

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/bankfeed"
	"github.com/vantigo-io/vantigo/server/internal/holidays"
	"github.com/vantigo-io/vantigo/server/internal/invoices/bankfile"
	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is the bank feed's connection and accounts (invoices bank feed
// design F3, F4, F8): one row of its own, invoices.bank_feed_connections, the
// payment provider's shape (vipps_settings.go), and the accounts a person
// switched on. The API key is sealed by the secrets box under its own
// purpose and is never answered, logged or sent anywhere but to the bank;
// only its last four characters are kept in clear. Every call to the bank is
// made outside any transaction, through bankFeed or bankFeedWith
// (contractscalls.go). The sync that reads the accounts is
// bank_feed_sync.go's, its worker bank_feed_worker.go's.

const (
	// bankFeedCredentialPurpose is the secrets box purpose the API key is
	// sealed under.
	bankFeedCredentialPurpose = "invoices/bank-feed-credential"
	// providerFolio is the one bank.
	providerFolio = "folio"
	// maxBankFeedKeyLength bounds the key a PUT takes, in characters;
	// minBankFeedKeyLength keeps the four characters shown in clear a small
	// part of it.
	maxBankFeedKeyLength = 1000
	minBankFeedKeyLength = 12

	codeBankFeedDisabled           = "bank_feed_disabled"
	codeBankFeedNotConnected       = "bank_feed_not_connected"
	codeBankFeedCredentialRejected = "bank_feed_credential_rejected"
	codeBankFeedUnavailable        = "bank_feed_unavailable"
	codeBankFeedAccountNotOffered  = "bank_feed_account_not_offered"
	codeBankFeedEnabled            = "bank_feed_enabled"
	codeBankFeedKeyChanged         = "bank_feed_key_changed"

	invalidBankFeedTitle           = "Invalid bank feed request"
	bankFeedDisabledTitle          = "The bank feed is switched off"
	bankFeedNotConnectedTitle      = "No bank is connected"
	bankFeedRejectedTitle          = "The bank refused the API key"
	bankFeedUnavailableTitle       = "The bank cannot be reached"
	bankFeedAccountNotOfferedTitle = "The bank does not offer this account"
	bankFeedEnabledTitle           = "The bank feed reads this account"
	bankFeedKeyChangedTitle        = "The bank key changed meanwhile"

	// maxStartDayMonths is how far back a start day may lie: a little over a
	// year, which a first read can cover without reading years of a bank's
	// history.
	maxStartDayMonths = 13

	// The reasons an account the bank answers cannot be switched on.
	whyNotCard           = "card"
	whyNotSellersAccount = "not_sellers_account"
	bankFeedFormatFeed   = string(bankfile.FormatFeed)
)

var (
	// errBankFeedSwitchedOff is bankFeed's answer when
	// INVOICES_BANK_FEED_ENABLED is off: no client is made, nothing is sent.
	errBankFeedSwitchedOff = errors.New("invoices: the bank feed is switched off")
	// errBankFeedNotConnected is bankFeed's answer when no key is stored.
	errBankFeedNotConnected = errors.New("invoices: no bank key is stored")
	// errBankFeedKeyUnreadable is a stored key the secrets box cannot open:
	// APP_SECRET changed, or the row was altered.
	errBankFeedKeyUnreadable = errors.New("invoices: the stored bank key cannot be opened")
)

// bankFeedRefusal is the problem a bank feed operation answers when bankFeed
// could not make a feed or the bank refused a call, with its status: 409 when
// the feed is switched off or not connected, 422 when the bank refused the
// key, 503 when the stored key cannot be opened or the bank could not be
// reached. Nothing of the bank's own answer is passed on: the adapter's
// errors name the status, never the key, and are logged, not answered.
func (s *server) bankFeedRefusal(ctx context.Context, err error) gen.InvoicesConflictProblem {
	var c gen.InvoicesConflictProblem
	status := http.StatusConflict
	switch {
	case errors.Is(err, errBankFeedSwitchedOff):
		c = conflict(codeBankFeedDisabled, bankFeedDisabledTitle,
			"The bank feed is switched off on this installation (INVOICES_BANK_FEED_ENABLED). Nothing was sent to the bank.")
	case errors.Is(err, errBankFeedNotConnected):
		c = conflict(codeBankFeedNotConnected, bankFeedNotConnectedTitle, "No bank API key is stored. Connect the bank first.")
	case errors.Is(err, bankfeed.ErrUnauthorized):
		c = conflict(codeBankFeedCredentialRejected, bankFeedRejectedTitle,
			"The bank refused the API key. Create a new key in the bank and store it.")
		status = http.StatusUnprocessableEntity
	case errors.Is(err, errBankFeedKeyUnreadable):
		s.deps.Logger.ErrorContext(ctx, "invoices: the stored bank key cannot be opened; it must be entered again")
		c = conflict(codeBankFeedUnavailable, bankFeedUnavailableTitle,
			"The stored bank API key cannot be read on this server. Store the key again.")
		status = http.StatusServiceUnavailable
	default:
		s.deps.Logger.WarnContext(ctx, "invoices: the bank could not be reached", "error", err.Error())
		c = conflict(codeBankFeedUnavailable, bankFeedUnavailableTitle,
			"The bank could not be reached, or did not answer as it should. Nothing was changed; try again.")
		status = http.StatusServiceUnavailable
	}
	c.Status = ptr(int32(status))
	return c
}

// parseBankFeedRequest checks a PUT body field by field and answers the key
// trimmed. A key is printable ASCII without spaces: it goes in a header.
func parseBankFeedRequest(body gen.InvoicesBankFeedRequest) (string, map[string][]string) {
	var errs map[string][]string
	if body.Provider != providerFolio {
		errs = withFieldError(errs, "provider", "The bank must be 'folio'.")
	}
	key := strings.TrimSpace(body.ApiKey)
	switch n := utf8.RuneCountInString(key); {
	case n == 0:
		errs = withFieldError(errs, "apiKey", "Must not be empty.")
	case n > maxBankFeedKeyLength:
		errs = withFieldError(errs, "apiKey", fmt.Sprintf("At most %d characters.", maxBankFeedKeyLength))
	case n < minBankFeedKeyLength:
		errs = withFieldError(errs, "apiKey", fmt.Sprintf("At least %d characters: this is not an API key.", minBankFeedKeyLength))
	case strings.ContainsFunc(key, func(r rune) bool { return r <= ' ' || r > '~' }):
		errs = withFieldError(errs, "apiKey", "An API key holds letters, digits and punctuation only, without spaces.")
	}
	return key, errs
}

// bankFeedResponse is the connection and its accounts as a client may see
// them: never the key.
func (s *server) bankFeedResponse(conn *store.InvoicesBankFeedConnection, accounts []store.InvoicesBankFeedAccount) gen.InvoicesBankFeed {
	out := gen.InvoicesBankFeed{
		EnabledOnInstance: s.deps.Config != nil && s.deps.Config.InvoicesBankFeedEnabled,
		Accounts:          make([]gen.InvoicesBankFeedAccount, 0, len(accounts)),
	}
	if conn != nil {
		out.Connected = true
		out.Provider, out.KeyLastFour, out.Status = &conn.Provider, &conn.KeyLast4, &conn.Status
		out.RejectedAt, out.ConnectedBy, out.ConnectedAt = conn.RejectedAt, &conn.ConnectedByUserID, &conn.ConnectedAt
	}
	for _, a := range accounts {
		out.Accounts = append(out.Accounts, bankFeedAccountResponse(a))
	}
	return out
}

// bankFeedAccountResponse is one account on the wire.
func bankFeedAccountResponse(a store.InvoicesBankFeedAccount) gen.InvoicesBankFeedAccount {
	out := gen.InvoicesBankFeedAccount{
		Account: a.Account, Enabled: a.Enabled, StartDay: wireDate(a.StartDay.Time), Status: a.Status,
		Reason: a.Reason, LastSyncedAt: a.LastSyncedAt, LastBankFileId: a.LastBankFileID, NextSyncAt: a.NextSyncAt,
	}
	if a.Detail != "" {
		out.Detail = &a.Detail
	}
	return out
}

// readBankFeed is the connection, nil without one, and every account, read
// on the pool.
func (s *server) readBankFeed(ctx context.Context) (gen.InvoicesBankFeed, error) {
	q := store.New(s.deps.Pool)
	var conn *store.InvoicesBankFeedConnection
	row, err := q.GetBankFeedConnection(ctx)
	switch {
	case err == nil:
		conn = &row
	case !errors.Is(err, pgx.ErrNoRows):
		return gen.InvoicesBankFeed{}, fmt.Errorf("invoices: read the bank connection: %w", err)
	}
	accounts, err := q.ListBankFeedAccounts(ctx)
	if err != nil {
		return gen.InvoicesBankFeed{}, fmt.Errorf("invoices: read the bank feed's accounts: %w", err)
	}
	return s.bankFeedResponse(conn, accounts), nil
}

// GetInvoicesBankFeed Get the bank connection
// (GET /api/v1/invoices/bank-feed)
func (s *server) GetInvoicesBankFeed(ctx context.Context, _ gen.GetInvoicesBankFeedRequestObject) (gen.GetInvoicesBankFeedResponseObject, error) {
	resp, err := s.readBankFeed(ctx)
	if err != nil {
		return nil, err
	}
	return gen.GetInvoicesBankFeed200JSONResponse(resp), nil
}

// bankFeedPutAfterWrite, when a test installs it, runs inside a connection
// PUT's transaction right after the row is written; nil in production.
var bankFeedPutAfterWrite func(ctx context.Context, tx pgx.Tx)

// PutInvoicesBankFeed Connect the bank
// (PUT /api/v1/invoices/bank-feed)
//
// The key is verified first, with the bank, outside any transaction (plan
// finding 6): a key the bank refuses, or a bank that cannot be reached,
// stores nothing. Then one transaction: the row inserted or written over —
// one statement, which locks it, so two PUTs at once store one row — the key
// sealed and its last four kept, and every account the bank refused the old
// key for made ok and due again.
func (s *server) PutInvoicesBankFeed(ctx context.Context, req gen.PutInvoicesBankFeedRequestObject) (gen.PutInvoicesBankFeedResponseObject, error) {
	if s.deps.Config == nil || !s.deps.Config.InvoicesBankFeedEnabled {
		return gen.PutInvoicesBankFeed409ApplicationProblemPlusJSONResponse(s.bankFeedRefusal(ctx, errBankFeedSwitchedOff)), nil
	}
	key, errs := parseBankFeedRequest(*req.Body)
	if len(errs) > 0 {
		return gen.PutInvoicesBankFeed400ApplicationProblemPlusJSONResponse(invalid(invalidBankFeedTitle, errs)), nil
	}
	feed, err := s.bankFeedWith(key)
	if err != nil {
		return nil, err
	}
	if err := feed.Verify(ctx); err != nil {
		problem := s.bankFeedRefusal(ctx, err)
		if errors.Is(err, bankfeed.ErrUnauthorized) {
			return gen.PutInvoicesBankFeed422ApplicationProblemPlusJSONResponse(problem), nil
		}
		return gen.PutInvoicesBankFeed503ApplicationProblemPlusJSONResponse(problem), nil
	}
	sealed, err := s.deps.Secrets.SealString(bankFeedCredentialPurpose, key)
	if err != nil {
		return nil, fmt.Errorf("invoices: seal the bank key: %w", err)
	}
	runes := []rune(key)
	now := s.deps.Clock()
	err = s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		if _, err := upsertBankFeedConnection(ctx, txq, store.UpsertBankFeedConnectionParams{
			Provider: providerFolio, SecretCiphertext: sealed, KeyLast4: string(runes[len(runes)-4:]),
			ConnectedByUserID: callerID(ctx), Now: now,
		}); err != nil {
			return fmt.Errorf("invoices: store the bank key: %w", err)
		}
		if err := txq.ReviveUnauthorizedBankFeedAccounts(ctx, now); err != nil {
			return fmt.Errorf("invoices: make the bank feed's refused accounts due: %w", err)
		}
		if hook := bankFeedPutAfterWrite; hook != nil {
			hook(ctx, tx)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	resp, err := s.readBankFeed(ctx)
	if err != nil {
		return nil, err
	}
	return gen.PutInvoicesBankFeed200JSONResponse(resp), nil
}

// DeleteInvoicesBankFeed Disconnect the bank
// (DELETE /api/v1/invoices/bank-feed)
//
// One transaction under the connection row FOR UPDATE: the key removed and
// every account switched off. Files, lines, payments and each account's
// format stay (F8).
func (s *server) DeleteInvoicesBankFeed(ctx context.Context, _ gen.DeleteInvoicesBankFeedRequestObject) (gen.DeleteInvoicesBankFeedResponseObject, error) {
	now := s.deps.Clock()
	var removed bool
	err := s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		if _, err := lockBankFeedConnection(ctx, txq); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("invoices: lock the bank connection: %w", err)
		}
		if _, err := txq.DeleteBankFeedConnection(ctx); err != nil {
			return fmt.Errorf("invoices: remove the bank key: %w", err)
		}
		if err := txq.DisableBankFeedAccounts(ctx, now); err != nil {
			return fmt.Errorf("invoices: switch the bank feed's accounts off: %w", err)
		}
		if hook := bankFeedDeleteAfterWrite; hook != nil {
			hook(ctx, tx)
		}
		removed = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	// The table keeps no record of who disconnected; the log does.
	if removed {
		s.deps.Logger.InfoContext(ctx, "invoices: the bank was disconnected", "userId", callerID(ctx).String())
	}
	return gen.DeleteInvoicesBankFeed204Response{}, nil
}

// bankFeedAccountNumber is the account number the bank writes read as 11
// digits, false when it does not read as a Norwegian account number.
func bankFeedAccountNumber(a bankfeed.Account) (string, bool) {
	return bankfile.NormaliseAccount(a.Number)
}

// GetInvoicesBankFeedAccounts List the bank's accounts
// (GET /api/v1/invoices/bank-feed/accounts)
//
// One call to the bank, outside any transaction; each account judged as a
// switch on would judge it: never a card, and only the seller's — the rule
// the bank import holds a file's accounts to.
func (s *server) GetInvoicesBankFeedAccounts(ctx context.Context, _ gen.GetInvoicesBankFeedAccountsRequestObject) (gen.GetInvoicesBankFeedAccountsResponseObject, error) {
	accounts, _, problem, err := s.bankFeedAccounts(ctx)
	switch {
	case err != nil:
		return nil, err
	case problem == nil:
	case *problem.Status == http.StatusConflict:
		return gen.GetInvoicesBankFeedAccounts409ApplicationProblemPlusJSONResponse(*problem), nil
	case *problem.Status == http.StatusUnprocessableEntity:
		return gen.GetInvoicesBankFeedAccounts422ApplicationProblemPlusJSONResponse(*problem), nil
	default:
		return gen.GetInvoicesBankFeedAccounts503ApplicationProblemPlusJSONResponse(*problem), nil
	}
	known, err := store.New(s.deps.Pool).KnownSellerAccounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the seller's accounts: %w", err)
	}
	data := make([]gen.InvoicesBankFeedBankAccount, 0, len(accounts))
	for _, a := range accounts {
		number, ok := bankFeedAccountNumber(a)
		out := gen.InvoicesBankFeedBankAccount{Account: a.Number, Name: a.Name, Kind: string(a.Kind)}
		if ok {
			out.Account = number
		}
		switch {
		case a.Kind == bankfeed.AccountCard:
			out.WhyNot = ptr(whyNotCard)
		case !ok || !slices.Contains(known, number):
			out.WhyNot = ptr(whyNotSellersAccount)
		default:
			out.CanEnable = true
		}
		data = append(data, out)
	}
	return gen.GetInvoicesBankFeedAccounts200JSONResponse(gen.InvoicesBankFeedBankAccountsResponse{Data: data}), nil
}

// bankFeedAccounts is the accounts the stored key reads, from the bank, and
// the key's version (the connection's updated_at) they were read with, or
// the problem to answer when the feed could not be made or the bank refused
// or failed the call. err is the module's own failure, a 500.
func (s *server) bankFeedAccounts(ctx context.Context) ([]bankfeed.Account, time.Time, *gen.InvoicesConflictProblem, error) {
	feed, err := s.bankFeed(ctx)
	switch {
	case errors.Is(err, errBankFeedSwitchedOff), errors.Is(err, errBankFeedNotConnected), errors.Is(err, errBankFeedKeyUnreadable):
		return nil, time.Time{}, ptr(s.bankFeedRefusal(ctx, err)), nil
	case err != nil:
		return nil, time.Time{}, nil, err
	}
	var version time.Time
	if noted, ok := feed.(notedFeed); ok && noted.version != nil {
		version = *noted.version
	}
	accounts, err := feed.Accounts(ctx)
	if err != nil {
		return nil, time.Time{}, ptr(s.bankFeedRefusal(ctx, err)), nil
	}
	return accounts, version, nil, nil
}

// The account switch's and the DELETE's test seams; nil in production.
// bankFeedSwitchBeforeTx runs after a switch on read the bank, before its
// transaction; bankFeedSwitchAfterLock inside a switch's transaction once its
// locks are taken — the account's bank import row, and for a switch on the
// connection too; bankFeedDeleteAfterWrite inside the DELETE's transaction
// once the key is removed and the accounts switched off.
var (
	bankFeedSwitchBeforeTx   func()
	bankFeedSwitchAfterLock  func(ctx context.Context, tx pgx.Tx)
	bankFeedDeleteAfterWrite func(ctx context.Context, tx pgx.Tx)
)

// PutInvoicesBankFeedAccountsByAccount Switch the bank feed on or off for an account
// (PUT /api/v1/invoices/bank-feed/accounts/{account})
//
// On (F4): the bank read first, outside any transaction — the account one it
// answers and not a card — and the account the seller's; then one
// transaction: the account's bank import row first (made with format feed
// when there is none, the import's statement), FOR UPDATE, then the
// connection FOR SHARE — a DELETE meanwhile waits, then switches it off with
// the rest — the format changed to feed as the format PUT changes one, and
// the account enabled from startDay, due now. Off: the bank import row FOR
// UPDATE, then the account switched off; its format is left.
func (s *server) PutInvoicesBankFeedAccountsByAccount(ctx context.Context, req gen.PutInvoicesBankFeedAccountsByAccountRequestObject) (gen.PutInvoicesBankFeedAccountsByAccountResponseObject, error) {
	account := req.Account
	if n, ok := bankfile.NormaliseAccount(account); !ok || n != account {
		return gen.PutInvoicesBankFeedAccountsByAccount400ApplicationProblemPlusJSONResponse(invalid(invalidBankFeedTitle,
			fieldError("account", "The account is its 11 digits, a valid Norwegian account number."))), nil
	}
	if _, err := store.New(s.deps.Pool).GetBankFeedConnection(ctx); errors.Is(err, pgx.ErrNoRows) {
		return gen.PutInvoicesBankFeedAccountsByAccount409ApplicationProblemPlusJSONResponse(s.bankFeedRefusal(ctx, errBankFeedNotConnected)), nil
	} else if err != nil {
		return nil, fmt.Errorf("invoices: read the bank connection: %w", err)
	}
	if !req.Body.Enabled {
		return s.switchBankFeedOff(ctx, account)
	}
	return s.switchBankFeedOn(ctx, account, req.Body.StartDay)
}

// switchBankFeedOff is the switch off: no next sync, no lease, the format
// left as it is.
func (s *server) switchBankFeedOff(ctx context.Context, account string) (gen.PutInvoicesBankFeedAccountsByAccountResponseObject, error) {
	var row store.InvoicesBankFeedAccount
	var missing bool
	err := s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		if _, err := lockImportAccount(ctx, txq, account); errors.Is(err, pgx.ErrNoRows) {
			missing = true
			return errRefused
		} else if err != nil {
			return fmt.Errorf("invoices: lock bank import account %s: %w", account, err)
		}
		if hook := bankFeedSwitchAfterLock; hook != nil {
			hook(ctx, tx)
		}
		var err error
		row, err = txq.DisableBankFeedAccount(ctx, store.DisableBankFeedAccountParams{Account: account, Now: s.deps.Clock()})
		if errors.Is(err, pgx.ErrNoRows) {
			missing = true
			return errRefused
		}
		if err != nil {
			return fmt.Errorf("invoices: switch the bank feed off for %s: %w", account, err)
		}
		return nil
	})
	switch {
	case missing:
		return gen.PutInvoicesBankFeedAccountsByAccount404Response{}, nil
	case err != nil:
		return nil, err
	}
	// The row keeps who switched it on, not who switched it off; the log does.
	s.deps.Logger.InfoContext(ctx, "invoices: the bank feed was switched off for an account",
		"account", account, "userId", callerID(ctx).String())
	return gen.PutInvoicesBankFeedAccountsByAccount200JSONResponse(bankFeedAccountResponse(row)), nil
}

// switchBankFeedOn is the switch on.
func (s *server) switchBankFeedOn(ctx context.Context, account string, startDay *openapi_types.Date) (gen.PutInvoicesBankFeedAccountsByAccountResponseObject, error) {
	now := s.deps.Clock()
	loc, err := s.businessZone(ctx)
	if err != nil {
		return nil, err
	}
	today := holidays.DayIn(now, loc)
	start := today
	if startDay != nil {
		start = startDay.Time
		if start.After(today) {
			return gen.PutInvoicesBankFeedAccountsByAccount400ApplicationProblemPlusJSONResponse(invalid(invalidBankFeedTitle,
				fieldError("startDay", "The start day may not be after today."))), nil
		}
		if start.Before(today.AddDate(0, -maxStartDayMonths, 0)) {
			return gen.PutInvoicesBankFeedAccountsByAccount400ApplicationProblemPlusJSONResponse(invalid(invalidBankFeedTitle,
				fieldError("startDay", fmt.Sprintf("The start day may be at most %d months before today.", maxStartDayMonths)))), nil
		}
	}

	offered, version, problem, err := s.bankFeedAccounts(ctx)
	switch {
	case err != nil:
		return nil, err
	case problem == nil:
	case *problem.Status == http.StatusConflict:
		return gen.PutInvoicesBankFeedAccountsByAccount409ApplicationProblemPlusJSONResponse(*problem), nil
	case *problem.Status == http.StatusUnprocessableEntity:
		return gen.PutInvoicesBankFeedAccountsByAccount422ApplicationProblemPlusJSONResponse(*problem), nil
	default:
		return gen.PutInvoicesBankFeedAccountsByAccount503ApplicationProblemPlusJSONResponse(*problem), nil
	}
	if !slices.ContainsFunc(offered, func(a bankfeed.Account) bool {
		number, ok := bankFeedAccountNumber(a)
		return ok && number == account && a.Kind != bankfeed.AccountCard
	}) {
		return gen.PutInvoicesBankFeedAccountsByAccount409ApplicationProblemPlusJSONResponse(conflict(codeBankFeedAccountNotOffered, bankFeedAccountNotOfferedTitle,
			fmt.Sprintf("The bank does not answer the account ending %s for this key, or it is a card account. Nothing was changed.", account[len(account)-4:]))), nil
	}
	known, err := store.New(s.deps.Pool).KnownSellerAccounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the seller's accounts: %w", err)
	}
	if !slices.Contains(known, account) {
		return gen.PutInvoicesBankFeedAccountsByAccount409ApplicationProblemPlusJSONResponse(conflict(codeBankAccountUnknown, bankAccountUnknownTitle,
			fmt.Sprintf("The account ending %s is neither the seller's bank account nor one an issued invoice printed. Nothing was changed.", account[len(account)-4:]))), nil
	}

	if hook := bankFeedSwitchBeforeTx; hook != nil {
		hook()
	}
	by := callerID(ctx)
	var row store.InvoicesBankFeedAccount
	var disconnected, keyChanged bool
	err = s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		// An account no file named yet is made with format feed, the import's
		// own statement; an existing one is left to the lock below, which a
		// format change needs FOR UPDATE.
		if err := txq.UpsertImportAccounts(ctx, store.UpsertImportAccountsParams{
			Format: bankFeedFormatFeed, SetByUserID: by, SetAt: now, Accounts: []string{account},
		}); err != nil {
			return fmt.Errorf("invoices: insert bank import account %s: %w", account, err)
		}
		locked, err := lockImportAccount(ctx, txq, account)
		if err != nil {
			return fmt.Errorf("invoices: lock bank import account %s: %w", account, err)
		}
		conn, err := shareBankFeedConnection(ctx, txq)
		if errors.Is(err, pgx.ErrNoRows) {
			disconnected = true
			return errRefused
		} else if err != nil {
			return fmt.Errorf("invoices: share the bank connection: %w", err)
		}
		// The account was judged with the key of version; another key stored
		// since may not read it.
		if !conn.UpdatedAt.Equal(version) {
			keyChanged = true
			return errRefused
		}
		if hook := bankFeedSwitchAfterLock; hook != nil {
			hook(ctx, tx)
		}
		if locked.Format != bankFeedFormatFeed {
			if err := changeImportFormat(ctx, txq, locked, bankFeedFormatFeed, by, now); err != nil {
				return err
			}
		}
		row, err = txq.EnableBankFeedAccount(ctx, store.EnableBankFeedAccountParams{
			Account: account, StartDay: pgDate(start), EnabledByUserID: by, Now: now,
		})
		if err != nil {
			return fmt.Errorf("invoices: switch the bank feed on for %s: %w", account, err)
		}
		return nil
	})
	switch {
	case disconnected:
		return gen.PutInvoicesBankFeedAccountsByAccount409ApplicationProblemPlusJSONResponse(s.bankFeedRefusal(ctx, errBankFeedNotConnected)), nil
	case keyChanged:
		return gen.PutInvoicesBankFeedAccountsByAccount409ApplicationProblemPlusJSONResponse(conflict(codeBankFeedKeyChanged, bankFeedKeyChangedTitle,
			"Another bank key was stored while the account was being switched on. Nothing was changed; read the accounts again and try again.")), nil
	case err != nil:
		return nil, err
	}
	return gen.PutInvoicesBankFeedAccountsByAccount200JSONResponse(bankFeedAccountResponse(row)), nil
}

// fetchNowAfterRead is how long after a read Fetch now leaves an account as
// it is: a read that recent is the fetch.
const fetchNowAfterRead = 2 * time.Minute

// PostInvoicesBankFeedSync Fetch the bank feed's accounts now
// (POST /api/v1/invoices/bank-feed/sync)
//
// Fetch now (F5): the account named, or every account the feed is switched
// on for, made due now — never before the bank's 429 allows, and not when it
// was read in the last two minutes — one statement on the pool, no lock taken
// and no call to the bank. The worker's claim then reads it, so a fetch and
// the worker never read one account together; a cycle in flight keeps its
// lease, finishes first and leaves the account due for the next claim. 202 with the accounts made due as they now stand.
func (s *server) PostInvoicesBankFeedSync(ctx context.Context, req gen.PostInvoicesBankFeedSyncRequestObject) (gen.PostInvoicesBankFeedSyncResponseObject, error) {
	if s.deps.Config == nil || !s.deps.Config.InvoicesBankFeedEnabled {
		return gen.PostInvoicesBankFeedSync409ApplicationProblemPlusJSONResponse(s.bankFeedRefusal(ctx, errBankFeedSwitchedOff)), nil
	}
	var account *string
	if req.Body != nil && req.Body.Account != nil {
		if n, ok := bankfile.NormaliseAccount(*req.Body.Account); !ok || n != *req.Body.Account {
			return gen.PostInvoicesBankFeedSync400ApplicationProblemPlusJSONResponse(invalid(invalidBankFeedTitle,
				fieldError("account", "The account is its 11 digits, a valid Norwegian account number."))), nil
		}
		account = req.Body.Account
	}
	q := store.New(s.deps.Pool)
	if _, err := q.GetBankFeedConnection(ctx); errors.Is(err, pgx.ErrNoRows) {
		return gen.PostInvoicesBankFeedSync409ApplicationProblemPlusJSONResponse(s.bankFeedRefusal(ctx, errBankFeedNotConnected)), nil
	} else if err != nil {
		return nil, fmt.Errorf("invoices: read the bank connection: %w", err)
	}
	now := s.deps.Clock()
	if err := q.MakeBankFeedAccountsDue(ctx, store.MakeBankFeedAccountsDueParams{
		Now: now, ReadBefore: now.Add(-fetchNowAfterRead), Account: account,
	}); err != nil {
		return nil, fmt.Errorf("invoices: make the bank feed's accounts due: %w", err)
	}
	rows, err := q.ListBankFeedAccounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the bank feed's accounts: %w", err)
	}
	out := gen.InvoicesBankFeedSyncResponse{Accounts: []gen.InvoicesBankFeedAccount{}}
	for _, r := range rows {
		if r.Enabled && (account == nil || r.Account == *account) {
			out.Accounts = append(out.Accounts, bankFeedAccountResponse(r))
		}
	}
	if account != nil && len(out.Accounts) == 0 {
		return gen.PostInvoicesBankFeedSync404Response{}, nil
	}
	return gen.PostInvoicesBankFeedSync202JSONResponse(out), nil
}

// bankFeedEnabledOn reports whether the feed is switched on for account, read
// on txq under the account's bank import row FOR UPDATE (the format PUT).
func bankFeedEnabledOn(ctx context.Context, txq *store.Queries, account string) (bool, error) {
	row, err := txq.GetBankFeedAccount(ctx, account)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("invoices: read account %s's bank feed: %w", account, err)
	}
	return row.Enabled, nil
}

// bankFeedEnabledConflict is the format PUT's 409 while the feed reads the
// account (plan finding 9).
func bankFeedEnabledConflict(account string) gen.InvoicesConflictProblem {
	return conflict(codeBankFeedEnabled, bankFeedEnabledTitle,
		fmt.Sprintf("The bank feed is switched on for account %s, so its format is feed. Switch the bank feed off for it first.", account))
}

// bankFeedKeyOf opens a stored key; any failure, an empty key included, is
// errBankFeedKeyUnreadable.
func (s *server) bankFeedKeyOf(row store.InvoicesBankFeedConnection) (string, error) {
	key, err := s.deps.Secrets.OpenString(bankFeedCredentialPurpose, row.SecretCiphertext)
	if err != nil || key == "" {
		return "", errBankFeedKeyUnreadable
	}
	return key, nil
}

// flagBankFeedUnauthorized records the bank's refusal of the key of version
// (the connection's updated_at it was opened from). It writes on the pool,
// never inside a caller's transaction, so a rolled-back operation still leaves
// the flag.
func (s *server) flagBankFeedUnauthorized(ctx context.Context, version time.Time) {
	if err := store.New(s.deps.Pool).MarkBankFeedUnauthorized(ctx, store.MarkBankFeedUnauthorizedParams{
		Now: s.deps.Clock(), Version: version,
	}); err != nil {
		s.deps.Logger.ErrorContext(ctx, "invoices: flag the bank key as refused", "error", err.Error())
	}
}
