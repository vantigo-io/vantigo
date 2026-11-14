package invoices_test

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/bankfeed/folio/foliotest"
	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/invoices/bankfile/bankfiletest"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The bank feed's connection and accounts (invoices bank feed design F3, F4,
// F8; plan delivery B): the key verified before it is stored and never
// answered, the accounts the bank answers judged as the import judges a
// file's, and the switch that sets an account's format to feed.

const (
	bankFeedPath         = invoicesPath + "/bank-feed"
	bankFeedAccountsPath = bankFeedPath + "/accounts"
	// bankFeedPurpose is the secrets box purpose the key is sealed under.
	bankFeedPurpose = "invoices/bank-feed-credential"
	// cardAccount is a valid account number the fake answers as a card's;
	// strangerAccount one that is neither the seller's nor printed.
	cardAccount     = "97100000001"
	strangerAccount = "15032080119"
)

// bankFeedJSON is the connection as a client reads it.
type bankFeedJSON struct {
	Connected         bool              `json:"connected"`
	EnabledOnInstance bool              `json:"enabledOnInstance"`
	Provider          *string           `json:"provider"`
	KeyLastFour       *string           `json:"keyLastFour"`
	Status            *string           `json:"status"`
	RejectedAt        *time.Time        `json:"rejectedAt"`
	ConnectedBy       *uuid.UUID        `json:"connectedBy"`
	ConnectedAt       *time.Time        `json:"connectedAt"`
	Accounts          []feedAccountJSON `json:"accounts"`
}

// feedAccountJSON is one account the feed reads.
type feedAccountJSON struct {
	Account    string     `json:"account"`
	Enabled    bool       `json:"enabled"`
	StartDay   string     `json:"startDay"`
	Status     string     `json:"status"`
	NextSyncAt *time.Time `json:"nextSyncAt"`
}

// bankAccountOfferJSON is one account the bank answers.
type bankAccountOfferJSON struct {
	Account   string  `json:"account"`
	Name      string  `json:"name"`
	Kind      string  `json:"kind"`
	CanEnable bool    `json:"canEnable"`
	WhyNot    *string `json:"whyNot"`
}

// folioKey is the PUT body the fake accepts.
func folioKey() map[string]any {
	return map[string]any{"provider": "folio", "apiKey": foliotest.APIKey}
}

func feedManager(t *testing.T, h *harness) *modtest.Client {
	t.Helper()
	return h.SignIn(t, "invoices:access", "invoices:manage")
}

// feedHarness is bankHarness with the bank feed switched on — it is off by
// default until the sync exists — and the fake answering the seller's
// account (as an IBAN), the older one, a card and a stranger's.
func feedHarness(t *testing.T, opts ...modtest.Option) *harness {
	t.Helper()
	h := bankHarness(t, append([]modtest.Option{modtest.WithEnv("INVOICES_BANK_FEED_ENABLED", "1")}, opts...)...)
	h.folio.SetAccounts(
		foliotest.Account{AccountNumber: "NO93 8601 1117 947", Name: "Drift", Type: "Operational", Balance: "0.00"},
		foliotest.Account{AccountNumber: olderAccount, Name: "Gamle driftskonto", Type: "Savings", Balance: "0.00"},
		foliotest.Account{AccountNumber: cardAccount, Name: "Kort", Type: "Card", Balance: "0.00"},
		foliotest.Account{AccountNumber: strangerAccount, Name: "Skatt", Type: "Tax", Balance: "0.00"},
		foliotest.Account{AccountNumber: "ACC-1", Name: "Utland", Type: "Mystery", Balance: "0.00"},
	)
	return h
}

// connect stores the fake's key, failing unless 200.
func connect(t *testing.T, h *harness) bankFeedJSON {
	t.Helper()
	res := feedManager(t, h).Do(http.MethodPut, bankFeedPath, folioKey())
	if res.Status != http.StatusOK {
		t.Fatalf("PUT %s = %d %s, want 200", bankFeedPath, res.Status, res.Body)
	}
	var out bankFeedJSON
	res.JSON(&out)
	return out
}

// switchFeed switches account on or off as c, with startDay when not "".
func switchFeed(c *modtest.Client, account string, enabled bool, startDay string) *modtest.Response {
	body := map[string]any{"enabled": enabled}
	if startDay != "" {
		body["startDay"] = startDay
	}
	return c.Do(http.MethodPut, bankFeedAccountsPath+"/"+account, body)
}

// switchedOn switches account on, failing unless 200.
func switchedOn(t *testing.T, h *harness, account, startDay string) feedAccountJSON {
	t.Helper()
	res := switchFeed(feedManager(t, h), account, true, startDay)
	if res.Status != http.StatusOK {
		t.Fatalf("switch %s on = %d %s, want 200", account, res.Status, res.Body)
	}
	var out feedAccountJSON
	res.JSON(&out)
	return out
}

