package folio_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/bankfeed"
	"github.com/vantigo-io/vantigo/server/internal/bankfeed/folio"
	"github.com/vantigo-io/vantigo/server/internal/bankfeed/folio/foliotest"
	"github.com/vantigo-io/vantigo/server/internal/buildinfo"
)

var start = time.Date(2026, time.November, 13, 10, 0, 0, 0, time.UTC)

// The account the fixtures' transactions are read for.
const operational = "36061234567"

// adapter is the Folio adapter pointed at ft with key, on a clock stopped at
// start.
func adapter(t *testing.T, ft *foliotest.Server, key string) *folio.Client {
	t.Helper()
	client, err := folio.New(folio.Options{
		BaseURL: ft.URL(), APIKey: key, Transport: ft.Transport(),
		Now: func() time.Time { return start },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return client
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return raw
}

func day(s string) time.Time {
	d, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return d
}

// requestsTo is every request whose path starts with prefix, in order.
func requestsTo(ft *foliotest.Server, prefix string) []foliotest.Request {
	var out []foliotest.Request
	for _, r := range ft.Requests() {
		if strings.HasPrefix(r.Path, prefix) {
			out = append(out, r)
		}
	}
	return out
}

// Every call carries the key as a bearer token, asks for JSON and names the
// release.
func assertHeaders(t *testing.T, r foliotest.Request) {
	t.Helper()
	want := map[string]string{
		"Authorization": "Bearer " + foliotest.APIKey,
		"Accept":        "application/json",
		"User-Agent":    "vantigo/" + buildinfo.Version,
	}
	for k, v := range want {
		if got := r.Header.Get(k); got != v {
			t.Errorf("%s %s: %s = %q, want %q", r.Method, r.Path, k, got, v)
		}
	}
}

// Accounts reads GET /accounts and folds Folio's five types onto the port's
// kinds, a type it does not know onto other; Verify makes the same call and
// answers nothing of it.
func TestFolio_Accounts(t *testing.T) {
	t.Parallel()
	ft := foliotest.New(t)
	ft.Fail(foliotest.OpAccounts, foliotest.Failure{Body: fixture(t, "accounts.json")})
	c := adapter(t, ft, foliotest.APIKey)

	got, err := c.Accounts(context.Background())
	if err != nil {
		t.Fatalf("Accounts: %v", err)
	}
	want := []bankfeed.Account{
		{Number: "36061234567", Name: "Driftskonto", Kind: bankfeed.AccountOperational},
		{Number: "36065555555", Name: "Skattetrekk", Kind: bankfeed.AccountTax},
		{Number: "36069876543", Name: "Sparekonto", Kind: bankfeed.AccountSavings},
		{Number: "36061111111", Name: "Moms", Kind: bankfeed.AccountEarmarks},
		{Number: "9836061277190", Name: "Kort", Kind: bankfeed.AccountCard},
		{Number: "36062222222", Name: "Ny kontotype", Kind: bankfeed.AccountOther},
	}
	if len(got) != len(want) {
		t.Fatalf("Accounts = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("account %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if err := c.Verify(context.Background()); err != nil {
		t.Errorf("Verify: %v", err)
	}
	reqs := requestsTo(ft, "/accounts")
	if len(reqs) != 2 {
		t.Fatalf("%d requests, want 2 GET /accounts", len(reqs))
	}
	for _, r := range reqs {
		if r.Method != http.MethodGet || r.Path != "/accounts" {
			t.Errorf("request %s %s, want GET /accounts", r.Method, r.Path)
		}
		assertHeaders(t, r)
	}

	// The fake's own accounts, as a module's test sets them.
	ft.Clear(foliotest.OpAccounts)
	ft.SetAccounts(foliotest.Account{AccountNumber: "36061234567", Name: "Drift", Type: "Operational"})
	got, err = c.Accounts(context.Background())
	if err != nil || len(got) != 1 || got[0] != (bankfeed.Account{Number: "36061234567", Name: "Drift", Kind: bankfeed.AccountOperational}) {
		t.Errorf("Accounts from the fake = %+v, %v", got, err)
	}
}

// An accounts answer without its list or with an account without its
// number does not decode.
func TestFolio_AccountsThatDoNotDecode(t *testing.T) {
	t.Parallel()
	for name, body := range map[string]string{
		"not JSON":          `{"accounts":`,
		"no list":           `{}`,
		"no account number": `{"accounts":[{"name":"Drift","type":"Operational"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ft := foliotest.New(t)
			ft.Fail(foliotest.OpAccounts, foliotest.Failure{Body: []byte(body)})
			if got, err := adapter(t, ft, foliotest.APIKey).Accounts(context.Background()); err == nil {
				t.Errorf("Accounts = %+v, want an error", got)
			}
		})
	}
}

// Every field of R3 the port carries, read from the hand-written fixture:
// each direction and kind, a KID and none, a BBAN and an IBAN creditor
// written with spaces, and a creditor without an account — unknown, which
// the consumer refuses. Raw is each transaction's bytes as Folio sent them.
func TestFolio_Transactions(t *testing.T) {
	t.Parallel()
	ft := foliotest.New(t)
	body := fixture(t, "transactions.json")
	ft.Fail(foliotest.OpTransactions, foliotest.Failure{Body: body})
	c := adapter(t, ft, foliotest.APIKey)

	got, err := c.Transactions(context.Background(), operational, day("2026-11-01"), day("2026-11-13"))
	if err != nil {
		t.Fatalf("Transactions: %v", err)
	}
	type row struct {
		id, bookedOn  string
		minor         int64
		direction     bankfeed.Direction
		kind          bankfeed.Kind
		kid, text     string
		debtorName    string
		debtorAccount string
	}
	want := []row{
		{"0b6f3a52-6c1e-4c8e-9a43-1d2f8e7c5a01", "2026-11-10", 125000, bankfeed.DirectionCredit, bankfeed.KindTransfer, "1000010010", "Betaling med KID", "Kari Nordmann", "12345678903"},
		{"0b6f3a52-6c1e-4c8e-9a43-1d2f8e7c5a02", "2026-11-10", 49950, bankfeed.DirectionCredit, bankfeed.KindTransfer, "", "Faktura 10002", "Ola Nordmann", "98765432109"},
		{"0b6f3a52-6c1e-4c8e-9a43-1d2f8e7c5a03", "2026-11-11", 800000, bankfeed.DirectionDebit, bankfeed.KindTransfer, "2000020025", "Leverandør Rør AS", "Eksempel AS", "36061234567"},
		{"0b6f3a52-6c1e-4c8e-9a43-1d2f8e7c5a04", "2026-11-11", 8900, bankfeed.DirectionDebit, bankfeed.KindCard, "", "KAFFEBRENNERIET", "Eksempel AS", "36061234567"},
		{"0b6f3a52-6c1e-4c8e-9a43-1d2f8e7c5a05", "2026-11-12", 500000, bankfeed.DirectionDebit, bankfeed.KindInternal, "", "Til sparekonto", "Driftskonto", "36061234567"},
		{"0b6f3a52-6c1e-4c8e-9a43-1d2f8e7c5a06", "2026-11-12", 30000, bankfeed.DirectionCredit, bankfeed.KindInternal, "", "Fra skattetrekk", "Skattetrekk", "36065555555"},
		{"0b6f3a52-6c1e-4c8e-9a43-1d2f8e7c5a07", "2026-11-12", 1, bankfeed.DirectionCredit, bankfeed.KindTransfer, "1000020018", "Betaling med KID", "Per Hansen", "15031234567"},
		{"0b6f3a52-6c1e-4c8e-9a43-1d2f8e7c5a08", "2026-11-13", 75000, bankfeed.DirectionUnknown, bankfeed.KindTransfer, "", "Innbetaling", "Lise Berg", "12340056789"},
	}
	if len(got) != len(want) {
		t.Fatalf("%d transactions, want %d", len(got), len(want))
	}
	var answered struct {
		Transactions struct {
			Booked []json.RawMessage `json:"booked"`
		} `json:"transactions"`
	}
	if err := json.Unmarshal(body, &answered); err != nil {
		t.Fatalf("the fixture: %v", err)
	}
	for i, w := range want {
		g := got[i]
		gotRow := row{g.ID, g.BookedOn.Format(time.DateOnly), g.AmountMinor, g.Direction, g.Kind, g.KID, g.Text, g.DebtorName, g.DebtorAccount}
		if gotRow != w {
			t.Errorf("transaction %d = %+v, want %+v", i, gotRow, w)
		}
		if g.Currency != "NOK" {
			t.Errorf("transaction %d: currency %q, want NOK", i, g.Currency)
		}
		if !g.BookedOn.Equal(day(w.bookedOn)) || g.BookedOn.Location() != time.UTC {
			t.Errorf("transaction %d: booked on %v, want UTC midnight", i, g.BookedOn)
		}
		if !bytes.Equal(g.Raw, answered.Transactions.Booked[i]) {
			t.Errorf("transaction %d: Raw is not the bytes Folio sent:\n%s", i, g.Raw)
		}
	}

	reqs := requestsTo(ft, "/accounts/")
	if len(reqs) != 1 {
		t.Fatalf("%d requests, want 1", len(reqs))
	}
	if want := "/accounts/36061234567/transactions?endDate=2026-11-13&startDate=2026-11-01"; reqs[0].Path != want {
		t.Errorf("path = %q, want %q", reqs[0].Path, want)
	}
	assertHeaders(t, reqs[0])
}

// Through the fake as a module's test drives it: the range is both days
// inclusive and nothing outside it is answered; a KID is trimmed; a
// transaction whose creditor and debtor are both the account asked for is
// unknown, its debtor's account compared without its spaces.
func TestFolio_TransactionsThroughTheFake(t *testing.T) {
	t.Parallel()
	ft := foliotest.New(t)
	in := func(id, booked, kid string) foliotest.Transaction {
		return foliotest.Transaction{
			ID: id, EventID: id, Description: "Betaling", BookingDate: booked, KID: kid,
			Creditor:          foliotest.Party{Name: "Eksempel AS", AccountNumber: operational},
			Debtor:            foliotest.Party{Name: "Kari Nordmann", AccountNumber: "12345678903"},
			TransactionAmount: foliotest.Amount{Amount: "100.00", Currency: "NOK"},
		}
	}
	ft.AddTransaction(operational, in("before", "2026-10-31", ""))
	ft.AddTransaction(operational, in("first", "2026-11-01", " 1000010010 "))
	both := in("both", "2026-11-05", "")
	both.Debtor.AccountNumber = "3606 1234 567"
	ft.AddTransaction(operational, both)
	ft.AddTransaction(operational, in("last", "2026-11-13", ""))
	ft.AddTransaction(operational, in("after", "2026-11-14", ""))
	ft.AddTransaction("36069876543", in("elsewhere", "2026-11-05", ""))

	got, err := adapter(t, ft, foliotest.APIKey).Transactions(context.Background(), operational, day("2026-11-01"), day("2026-11-13"))
	if err != nil {
		t.Fatalf("Transactions: %v", err)
	}
	var ids []string
	for _, g := range got {
		ids = append(ids, g.ID)
	}
	if strings.Join(ids, ",") != "first,both,last" {
		t.Fatalf("ids = %v, want first, both, last", ids)
	}
	if got[0].KID != "1000010010" || got[0].Direction != bankfeed.DirectionCredit || got[0].AmountMinor != 10000 {
		t.Errorf("first = %+v, want the KID trimmed, a credit of 10000 øre", got[0])
	}
	if got[1].Direction != bankfeed.DirectionUnknown {
		t.Errorf("both sides the account asked for = %s, want unknown", got[1].Direction)
	}

	if _, err := adapter(t, ft, foliotest.APIKey).Transactions(context.Background(), "99999999999", day("2026-11-01"), day("2026-11-13")); err == nil {
		t.Errorf("an account Folio does not hold answered no error")
	}
}

// A transaction whose id, booking day or amount does not read fails the
// whole call: the consumer refuses the batch rather than store part of it.
// The amount is digits, a point and exactly two decimals — never three,
// never a sign, never past int64 — and is read without a float.
func TestFolio_TransactionsThatDoNotRead(t *testing.T) {
	t.Parallel()
	good := func() map[string]any {
		return map[string]any{
			"id": "0b6f3a52-6c1e-4c8e-9a43-1d2f8e7c5a01", "eventId": "x", "description": "Betaling", "bookingDate": "2026-11-10",
			"creditor": map[string]any{"accountNumber": operational}, "debtor": map[string]any{"accountNumber": "12345678903"},
			"transactionAmount": map[string]any{"amount": "1250.00", "currency": "NOK"},
			"currencyAmount":    map[string]any{"amount": "1250.00", "currency": "NOK"},
			"attachments":       []any{}, "complete": false,
		}
	}
	answer := func(txs ...map[string]any) []byte {
		body, err := json.Marshal(map[string]any{"transactions": map[string]any{"booked": txs}})
		if err != nil {
			t.Fatal(err)
		}
		return body
	}
	cases := map[string][]byte{
		"not JSON":                             []byte(`{"transactions":{"booked":[`),
		"no transactions":                      []byte(`{}`),
		"no booked list":                       []byte(`{"transactions":{}}`),
		"a booked entry that is not an object": []byte(`{"transactions":{"booked":["x"]}}`),
	}
	for name, mutate := range map[string]func(map[string]any){
		"three decimals": func(m map[string]any) {
			m["transactionAmount"] = map[string]any{"amount": "1250.000", "currency": "NOK"}
		},
		"one decimal": func(m map[string]any) { m["transactionAmount"] = map[string]any{"amount": "1250.0", "currency": "NOK"} },
		"no decimals": func(m map[string]any) { m["transactionAmount"] = map[string]any{"amount": "1250", "currency": "NOK"} },
		"a sign": func(m map[string]any) {
			m["transactionAmount"] = map[string]any{"amount": "-1250.00", "currency": "NOK"}
		},
		"an exponent": func(m map[string]any) { m["transactionAmount"] = map[string]any{"amount": "1e3", "currency": "NOK"} },
		"past int64": func(m map[string]any) {
			m["transactionAmount"] = map[string]any{"amount": "92233720368547758.08", "currency": "NOK"}
		},
		"a number, no string":        func(m map[string]any) { m["transactionAmount"] = map[string]any{"amount": 1250.00, "currency": "NOK"} },
		"no currency":                func(m map[string]any) { m["transactionAmount"] = map[string]any{"amount": "1250.00"} },
		"no amount":                  func(m map[string]any) { delete(m, "transactionAmount") },
		"no id":                      func(m map[string]any) { delete(m, "id") },
		"a booking date with a time": func(m map[string]any) { m["bookingDate"] = "2026-11-10T00:00:00Z" },
		"no booking date":            func(m map[string]any) { delete(m, "bookingDate") },
	} {
		bad := good()
		mutate(bad)
		cases[name] = answer(good(), bad)
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ft := foliotest.New(t)
			ft.Fail(foliotest.OpTransactions, foliotest.Failure{Body: body})
			got, err := adapter(t, ft, foliotest.APIKey).Transactions(context.Background(), operational, day("2026-11-01"), day("2026-11-13"))
			if err == nil || got != nil {
				t.Errorf("Transactions = %d transactions, %v; want none and an error", len(got), err)
			}
			if err != nil && (errors.Is(err, bankfeed.ErrUnauthorized) || isRateLimited(err)) {
				t.Errorf("err = %v, want a plain error", err)
			}
		})
	}

	// The largest amount that reads, to the øre.
	ft := foliotest.New(t)
	top := good()
	top["transactionAmount"] = map[string]any{"amount": "92233720368547757.99", "currency": "NOK"}
	ft.Fail(foliotest.OpTransactions, foliotest.Failure{Body: answer(top)})
	got, err := adapter(t, ft, foliotest.APIKey).Transactions(context.Background(), operational, day("2026-11-01"), day("2026-11-13"))
	if err != nil || len(got) != 1 || got[0].AmountMinor != 9223372036854775799 {
		t.Errorf("the largest amount = %+v, %v", got, err)
	}
}

func isRateLimited(err error) bool {
	_, ok := errors.AsType[*bankfeed.ErrRateLimited](err)
	return ok
}

// Each status maps to the port's errors on every call: 401 and 403 are
// refused keys, 429 a rate limit with the Retry-After it named (seconds or a
// date, at most a day), and a 500, a redirect and a reset connection plain
// errors. No error ever names the key.
func TestFolio_ErrorMapping(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		f     foliotest.Failure
		check func(error) bool
	}{
		{"401", foliotest.Failure{Status: 401}, func(err error) bool { return errors.Is(err, bankfeed.ErrUnauthorized) }},
		{"403", foliotest.Failure{Status: 403}, func(err error) bool { return errors.Is(err, bankfeed.ErrUnauthorized) }},
		{"429", foliotest.Failure{Status: 429}, func(err error) bool {
			e, ok := errors.AsType[*bankfeed.ErrRateLimited](err)
			return ok && e.RetryAfter == 0
		}},
		{"429 seconds", foliotest.Failure{Status: 429, RetryAfter: "17"}, func(err error) bool {
			e, ok := errors.AsType[*bankfeed.ErrRateLimited](err)
			return ok && e.RetryAfter == 17*time.Second
		}},
		{"429 date", foliotest.Failure{Status: 429, RetryAfter: start.Add(90 * time.Second).Format(http.TimeFormat)}, func(err error) bool {
			e, ok := errors.AsType[*bankfeed.ErrRateLimited](err)
			return ok && e.RetryAfter == 90*time.Second
		}},
		{"429 a date past", foliotest.Failure{Status: 429, RetryAfter: start.Add(-time.Hour).Format(http.TimeFormat)}, func(err error) bool {
			e, ok := errors.AsType[*bankfeed.ErrRateLimited](err)
			return ok && e.RetryAfter == 0
		}},
		{"429 seconds that overflow", foliotest.Failure{Status: 429, RetryAfter: "9223372036854775807"}, func(err error) bool {
			e, ok := errors.AsType[*bankfeed.ErrRateLimited](err)
			return ok && e.RetryAfter == 24*time.Hour
		}},
		{"500", foliotest.Failure{Status: 500}, isPlain},
		{"404", foliotest.Failure{Status: 404}, isPlain},
		{"reset", foliotest.Failure{Reset: true}, isPlain},
		{"redirect", foliotest.Failure{Redirect: "https://elsewhere.example/accounts"}, isPlain},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ft := foliotest.New(t)
			c := adapter(t, ft, foliotest.APIKey)
			ctx := context.Background()
			ft.Fail(foliotest.OpAccounts, tc.f)
			ft.Fail(foliotest.OpTransactions, tc.f)
			errs := map[string]error{"Verify": c.Verify(ctx)}
			_, errs["Accounts"] = c.Accounts(ctx)
			_, errs["Transactions"] = c.Transactions(ctx, operational, day("2026-11-01"), day("2026-11-13"))
			for call, err := range errs {
				if !tc.check(err) {
					t.Errorf("%s answered %v (%T)", call, err, err)
				}
				if err != nil && strings.Contains(err.Error(), foliotest.APIKey) {
					t.Errorf("%s's error names the key: %v", call, err)
				}
			}
		})
	}
}

func isPlain(err error) bool {
	return err != nil && !errors.Is(err, bankfeed.ErrUnauthorized) && !isRateLimited(err)
}

// A key the bank does not know is refused keys, and the error does not name
// it.
func TestFolio_AnotherKey(t *testing.T) {
	t.Parallel()
	ft := foliotest.New(t)
	const other = "another-secret-key"
	err := adapter(t, ft, other).Verify(context.Background())
	if !errors.Is(err, bankfeed.ErrUnauthorized) {
		t.Fatalf("Verify = %v, want ErrUnauthorized", err)
	}
	if strings.Contains(err.Error(), other) {
		t.Errorf("the error names the key: %v", err)
	}
}

// A failure set on one operation answers that operation only.
func TestFolio_FailIsPerOperation(t *testing.T) {
	t.Parallel()
	ft := foliotest.New(t)
	ft.SetAccounts(foliotest.Account{AccountNumber: operational, Name: "Drift", Type: "Operational"})
	ft.Fail(foliotest.OpTransactions, foliotest.Failure{Status: 500})
	c := adapter(t, ft, foliotest.APIKey)
	if _, err := c.Accounts(context.Background()); err != nil {
		t.Errorf("Accounts with transactions failing: %v", err)
	}
	if _, err := c.Transactions(context.Background(), operational, day("2026-11-01"), day("2026-11-13")); err == nil {
		t.Errorf("Transactions answered no error")
	}
}

// An answer is read up to 10 MiB and refused past it.
func TestFolio_SizeBound(t *testing.T) {
	t.Parallel()
	ft := foliotest.New(t)
	c := adapter(t, ft, foliotest.APIKey)
	whole := []byte(`{"accounts":[]}`)
	padded := append(whole, bytes.Repeat([]byte(" "), foliotest.MaxAnswerBytes-len(whole))...)
	ft.Fail(foliotest.OpAccounts, foliotest.Failure{Body: padded, Times: 1})
	if got, err := c.Accounts(context.Background()); err != nil || len(got) != 0 {
		t.Errorf("an answer of exactly 10 MiB = %v, %v; want it read", got, err)
	}
	ft.Fail(foliotest.OpAccounts, foliotest.Failure{Oversized: true})
	if _, err := c.Accounts(context.Background()); !isPlain(err) {
		t.Errorf("an answer of 10 MiB + 1 = %v, want a plain error", err)
	}
}

// A redirect is answered, never followed (the key would go to a host no
// operator named), and nothing is retried inside the adapter: one failed
// read is one request.
func TestFolio_NoRedirectNoRetry(t *testing.T) {
	t.Parallel()
	ft := foliotest.New(t)
	c := adapter(t, ft, foliotest.APIKey)
	ctx := context.Background()

	ft.Fail(foliotest.OpAccounts, foliotest.Failure{Redirect: ft.URL() + "/elsewhere"})
	if _, err := c.Accounts(ctx); !isPlain(err) {
		t.Errorf("a redirect = %v, want a plain error", err)
	}
	if got := requestsTo(ft, "/elsewhere"); len(got) != 0 {
		t.Errorf("the redirect was followed %d times", len(got))
	}

	for _, f := range []foliotest.Failure{{Status: http.StatusServiceUnavailable}, {Status: http.StatusTooManyRequests}, {Status: http.StatusUnauthorized}} {
		before := len(requestsTo(ft, "/accounts/"))
		ft.Fail(foliotest.OpTransactions, f)
		if _, err := c.Transactions(ctx, operational, day("2026-11-01"), day("2026-11-13")); err == nil {
			t.Errorf("%+v: no error", f)
		}
		if n := len(requestsTo(ft, "/accounts/")) - before; n != 1 {
			t.Errorf("%+v: %d requests, want 1", f, n)
		}
	}
}

// New refuses options it cannot work with, and Transactions a read it
// cannot make, before any request.
func TestFolio_NewRefusesIncompleteOptions(t *testing.T) {
	t.Parallel()
	good := folio.Options{BaseURL: "https://api.folio.no/v2", APIKey: "k"}
	if _, err := folio.New(good); err != nil {
		t.Fatalf("New(good): %v", err)
	}
	if _, err := folio.New(folio.Options{BaseURL: "http://127.0.0.1:8080/v2", APIKey: "k"}); err != nil {
		t.Errorf("New(http): %v", err)
	}
	for name, mutate := range map[string]func(*folio.Options){
		"no base":        func(o *folio.Options) { o.BaseURL = "" },
		"relative":       func(o *folio.Options) { o.BaseURL = "/v2" },
		"another scheme": func(o *folio.Options) { o.BaseURL = "ftp://api.folio.no/v2" },
		"no host":        func(o *folio.Options) { o.BaseURL = "https:///v2" },
		"no key":         func(o *folio.Options) { o.APIKey = "" },
	} {
		o := good
		mutate(&o)
		if _, err := folio.New(o); err == nil {
			t.Errorf("%s: New accepted it", name)
		}
	}

	ft := foliotest.New(t)
	c := adapter(t, ft, foliotest.APIKey)
	if _, err := c.Transactions(context.Background(), " ", day("2026-11-01"), day("2026-11-13")); err == nil {
		t.Errorf("no account: no error")
	}
	if _, err := c.Transactions(context.Background(), operational, day("2026-11-13"), day("2026-11-01")); err == nil {
		t.Errorf("a range that ends before it starts: no error")
	}
	if n := len(ft.Requests()); n != 0 {
		t.Errorf("%d requests sent, want none", n)
	}
}