// importAccount is account's bank import row.
func importAccount(t *testing.T, h *harness, account string) store.InvoicesBankImportAccount {
	t.Helper()
	var row store.InvoicesBankImportAccount
	if err := h.Pool().QueryRow(context.Background(),
		`SELECT account, format, previous_format, cutover_through, set_by_user_id, set_at FROM invoices.bank_import_accounts WHERE account = $1`,
		account).Scan(&row.Account, &row.Format, &row.PreviousFormat, &row.CutoverThrough, &row.SetByUserID, &row.SetAt); err != nil {
		t.Fatalf("read bank import account %s: %v", account, err)
	}
	return row
}

// folioRequests is how many requests the fake received.
func folioRequests(h *harness) int { return len(h.folio.Requests()) }

// No key ever comes out: not in the PUT's answer, the GET or a refusal, not
// in the row's clear columns but its last four — only sealed, under the
// module's own purpose.
func TestBankFeed_NeverAnswersTheKey(t *testing.T) {
	t.Parallel()
	h := feedHarness(t)
	manager, managerID := h.SignInUser(t, "invoices:access", "invoices:manage")

	res := manager.Do(http.MethodPut, bankFeedPath, folioKey())
	if res.Status != http.StatusOK {
		t.Fatalf("PUT = %d %s, want 200", res.Status, res.Body)
	}
	var got bankFeedJSON
	res.JSON(&got)
	last4 := foliotest.APIKey[len(foliotest.APIKey)-4:]
	if !got.Connected || got.Provider == nil || *got.Provider != "folio" || got.KeyLastFour == nil || *got.KeyLastFour != last4 ||
		got.Status == nil || *got.Status != "ok" || got.ConnectedBy == nil || *got.ConnectedBy != managerID ||
		got.ConnectedAt == nil || !got.ConnectedAt.Equal(h.Now()) || !got.EnabledOnInstance {
		t.Errorf("PUT answered %+v, want folio connected by the manager now, its last four %q", got, last4)
	}
	read := h.SignIn(t, "invoices:access").Do(http.MethodGet, bankFeedPath, nil)
	for what, body := range map[string][]byte{"PUT": res.Body, "GET": read.Body} {
		if strings.Contains(string(body), foliotest.APIKey) {
			t.Errorf("the %s answer carries the key: %s", what, body)
		}
	}
	var clear string
	if err := h.Pool().QueryRow(context.Background(),
		`SELECT (to_jsonb(c) - 'secret_ciphertext')::text FROM invoices.bank_feed_connections c`).Scan(&clear); err != nil {
		t.Fatalf("read the connection row: %v", err)
	}
	if strings.Contains(clear, foliotest.APIKey) {
		t.Errorf("the row's clear columns carry the key: %s", clear)
	}
	row, err := store.New(h.Pool()).GetBankFeedConnection(context.Background())
	if err != nil {
		t.Fatalf("read the connection: %v", err)
	}
	if strings.Contains(row.SecretCiphertext, foliotest.APIKey) {
		t.Error("the ciphertext carries the key in clear")
	}
	if key, err := h.Deps().Secrets.OpenString(bankFeedPurpose, row.SecretCiphertext); err != nil || key != foliotest.APIKey {
		t.Errorf("the ciphertext opens to %q, %v under %s, want the key", key, err, bankFeedPurpose)
	}
	if _, err := h.Deps().Secrets.OpenString(vippsPurpose, row.SecretCiphertext); err == nil {
		t.Error("the ciphertext opens under another purpose")
	}

	// A refused key is not answered either, nor any part of it.
	wrong := "wrong-folio-key-5678"
	res = manager.Do(http.MethodPut, bankFeedPath, map[string]any{"provider": "folio", "apiKey": wrong})
	if res.Status != http.StatusUnprocessableEntity || strings.Contains(string(res.Body), wrong) || strings.Contains(string(res.Body), "5678") {
		t.Errorf("PUT of a refused key = %d %s, want 422 without the key", res.Status, res.Body)
	}
}

// A key the bank refuses, or a bank that cannot be reached, stores nothing:
// the verify comes before the transaction (plan finding 6). The key goes to
// the bank as the bearer token, and nowhere else.
func TestBankFeed_PutVerifiesFirst(t *testing.T) {
	t.Parallel()
	h := feedHarness(t)
	manager := feedManager(t, h)
	stored := func() int {
		return h.Count(t, `SELECT count(*) FROM invoices.bank_feed_connections`)
	}

	res := manager.Do(http.MethodPut, bankFeedPath, map[string]any{"provider": "folio", "apiKey": "not-the-fake-key"})
	if res.Status != http.StatusUnprocessableEntity || problemOf(t, res).Code != "bank_feed_credential_rejected" {
		t.Errorf("PUT of a key the bank refuses = %d %s, want 422 bank_feed_credential_rejected", res.Status, res.Body)
	}
	for _, f := range []foliotest.Failure{{Status: http.StatusInternalServerError}, {Status: http.StatusTooManyRequests, RetryAfter: "30"}, {Reset: true}} {
		h.folio.Fail(foliotest.OpAccounts, f)
		res = manager.Do(http.MethodPut, bankFeedPath, folioKey())
		if res.Status != http.StatusServiceUnavailable || problemOf(t, res).Code != "bank_feed_unavailable" {
			t.Errorf("PUT while the bank answers %+v = %d %s, want 503 bank_feed_unavailable", f, res.Status, res.Body)
		}
		h.folio.Clear(foliotest.OpAccounts)
	}
	if n := stored(); n != 0 {
		t.Fatalf("connections after the refusals = %d, want none", n)
	}

	connect(t, h)
	if n := stored(); n != 1 {
		t.Errorf("connections = %d, want one", n)
	}
	requests := h.folio.Requests()
	last := requests[len(requests)-1]
	if last.Method != http.MethodGet || last.Path != "/accounts" || last.Header.Get("Authorization") != "Bearer "+foliotest.APIKey {
		t.Errorf("the verify was %s %s %q, want GET /accounts with the key as its bearer token", last.Method, last.Path, last.Header.Get("Authorization"))
	}
}

// The PUT's refusals: the instance switch off sends nothing to the bank
// (409 bank_feed_disabled); a provider or a key that does not pass is a 400
// on its field; invoices:manage is needed, invoices:access reads.
func TestBankFeed_PutRefusals(t *testing.T) {
	t.Parallel()
	off := feedHarness(t, modtest.WithEnv("INVOICES_BANK_FEED_ENABLED", "0"))
	res := feedManager(t, off).Do(http.MethodPut, bankFeedPath, folioKey())
	if res.Status != http.StatusConflict || problemOf(t, res).Code != "bank_feed_disabled" {
		t.Errorf("PUT with the switch off = %d %s, want 409 bank_feed_disabled", res.Status, res.Body)
	}
	var read bankFeedJSON
	off.SignIn(t, "invoices:access").Do(http.MethodGet, bankFeedPath, nil).JSON(&read)
	if read.EnabledOnInstance || read.Connected {
		t.Errorf("GET with the switch off = %+v, want not enabled, not connected", read)
	}
	if n := folioRequests(off); n != 0 {
		t.Errorf("the bank got %d requests with the switch off, want none", n)
	}

	h := feedHarness(t)
	manager := feedManager(t, h)
	for name, body := range map[string]map[string]any{
		"provider": {"provider": "dnb", "apiKey": foliotest.APIKey},
		"empty":    {"provider": "folio", "apiKey": ""},
		"blank":    {"provider": "folio", "apiKey": "    "},
		"long":     {"provider": "folio", "apiKey": strings.Repeat("k", 1001)},
		"short":    {"provider": "folio", "apiKey": "abc12345"},
		"spaced":   {"provider": "folio", "apiKey": "folio key with spaces"},
	} {
		field := "apiKey"
		if name == "provider" {
			field = "provider"
		}
		if res := manager.Do(http.MethodPut, bankFeedPath, body); res.Status != http.StatusBadRequest || len(problemOf(t, res).Errors[field]) != 1 {
			t.Errorf("PUT %s = %d %s, want 400 on %s", name, res.Status, res.Body, field)
		}
	}
	if n := folioRequests(h); n != 0 {
		t.Errorf("the bank got %d requests for bodies that did not pass, want none", n)
	}
	payments := h.SignIn(t, "invoices:access", "invoices:payments")
	for _, call := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPut, bankFeedPath, folioKey()}, {http.MethodDelete, bankFeedPath, nil}, {http.MethodGet, bankFeedAccountsPath, nil},
		{http.MethodPut, bankFeedAccountsPath + "/" + sellerAccount, map[string]any{"enabled": true}},
	} {
		if res := payments.Do(call.method, call.path, call.body); res.Status != http.StatusForbidden {
			t.Errorf("%s %s without invoices:manage = %d, want 403", call.method, call.path, res.Status)
		}
	}
	if res := h.SignIn(t, "invoices:access").Do(http.MethodGet, bankFeedPath, nil); res.Status != http.StatusOK {
		t.Errorf("GET with invoices:access = %d %s, want 200", res.Status, res.Body)
	}
}

// The bank's accounts, each judged as a switch on would judge it: the
// seller's account read from Folio's IBAN, the older one an issued invoice
// printed, never a card, and a stranger's or an unreadable number not the
// seller's.
func TestBankFeed_AccountsList(t *testing.T) {
	t.Parallel()
	h := feedHarness(t)
	manager := feedManager(t, h)
	if res := manager.Do(http.MethodGet, bankFeedAccountsPath, nil); res.Status != http.StatusConflict || problemOf(t, res).Code != "bank_feed_not_connected" {
		t.Errorf("GET before connecting = %d %s, want 409 bank_feed_not_connected", res.Status, res.Body)
	}
	connect(t, h)
	res := manager.Do(http.MethodGet, bankFeedAccountsPath, nil)
	if res.Status != http.StatusOK {
		t.Fatalf("GET = %d %s, want 200", res.Status, res.Body)
	}
	var got struct {
		Data []bankAccountOfferJSON `json:"data"`
	}
	res.JSON(&got)
	type offer struct {
		account, kind string
		can           bool
		why           string
	}
	var offers []offer
	for _, a := range got.Data {
		why := ""
		if a.WhyNot != nil {
			why = *a.WhyNot
		}
		offers = append(offers, offer{a.Account, a.Kind, a.CanEnable, why})
	}
	want := []offer{
		{sellerAccount, "operational", true, ""},
		{olderAccount, "savings", true, ""},
		{cardAccount, "card", false, "card"},
		{strangerAccount, "tax", false, "not_sellers_account"},
		{"ACC-1", "other", false, "not_sellers_account"},
	}
	if !slices.Equal(offers, want) {
		t.Errorf("the accounts = %+v, want %+v", offers, want)
	}

	// The bank refuses the stored key: 422, and the connection is unauthorized.
	h.folio.Fail(foliotest.OpAccounts, foliotest.Failure{Status: http.StatusUnauthorized})
	h.Advance(time.Minute)
	res = manager.Do(http.MethodGet, bankFeedAccountsPath, nil)
	if res.Status != http.StatusUnprocessableEntity || problemOf(t, res).Code != "bank_feed_credential_rejected" {
		t.Errorf("GET with the key refused = %d %s, want 422", res.Status, res.Body)
	}
	var read bankFeedJSON
	manager.Do(http.MethodGet, bankFeedPath, nil).JSON(&read)
	if read.Status == nil || *read.Status != "unauthorized" || read.RejectedAt == nil || !read.RejectedAt.Equal(h.Now()) {
		t.Errorf("the connection after the refusal = %+v, want unauthorized now", read)
	}
	h.folio.Fail(foliotest.OpAccounts, foliotest.Failure{Status: http.StatusBadGateway})
	if res := manager.Do(http.MethodGet, bankFeedAccountsPath, nil); res.Status != http.StatusServiceUnavailable {
		t.Errorf("GET while the bank fails = %d %s, want 503", res.Status, res.Body)
	}
	h.folio.Clear(foliotest.OpAccounts)

	// A new key makes the connection ok again.
	if again := connect(t, h); again.Status == nil || *again.Status != "ok" || again.RejectedAt != nil {
		t.Errorf("the connection after a new PUT = %+v, want ok, no refusal", again)
	}
}

// Switching an account on, from each state its bank import row may be in:
// none (made with format feed), camt054 with lines (changed as the format PUT
// changes one, the cutover kept), feed already (left as it is).
func TestBankFeed_SwitchOnSetsTheFormat(t *testing.T) {
	t.Parallel()
	h := feedHarness(t)
	connect(t, h)
	manager, managerID := h.SignInUser(t, "invoices:access", "invoices:manage")

	res := switchFeed(manager, sellerAccount, true, "")
	if res.Status != http.StatusOK {
		t.Fatalf("switch on = %d %s, want 200", res.Status, res.Body)
	}
	var got feedAccountJSON
	res.JSON(&got)
	if !got.Enabled || got.StartDay != "2026-10-07" || got.Status != "ok" || got.NextSyncAt == nil || !got.NextSyncAt.Equal(h.Now()) {
		t.Errorf("switched on = %+v, want enabled from today, ok, due now", got)
	}
	if row := importAccount(t, h, sellerAccount); row.Format != "feed" || row.PreviousFormat != nil || row.CutoverThrough.Valid || row.SetByUserID != managerID {
		t.Errorf("a new account's import row = %+v, want feed by the manager, nothing before it", row)
	}

	imported(t, importer(t, h), camtOn("OLD-1", olderAccount, 5, bankfiletest.CamtTx{AmountMinor: 125000, KID: "0010017"}))
	h.Advance(time.Hour)
	got = switchedOn(t, h, olderAccount, "2026-10-01")
	if got.StartDay != "2026-10-01" {
		t.Errorf("the start day = %s, want the one given", got.StartDay)
	}
	row := importAccount(t, h, olderAccount)
	if row.Format != "feed" || row.PreviousFormat == nil || *row.PreviousFormat != "camt054" ||
		!row.CutoverThrough.Valid || row.CutoverThrough.Time.Format(time.DateOnly) != "2026-10-05" {
		t.Errorf("a camt054 account's import row = %+v, want feed after camt054 through 2026-10-05", row)
	}

	// Off and on again: the format stays feed, its cutover as it was.
	if res := switchFeed(manager, olderAccount, false, ""); res.Status != http.StatusOK {
		t.Fatalf("switch off = %d %s", res.Status, res.Body)
	}
	if again := importAccount(t, h, olderAccount); again.Format != "feed" || !again.SetAt.Equal(row.SetAt) {
		t.Errorf("the import row after the switch off = %+v, want it left as it was", again)
	}
	h.Advance(time.Hour)
	switchedOn(t, h, olderAccount, "")
	if again := importAccount(t, h, olderAccount); again.Format != "feed" || !again.SetAt.Equal(row.SetAt) ||
		again.PreviousFormat == nil || *again.PreviousFormat != "camt054" {
		t.Errorf("the import row after switching on again = %+v, want it left as it was", again)
	}

	// A file uploaded for a feed account is refused, the import's own rule.
	refusedImport(t, "a camt.054 file on a feed account",
		upload(t, importer(t, h), camtOn("NEW-1", olderAccount, 6, bankfiletest.CamtTx{AmountMinor: 700, KID: "0010041"})),
		"bank_import_format_mismatch")
}

// The switch's refusals: no connection; a start day after today; an account
// that is not 11 digits; one the bank does not answer, or a card; one that
// is not the seller's; the switch off of an account never switched on.
func TestBankFeed_SwitchRefusals(t *testing.T) {
	t.Parallel()
	h := feedHarness(t)
	manager := feedManager(t, h)
	if res := switchFeed(manager, sellerAccount, true, ""); res.Status != http.StatusConflict || problemOf(t, res).Code != "bank_feed_not_connected" {
		t.Errorf("switch on before connecting = %d %s, want 409 bank_feed_not_connected", res.Status, res.Body)
	}
	connect(t, h)
	if res := switchFeed(manager, sellerAccount, true, "2026-10-08"); res.Status != http.StatusBadRequest || len(problemOf(t, res).Errors["startDay"]) != 1 {
		t.Errorf("a start day after today = %d %s, want 400 on startDay", res.Status, res.Body)
	}
	for _, bad := range []string{"8601111794", "86011117948", "NO9386011117947"} {
		if res := switchFeed(manager, bad, true, ""); res.Status != http.StatusBadRequest || len(problemOf(t, res).Errors["account"]) != 1 {
			t.Errorf("switch on %q = %d %s, want 400 on account", bad, res.Status, res.Body)
		}
	}
	for _, account := range []string{cardAccount, "30000000007"} {
		if res := switchFeed(manager, account, true, ""); res.Status != http.StatusConflict || problemOf(t, res).Code != "bank_feed_account_not_offered" {
			t.Errorf("switch on %s = %d %s, want 409 bank_feed_account_not_offered", account, res.Status, res.Body)
		}
	}
	res := switchFeed(manager, strangerAccount, true, "")
	if p := problemOf(t, res); res.Status != http.StatusConflict || p.Code != "bank_account_unknown" || !strings.Contains(p.Detail, "0119") {
		t.Errorf("switch on a stranger's account = %d %s, want 409 bank_account_unknown naming its last four", res.Status, res.Body)
	}
	if res := switchFeed(manager, olderAccount, false, ""); res.Status != http.StatusNotFound {
		t.Errorf("switch off an account never switched on = %d %s, want 404", res.Status, res.Body)
	}
	if n := h.Count(t, `SELECT count(*) FROM invoices.bank_feed_accounts`) + h.Count(t, `SELECT count(*) FROM invoices.bank_import_accounts`); n != 0 {
		t.Errorf("rows after the refusals = %d, want none", n)
	}
}

// The format PUT never takes feed, and refuses a change away from it while
// the feed reads the account (plan finding 9); once the feed is off the
// change is made as any other, feed kept as the previous format.
func TestBankFeed_FormatPutWhileTheFeedReads(t *testing.T) {
	t.Parallel()
	h := feedHarness(t)
	connect(t, h)
	manager := feedManager(t, h)
	switchedOn(t, h, sellerAccount, "")
	path := bankAccountsPath + "/" + sellerAccount + "/format"

	if res := manager.Do(http.MethodPut, path, map[string]any{"format": "feed"}); res.Status != http.StatusBadRequest {
		t.Errorf("PUT format feed = %d %s, want 400", res.Status, res.Body)
	}
	if res := manager.Do(http.MethodPut, path, map[string]any{"format": "camt054"}); res.Status != http.StatusConflict || problemOf(t, res).Code != "bank_feed_enabled" {
		t.Errorf("PUT format camt054 while the feed reads it = %d %s, want 409 bank_feed_enabled", res.Status, res.Body)
	}
	if res := switchFeed(manager, sellerAccount, false, ""); res.Status != http.StatusOK {
		t.Fatalf("switch off = %d %s", res.Status, res.Body)
	}
	res := manager.Do(http.MethodPut, path, map[string]any{"format": "camt054"})
	var changed bankAccountJSON
	res.JSON(&changed)
	if res.Status != http.StatusOK || changed.Format != "camt054" || changed.PreviousFormat == nil || *changed.PreviousFormat != "feed" {
		t.Errorf("PUT format camt054 once off = %d %s, want camt054 after feed", res.Status, res.Body)
	}
}

// The DELETE removes the key and switches every account off; files, lines
// and each account's format stay. Removing what is not there is a 204 too.
func TestBankFeed_DeleteSwitchesEveryAccountOff(t *testing.T) {
	t.Parallel()
	h := feedHarness(t)
	imported(t, importer(t, h), camtOn("OLD-1", olderAccount, 5, bankfiletest.CamtTx{AmountMinor: 125000, KID: "0010017"}))
	connect(t, h)
	switchedOn(t, h, sellerAccount, "")
	switchedOn(t, h, olderAccount, "")
	before := written(t, h)
	manager := feedManager(t, h)

	if res := manager.Do(http.MethodDelete, bankFeedPath, nil); res.Status != http.StatusNoContent {
		t.Fatalf("DELETE = %d %s, want 204", res.Status, res.Body)
	}
	var read bankFeedJSON
	manager.Do(http.MethodGet, bankFeedPath, nil).JSON(&read)
	if read.Connected || read.KeyLastFour != nil || len(read.Accounts) != 2 {
		t.Fatalf("GET after the DELETE = %+v, want not connected, both accounts kept", read)
	}
	for _, a := range read.Accounts {
		if a.Enabled || a.NextSyncAt != nil {
			t.Errorf("account %s after the DELETE = %+v, want off, no next sync", a.Account, a)
		}
	}
	if got := written(t, h); got != before {
		t.Errorf("files, lines, accounts = %v after the DELETE, want %v", got, before)
	}
	if row := importAccount(t, h, olderAccount); row.Format != "feed" {
		t.Errorf("the format after the DELETE = %s, want feed kept", row.Format)
	}
	if res := manager.Do(http.MethodDelete, bankFeedPath, nil); res.Status != http.StatusNoContent {
		t.Errorf("DELETE again = %d, want 204", res.Status)
	}
	if res := switchFeed(manager, sellerAccount, true, ""); res.Status != http.StatusConflict || problemOf(t, res).Code != "bank_feed_not_connected" {
		t.Errorf("switch on after the DELETE = %d %s, want 409 bank_feed_not_connected", res.Status, res.Body)
	}
}

// A new key brings back every account the bank refused the old one for: ok
// and due now when it is switched on.
func TestBankFeed_NewKeyRevivesRefusedAccounts(t *testing.T) {
	t.Parallel()
	h := feedHarness(t)
	connect(t, h)
	switchedOn(t, h, sellerAccount, "")
	h.Exec(t, `UPDATE invoices.bank_feed_accounts SET status = 'unauthorized', next_sync_at = NULL`)
	h.Advance(time.Hour)
	read := connect(t, h)
	if len(read.Accounts) != 1 || read.Accounts[0].Status != "ok" || read.Accounts[0].NextSyncAt == nil || !read.Accounts[0].NextSyncAt.Equal(h.Now()) {
		t.Errorf("the accounts after a new key = %+v, want ok and due now", read.Accounts)
	}
}

// TestMeta_CanManageBankFeed: the capability is invoices:manage (F8).
func TestMeta_CanManageBankFeed(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	can := func(permissions ...string) bool {
		t.Helper()
		var meta struct {
			Capabilities struct {
				CanManageBankFeed *bool `json:"canManageBankFeed"`
			} `json:"capabilities"`
		}
		res := h.SignIn(t, permissions...).Do(http.MethodGet, metaPath, nil)
		res.JSON(&meta)
		if meta.Capabilities.CanManageBankFeed == nil {
			t.Fatalf("GET /meta = %s, want canManageBankFeed", res.Body)
		}
		return *meta.Capabilities.CanManageBankFeed
	}
	if !can("invoices:access", "invoices:manage") {
		t.Error("canManageBankFeed with invoices:manage = false, want true")
	}
	if can("invoices:access", "invoices:payments", "invoices:issue") {
		t.Error("canManageBankFeed without invoices:manage = true, want false")
	}
}

// Every call to the bank is made outside any transaction.
func TestBankFeed_NoCallUnderALock(t *testing.T) {
	t.Parallel()
	h := feedHarness(t)
	manager, user := h.SignInUser(t, "invoices:access", "invoices:manage")
	if res := manager.Do(http.MethodPut, bankFeedPath, folioKey()); res.Status != http.StatusOK {
		t.Fatalf("PUT = %d %s", res.Status, res.Body)
	}
	if res := manager.Do(http.MethodGet, bankFeedAccountsPath, nil); res.Status != http.StatusOK {
		t.Fatalf("GET accounts = %d %s", res.Status, res.Body)
	}
	if res := switchFeed(manager, sellerAccount, true, ""); res.Status != http.StatusOK {
		t.Fatalf("switch on = %d %s", res.Status, res.Body)
	}
	var methods []string
	for _, call := range contractCalls.by(user) {
		if call.locked {
			t.Errorf("%s was called under a lock", call.method)
		}
		methods = append(methods, call.method)
	}
	if want := []string{"BankFeed.Verify", "BankFeed.Accounts", "BankFeed.Accounts"}; !slices.Equal(methods, want) {
		t.Errorf("the calls out of the module = %v, want %v", methods, want)
	}
}

// TestBankFeed_LockOrder: a switch takes the account's bank import row, then
// the connection; the PUT and the DELETE the connection alone.
func TestBankFeed_LockOrder(t *testing.T) {
	h := feedHarness(t)
	seen := &lockSeen{}
	restore := invoices.SetLockTaken(seen.note)
	defer restore()

	connect(t, h)
	if got, want := seen.take(), []string{"bank_feed_connection 1"}; !slices.Equal(got, want) {
		t.Errorf("the PUT's locks = %v, want %v", got, want)
	}
	switchedOn(t, h, sellerAccount, "")
	if got, want := seen.take(), []string{"account " + sellerAccount, "bank_feed_connection 1"}; !slices.Equal(got, want) {
		t.Errorf("the switch on's locks = %v, want %v", got, want)
	}
	if res := switchFeed(feedManager(t, h), sellerAccount, false, ""); res.Status != http.StatusOK {
		t.Fatalf("switch off = %d %s", res.Status, res.Body)
	}
	if got, want := seen.take(), []string{"account " + sellerAccount}; !slices.Equal(got, want) {
		t.Errorf("the switch off's locks = %v, want %v", got, want)
	}
	if res := feedManager(t, h).Do(http.MethodDelete, bankFeedPath, nil); res.Status != http.StatusNoContent {
		t.Fatalf("DELETE = %d %s", res.Status, res.Body)
	}
	if got, want := seen.take(), []string{"bank_feed_connection 1"}; !slices.Equal(got, want) {
		t.Errorf("the DELETE's locks = %v, want %v", got, want)
	}
}

// A detour through the bank feed loses no cutover (bank feed review): an
// account taken from camt054 to feed keeps camt054's cutover, and switched
// off and back to files while the feed booked nothing, it still holds back
// what camt054 may have registered.
func TestBankFeed_CutoverSurvivesAFeedDetour(t *testing.T) {
	t.Parallel()
	h := feedHarness(t)
	imported(t, importer(t, h), camtOn("OLD-1", olderAccount, 5, bankfiletest.CamtTx{AmountMinor: 125000, KID: "0010017"}))
	connect(t, h)
	manager := feedManager(t, h)
	switchedOn(t, h, olderAccount, "")
	if res := switchFeed(manager, olderAccount, false, ""); res.Status != http.StatusOK {
		t.Fatalf("switch off = %d %s", res.Status, res.Body)
	}
	res := manager.Do(http.MethodPut, bankAccountsPath+"/"+olderAccount+"/format", map[string]any{"format": "ocr"})
	if res.Status != http.StatusOK {
		t.Fatalf("PUT format ocr = %d %s", res.Status, res.Body)
	}
	row := importAccount(t, h, olderAccount)
	if row.Format != "ocr" || row.PreviousFormat == nil || *row.PreviousFormat != "camt054" ||
		!row.CutoverThrough.Valid || row.CutoverThrough.Time.Format(time.DateOnly) != "2026-10-05" {
		t.Errorf("after camt054 → feed → ocr the import row = %+v, want ocr after camt054 through 2026-10-05", row)
	}
	// And back on: ocr brought nothing, so camt054's cutover is kept again.
	switchedOn(t, h, olderAccount, "")
	if row := importAccount(t, h, olderAccount); row.Format != "feed" || row.PreviousFormat == nil || *row.PreviousFormat != "camt054" ||
		!row.CutoverThrough.Valid || row.CutoverThrough.Time.Format(time.DateOnly) != "2026-10-05" {
		t.Errorf("after ocr → feed the import row = %+v, want feed after camt054 through 2026-10-05", row)
	}
}

// A key stored while an account is being switched on — between the bank's
// read and the switch's transaction — refuses the switch: the account was
// judged with the old key (bank feed review).
func TestBankFeed_SwitchRefusedWhenTheKeyChanged(t *testing.T) {
	h := feedHarness(t)
	connect(t, h)
	manager := feedManager(t, h)
	var once sync.Once
	restore := invoices.SetBankFeedSwitchBeforeTx(func() {
		once.Do(func() {
			h.Advance(time.Second)
			if res := manager.Do(http.MethodPut, bankFeedPath, folioKey()); res.Status != http.StatusOK {
				t.Errorf("the PUT meanwhile = %d %s", res.Status, res.Body)
			}
		})
	})
	defer restore()
	res := switchFeed(manager, sellerAccount, true, "")
	if res.Status != http.StatusConflict || problemOf(t, res).Code != "bank_feed_key_changed" {
		t.Errorf("switch on across a new key = %d %s, want 409 bank_feed_key_changed", res.Status, res.Body)
	}
	if n := h.Count(t, `SELECT count(*) FROM invoices.bank_feed_accounts`) + h.Count(t, `SELECT count(*) FROM invoices.bank_import_accounts`); n != 0 {
		t.Errorf("rows after the refusal = %d, want none", n)
	}
	if res := switchFeed(manager, sellerAccount, true, ""); res.Status != http.StatusOK {
		t.Errorf("switch on again = %d %s, want 200", res.Status, res.Body)
	}
}

// A start day is at most 13 months before today (bank feed review).
func TestBankFeed_StartDayAtMostThirteenMonthsBack(t *testing.T) {
	t.Parallel()
	h := feedHarness(t)
	connect(t, h)
	manager := feedManager(t, h)
	if res := switchFeed(manager, sellerAccount, true, "2025-09-06"); res.Status != http.StatusBadRequest || len(problemOf(t, res).Errors["startDay"]) != 1 {
		t.Errorf("a start day 13 months and a day back = %d %s, want 400 on startDay", res.Status, res.Body)
	}
	if got := switchedOn(t, h, sellerAccount, "2025-09-07"); got.StartDay != "2025-09-07" {
		t.Errorf("a start day 13 months back = %s, want it taken", got.StartDay)
	}
}

// Nothing of the key reaches the log, whatever the bank answers: a reset
// connection, a redirect or a 500, to the PUT's verify and to the accounts'
// read (bank feed review).
func TestBankFeed_TheKeyNeverReachesTheLog(t *testing.T) {
	t.Parallel()
	for name, f := range map[string]foliotest.Failure{
		"reset":    {Reset: true},
		"redirect": {Redirect: "https://elsewhere.example.test/accounts"},
		"500":      {Status: http.StatusInternalServerError},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h := feedHarness(t)
			manager := feedManager(t, h)
			connect(t, h)
			h.folio.Fail(foliotest.OpAccounts, f)
			if res := manager.Do(http.MethodPut, bankFeedPath, folioKey()); res.Status != http.StatusServiceUnavailable {
				t.Errorf("PUT while the bank fails = %d %s, want 503", res.Status, res.Body)
			}
			if res := manager.Do(http.MethodGet, bankFeedAccountsPath, nil); res.Status != http.StatusServiceUnavailable {
				t.Errorf("GET accounts while the bank fails = %d %s, want 503", res.Status, res.Body)
			}
			logs := h.Logs()
			if !strings.Contains(logs, "the bank could not be reached") {
				t.Errorf("the log has no line about the failure: %s", logs)
			}
			if strings.Contains(logs, foliotest.APIKey) {
				t.Errorf("the log carries the key:\n%s", logs)
			}
		})
	}
}

// Who disconnected the bank and who switched an account off is logged with
// the caller's user id: the tables keep neither (bank feed review).
func TestBankFeed_LogsWhoDisconnectsAndSwitchesOff(t *testing.T) {
	t.Parallel()
	h := feedHarness(t)
	connect(t, h)
	switchedOn(t, h, sellerAccount, "")
	manager, managerID := h.SignInUser(t, "invoices:access", "invoices:manage")
	if res := switchFeed(manager, sellerAccount, false, ""); res.Status != http.StatusOK {
		t.Fatalf("switch off = %d %s", res.Status, res.Body)
	}
	if res := manager.Do(http.MethodDelete, bankFeedPath, nil); res.Status != http.StatusNoContent {
		t.Fatalf("DELETE = %d %s", res.Status, res.Body)
	}
	logs := h.Logs()
	for _, line := range []string{"the bank feed was switched off for an account", "the bank was disconnected"} {
		i := strings.Index(logs, line)
		if i < 0 {
			t.Errorf("no log line %q", line)
			continue
		}
		end := strings.IndexByte(logs[i:], '\n')
		if end < 0 {
			end = len(logs) - i
		}
		if !strings.Contains(logs[i:i+end], managerID.String()) {
			t.Errorf("the line %q = %s, want the caller's id %s", line, logs[i:i+end], managerID)
		}
	}
}
