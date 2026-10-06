package invoices_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/invoices/bankfile/bankfiletest"
	"github.com/vantigo-io/vantigo/server/internal/invoices/kid"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// Matching on KID (invoices payments and reminders design D4): every line a
// file brought is classified on the pool, then — a candidate — matched under
// its own lock and its invoice's, registering a payment of the principal and
// a charge payment of the rest. The invoices carry KIDs under a 7-digit MOD10
// agreement; the files are bankfiletest's.

func matchPath(id int64) string { return fmt.Sprintf("%s/%d/match", bankFilesPath, id) }

// matchHarness is an installation whose seller has a KID agreement of 7
// digits, MOD10, and banks with sellerAccount; one invoice, the first,
// answered here, was issued while it banked with olderAccount, so a file on
// either account is the seller's. The clock is modtest.Start's: what a test
// issues is issued on 2026-09-12 until it calls toMatchDay.
func matchHarness(t *testing.T, opts ...modtest.Option) (*harness, invoiceJSON) {
	t.Helper()
	h := newHarness(t, opts...)
	older := completeSeller(1)
	older["bankAccount"], older["iban"], older["bic"] = olderAccount, "", ""
	older["kidLength"], older["kidAlgorithm"] = 7, kid.Mod10
	saved := saveSeller(t, h, older)
	first := kidded(t, thousand(t, h))
	current := completeSeller(saved.Revision)
	current["kidLength"], current["kidAlgorithm"] = 7, kid.Mod10
	saveSeller(t, h, current)
	return h, first
}

// toMatchDay moves the clock to bankDay, 2026-10-07, after every booking day
// the tests use.
func toMatchDay(h *harness) { h.Advance(bankDay.Sub(h.Now())) }

// kidded fails the test unless inv carries a KID, and answers it.
func kidded(t *testing.T, inv invoiceJSON) invoiceJSON {
	t.Helper()
	if inv.Kid == nil || inv.Number == nil {
		t.Fatalf("invoice %d has no KID or number", inv.ID)
	}
	return inv
}

// kidInvoice is an issued invoice of 1000.00 with its KID.
func kidInvoice(t *testing.T, h *harness) invoiceJSON {
	t.Helper()
	return kidded(t, thousand(t, h))
}

// minor is an amount in øre.
func minor(amount float64) int64 { return int64(math.Round(amount * 100)) }

// kidPay is an OCR giro payment of amount with KID k to account, settled and
// ordered on day, its archive reference ref (which names its line).
func kidPay(account string, day int, amount float64, k, ref string) bankfiletest.OCRPayment {
	return giro(account, day, minor(amount), k, ref)
}

// camtPay is one camt.054 entry booked on day of one transaction of amount
// with KID k, its AcctSvcrRef ref.
func camtPay(day int, amount float64, k, ref string) bankfiletest.CamtEntry {
	return bankfiletest.CamtEntry{BookedOn: oct(day), Txs: []bankfiletest.CamtTx{{AmountMinor: minor(amount), KID: k, AcctSvcrRef: ref}}}
}

// camtFile is a camt.054.001.02 notification msgID on account of entries.
func camtFile(msgID, account string, entries ...bankfiletest.CamtEntry) []byte {
	return bankfiletest.Camt054("camt.054.001.02", msgID, oct(7).Add(8*time.Hour), account, entries...)
}

// texts runs sql, whose rows are one text each, and answers them.
func texts(t *testing.T, h *harness, sql string, args ...any) []string {
	t.Helper()
	rows, err := h.Pool().Query(context.Background(), sql, args...)
	if err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return out
}

// lineID is the live line of file fileID whose archive reference is ref
// (OCR writes it zero-padded).
func lineID(t *testing.T, h *harness, fileID int64, ref string) int64 {
	t.Helper()
	return modtest.One[int64](t, h.Harness, `SELECT id FROM invoices.bank_transactions
		WHERE bank_file_id = $1 AND ltrim(archive_ref, '0') = ltrim($2, '0') AND duplicate_of_id IS NULL`, fileID, ref)
}

// stateOf is a line's status and reason, "status reason" ("-" for none).
func stateOf(t *testing.T, h *harness, id int64) string {
	t.Helper()
	return modtest.One[string](t, h.Harness, `SELECT status || ' ' || coalesce(reason, '-') FROM invoices.bank_transactions WHERE id = $1`, id)
}

// paymentsFrom is the live payments taken from line id, each "invoice amount
// source paidOn reference".
func paymentsFrom(t *testing.T, h *harness, id int64) []string {
	t.Helper()
	return texts(t, h, `SELECT concat_ws(' ', invoice_id, amount, source, paid_on, reference) FROM invoices.payments
		WHERE bank_transaction_id = $1 AND removed_at IS NULL ORDER BY id`, id)
}

// chargePaymentsFrom is the live charge payments taken from line id, each
// "invoice amount source paidOn reference".
func chargePaymentsFrom(t *testing.T, h *harness, id int64) []string {
	t.Helper()
	return texts(t, h, `SELECT concat_ws(' ', invoice_id, amount, source, paid_on, reference) FROM invoices.charge_payments
		WHERE bank_transaction_id = $1 AND removed_at IS NULL ORDER BY id`, id)
}

// suggestedOf is line id's suggested invoice, "-" for none.
func suggestedOf(t *testing.T, h *harness, id int64) string {
	t.Helper()
	return modtest.One[string](t, h.Harness, `SELECT coalesce(suggested_invoice_id::text, '-') FROM invoices.bank_transactions WHERE id = $1`, id)
}

// eventsOf is line id's events, each "event reason by".
func eventsOf(t *testing.T, h *harness, id int64) []string {
	t.Helper()
	return texts(t, h, `SELECT concat_ws(' ', event, coalesce(reason, '-'), by_user_id) FROM invoices.bank_transaction_events
		WHERE bank_transaction_id = $1 ORDER BY id`, id)
}

// livePaymentsOf is invoice id's live payments, each "amount source".
func livePaymentsOf(t *testing.T, h *harness, id int64) []string {
	t.Helper()
	return texts(t, h, `SELECT concat_ws(' ', amount, source) FROM invoices.payments
		WHERE invoice_id = $1 AND removed_at IS NULL ORDER BY id`, id)
}

// paid is "invoice amount source paidOn reference", as paymentsFrom answers it.
func paid(inv invoiceJSON, amount, source, on, reference string) string {
	return fmt.Sprintf("%d %s %s %s %s", inv.ID, amount, source, on, reference)
}

// badCheck is body with a check digit that verifies under neither MOD10 nor
// MOD11.
func badCheck(body string) string {
	for c := byte('0'); c <= '9'; c++ {
		if c != kid.CheckMod10(body) && c != kid.CheckMod11(body) {
			return body + string(c)
		}
	}
	panic(body)
}

// changeFormat is a manager's PUT of account's format.
func changeFormat(t *testing.T, h *harness, account, format string) {
	t.Helper()
	res := h.SignIn(t, "invoices:access", "invoices:manage").Do(http.MethodPut, bankAccountsPath+"/"+account+"/format", map[string]any{"format": format})
	if res.Status != http.StatusOK {
		t.Fatalf("PUT %s's format %s = %d %s", account, format, res.Status, res.Body)
	}
}

// TestMatch_Classify: D4's seven steps on the pool, in order, each line built
// to pass every step before its own and trip that one — a reversal, a
// negative line, the cutover's possible duplicate, the soft key's, a Vipps
// payout, no KID, a KID that is not valid (a MOD10 failure, a MOD11 one; a
// MOD11 '-' that is valid goes through), a KID no issued invoice carries (no
// such number, another agreement's 25 digits past int64, a stored KID that
// differs, a credit note's number) and an invoice on the other account. A
// line queued on the pool takes no lock: only the candidates are locked,
// each line before its invoice.
func TestMatch_Classify(t *testing.T) {
	h, older := matchHarness(t)
	a, b, c := kidInvoice(t, h), kidInvoice(t, h), kidInvoice(t, h)
	credited := kidInvoice(t, h)
	note := issued(t, h, creditDraft(t, h, credited.ID).ID)
	withKidAgreement(t, h, 7, kid.Mod11)
	h.Exec(t, `UPDATE invoices.counters SET next_value = 1009`)
	dash := kidded(t, thousand(t, h))
	if *dash.Kid != "001009-" {
		t.Fatalf("invoice 1009 under 7/mod11 has KID %s, want 001009-", *dash.Kid)
	}
	toMatchDay(h)
	c1 := importer(t, h)

	// The soft key's first copy: a camt.054 line on olderAccount, matched.
	first := imported(t, c1, camtFile("SOFT-1", olderAccount, camtPay(5, 300, *older.Kid, "SOFT-A")))
	if first.Matched != 1 {
		t.Fatalf("the first copy = %+v, want matched", first)
	}

	unknownNumber, _ := kid.Compute(999, 7, kid.Mod10)
	long := strings.Repeat("9", 24)
	pastInt64 := long + string(kid.CheckMod10(long))
	noteKid, _ := kid.Compute(*note.Number, 7, kid.Mod10)
	mod11Body := "000002"
	mod11Bad := mod11Body + "-"
	if kid.CheckMod11(mod11Body) == '-' {
		t.Fatal("pick another body for the MOD11 failure")
	}
	seen := &lockSeen{}
	restore := invoices.SetLockTaken(seen.note)
	defer restore()

	ocr := imported(t, c1, bankfiletest.OCR("1",
		bankfiletest.OCRPayment{Type: 10, Account: sellerAccount, Settled: oct(6), Ordered: oct(6), AmountMinor: 1000, Negative: true, KID: *a.Kid, ArchiveRef: "11"},
		bankfiletest.OCRPayment{Type: 13, Account: sellerAccount, Settled: oct(6), AmountMinor: 4995, ArchiveRef: "12"},
		kidPay(sellerAccount, 6, 10, badCheck((*a.Kid)[:6]), "13"),
		kidPay(sellerAccount, 6, 11, mod11Bad, "14"),
		kidPay(sellerAccount, 6, 12, unknownNumber, "15"),
		kidPay(sellerAccount, 6, 13, pastInt64, "16"),
		kidPay(sellerAccount, 6, 14, "0"+*b.Kid, "17"),
		kidPay(sellerAccount, 6, 15, noteKid, "18"),
		kidPay(sellerAccount, 6, 16, *older.Kid, "19"),
		kidPay(sellerAccount, 6, 100, *a.Kid, "20"),
		kidPay(sellerAccount, 6, 100, *dash.Kid, "21"),
	))
	ocrLocks := seen.take()
	camt := imported(t, c1, camtFile("CLASSIFY-1", olderAccount,
		bankfiletest.CamtEntry{BookedOn: oct(6), CreditDebit: "DBIT", Reversal: true, Txs: []bankfiletest.CamtTx{{AmountMinor: 5000, KID: *older.Kid, AcctSvcrRef: "REV"}}},
		camtPay(5, 300, *older.Kid, "SOFT-B"),
		bankfiletest.CamtEntry{BookedOn: oct(6), Txs: []bankfiletest.CamtTx{{AmountMinor: 20000, Ustrd: "Utb. 2000810 Vippsnr 117703", AcctSvcrRef: "VIPPS"}}},
		bankfiletest.CamtEntry{BookedOn: oct(6), Txs: []bankfiletest.CamtTx{{AmountMinor: 777, Ustrd: "Faktura 2", AcctSvcrRef: "NOKID"}}},
	))
	camtLocks := seen.take()
	// The cutover: sellerAccount's OCR files covered 6 October; a camt.054
	// line booked then is held back, one booked the day after is not.
	changeFormat(t, h, sellerAccount, "camt054")
	seen.take()
	cut := imported(t, c1, camtFile("CUTOVER-1", sellerAccount, camtPay(6, 50, *c.Kid, "CUT-6"), camtPay(7, 50, *c.Kid, "CUT-7")))

	for _, w := range []struct {
		file      int64
		ref, want string
	}{
		{camt.File.ID, "REV", "exception reversal"},
		{ocr.File.ID, "11", "exception negative_amount"},
		{cut.File.ID, "CUT-6", "exception possible_duplicate"},
		{camt.File.ID, "SOFT-B", "exception possible_duplicate"},
		{camt.File.ID, "VIPPS", "exception vipps_payout"},
		{camt.File.ID, "NOKID", "exception no_kid"},
		{ocr.File.ID, "12", "exception no_kid"},
		{ocr.File.ID, "13", "exception kid_invalid"},
		{ocr.File.ID, "14", "exception kid_invalid"},
		{ocr.File.ID, "15", "exception kid_unknown"},
		{ocr.File.ID, "16", "exception kid_unknown"},
		{ocr.File.ID, "17", "exception kid_unknown"},
		{ocr.File.ID, "18", "exception kid_unknown"},
		{ocr.File.ID, "19", "exception account_mismatch"},
		{ocr.File.ID, "20", "matched -"},
		{ocr.File.ID, "21", "matched -"},
		{cut.File.ID, "CUT-7", "matched -"},
	} {
		if got := stateOf(t, h, lineID(t, h, w.file, w.ref)); got != w.want {
			t.Errorf("line %s = %s, want %s", w.ref, got, w.want)
		}
	}
	// A line queued after its KID named an invoice keeps it as the
	// suggestion — a possible duplicate, by the cutover or the soft key, and
	// an invoice on the other account; one whose KID named none keeps none.
	for _, w := range []struct {
		file      int64
		ref, want string
	}{
		{cut.File.ID, "CUT-6", idKey(c.ID)},
		{camt.File.ID, "SOFT-B", idKey(older.ID)},
		{ocr.File.ID, "19", idKey(older.ID)},
		{ocr.File.ID, "15", "-"},
		{ocr.File.ID, "13", "-"},
		{camt.File.ID, "NOKID", "-"},
		{camt.File.ID, "REV", "-"},
	} {
		if got := suggestedOf(t, h, lineID(t, h, w.file, w.ref)); got != w.want {
			t.Errorf("line %s's suggested invoice = %s, want %s", w.ref, got, w.want)
		}
	}
	if ocr.Matched != 2 || ocr.Exceptions != 9 || ocr.Pending != 0 || ocr.MatchedAmount != 200 || ocr.ExceptionsAmount != 150.95 {
		t.Errorf("the OCR import = matched %d (%v), exceptions %d (%v), pending %d; want 2 (200), 9 (150.95), 0",
			ocr.Matched, ocr.MatchedAmount, ocr.Exceptions, ocr.ExceptionsAmount, ocr.Pending)
	}
	if camt.Matched != 0 || camt.Exceptions != 4 || camt.File.Exceptions != 4 || camt.File.Pending != 0 {
		t.Errorf("the camt.054 import = %+v, want its four lines queued", camt)
	}
	if got := livePaymentsOf(t, h, older.ID); !slices.Equal(got, []string{"300.00 camt054"}) {
		t.Errorf("the older invoice's payments = %v, want the first copy's alone", got)
	}

	// Each import locked its account, then each candidate — the line, then
	// its invoice — in the lines' order, and nothing for a line queued on the
	// pool.
	l20, l21 := lineID(t, h, ocr.File.ID, "20"), lineID(t, h, ocr.File.ID, "21")
	candidates := [][2]int64{{l20, a.ID}, {l21, dash.ID}}
	if l21 < l20 {
		slices.Reverse(candidates)
	}
	wantOCR := []string{"account " + sellerAccount}
	for _, p := range candidates {
		wantOCR = append(wantOCR, "bank_transaction "+idKey(p[0]), "invoice "+idKey(p[1]))
	}
	for _, w := range []struct {
		what      string
		got, want []string
	}{
		{"the OCR file", ocrLocks, wantOCR},
		{"the camt.054 file", camtLocks, []string{"account " + olderAccount}},
		{"the cutover file", seen.take(), []string{"account " + sellerAccount, "bank_transaction " + idKey(lineID(t, h, cut.File.ID, "CUT-7")), "invoice " + idKey(c.ID)}},
	} {
		if !slices.Equal(w.got, w.want) {
			t.Errorf("%s's locks = %v, want %v", w.what, w.got, w.want)
		}
	}
}

// TestMatch_Cases: D4's table under the lock, each row at its boundaries — a
// fully credited invoice queued and a partly credited one paid; a payment
// booked the day before the issue queued and one booked on it paid; the
// open amount exactly and a øre less; the open amount and the charges
// exactly, as a payment and a charge payment; the charges alone of a paid
// invoice, and a øre more (settled); a paid invoice without charges
// (settled); and a øre past the open amount and the charges (exceeds_open).
func TestMatch_Cases(t *testing.T) {
	t.Parallel()
	h, _ := matchHarness(t)
	fully, partly := kidInvoice(t, h), kidInvoice(t, h)
	issued(t, h, creditDraft(t, h, fully.ID).ID)
	issued(t, h, creditOf(t, h, partly.ID, map[int32]float64{1: 1}).ID) // 100.00 credited
	exact, lessOre, withFee, feeOnly, feeOver, settled, over := kidInvoice(t, h), kidInvoice(t, h), kidInvoice(t, h),
		kidInvoice(t, h), kidInvoice(t, h), kidInvoice(t, h), kidInvoice(t, h)
	for _, inv := range []invoiceJSON{withFee, feeOnly, feeOver, over} {
		plantSent(t, h, inv.ID, sentFacts{1, "2026-09-28", "reminder_fee", "35", "", "0"})
	}
	for _, inv := range []invoiceJSON{feeOnly, feeOver, settled} {
		registered(t, h, inv.ID, pay(1000, "2026-09-12"))
	}
	toMatchDay(h)
	late := kidded(t, issued(t, h, createDraft(t, h, draftBody(customerAcme, line("Konsulenttime", 10, 80, vat25))).ID))
	if *late.IssueDate != "2026-10-07" {
		t.Fatalf("the late invoice is issued on %s, want 2026-10-07", *late.IssueDate)
	}

	r := imported(t, importer(t, h), bankfiletest.OCR("1",
		kidPay(sellerAccount, 6, 100, *fully.Kid, "1"),
		kidPay(sellerAccount, 6, 100, *partly.Kid, "2"),
		kidPay(sellerAccount, 6, 100, *late.Kid, "3"),
		kidPay(sellerAccount, 7, 100, *late.Kid, "4"),
		kidPay(sellerAccount, 6, 1000, *exact.Kid, "5"),
		kidPay(sellerAccount, 6, 999.99, *lessOre.Kid, "6"),
		kidPay(sellerAccount, 6, 1035, *withFee.Kid, "7"),
		kidPay(sellerAccount, 6, 35, *feeOnly.Kid, "8"),
		kidPay(sellerAccount, 6, 35.01, *feeOver.Kid, "9"),
		kidPay(sellerAccount, 6, 1, *settled.Kid, "10"),
		kidPay(sellerAccount, 6, 1035.01, *over.Kid, "11"),
	))
	for _, w := range []struct {
		ref, state string
		payments   []string
		charges    []string
	}{
		{"1", "exception invoice_credited", nil, nil},
		{"2", "matched -", []string{paid(partly, "100.00", "ocr", "2026-10-06", *partly.Kid)}, nil},
		{"3", "exception paid_before_issue", nil, nil},
		{"4", "matched -", []string{paid(late, "100.00", "ocr", "2026-10-07", *late.Kid)}, nil},
		{"5", "matched -", []string{paid(exact, "1000.00", "ocr", "2026-10-06", *exact.Kid)}, nil},
		{"6", "matched -", []string{paid(lessOre, "999.99", "ocr", "2026-10-06", *lessOre.Kid)}, nil},
		{"7", "matched -", []string{paid(withFee, "1000.00", "ocr", "2026-10-06", *withFee.Kid)}, []string{paid(withFee, "35.00", "ocr", "2026-10-06", *withFee.Kid)}},
		{"8", "matched -", nil, []string{paid(feeOnly, "35.00", "ocr", "2026-10-06", *feeOnly.Kid)}},
		{"9", "exception invoice_settled", nil, nil},
		{"10", "exception invoice_settled", nil, nil},
		{"11", "exception exceeds_open", nil, nil},
	} {
		id := lineID(t, h, r.File.ID, w.ref)
		if got := stateOf(t, h, id); got != w.state {
			t.Errorf("line %s = %s, want %s", w.ref, got, w.state)
		}
		if got := paymentsFrom(t, h, id); !slices.Equal(got, w.payments) {
			t.Errorf("line %s's payments = %v, want %v", w.ref, got, w.payments)
		}
		if got := chargePaymentsFrom(t, h, id); !slices.Equal(got, w.charges) {
			t.Errorf("line %s's charge payments = %v, want %v", w.ref, got, w.charges)
		}
	}
	// A line queued under the lock keeps the invoice its KID named.
	for ref, inv := range map[string]int64{"1": fully.ID, "3": late.ID, "9": feeOver.ID, "11": over.ID} {
		if got := suggestedOf(t, h, lineID(t, h, r.File.ID, ref)); got != idKey(inv) {
			t.Errorf("line %s's suggested invoice = %s, want %d", ref, got, inv)
		}
	}
	if r.Matched != 6 || r.Exceptions != 5 || r.MatchedAmount != 3269.99 || r.ExceptionsAmount != 1271.02 {
		t.Errorf("the import = matched %d (%v), exceptions %d (%v); want 6 (3269.99), 5 (1271.02)",
			r.Matched, r.MatchedAmount, r.Exceptions, r.ExceptionsAmount)
	}
	if got := getInvoice(t, h, exact.ID); got.State != "paid" || money(got.OpenAmount) != 0.0 {
		t.Errorf("the exactly paid invoice = %s open %v, want paid, 0", got.State, money(got.OpenAmount))
	}
	if got := getInvoice(t, h, lessOre.ID); money(got.OpenAmount) != 0.01 {
		t.Errorf("the invoice paid a øre short = open %v, want 0.01", money(got.OpenAmount))
	}
	chargesAre(t, "the invoice whose fee the line paid", receivablesOf(t, h, withFee.ID), 35, 0, 35, 0, nil)
}

// TestMatch_PossibleDuplicate: what the other format may already have
// registered is held back (D4 step 2) — a line of the new format booked on or
// before the account's cutover, and a line the soft key finds in another
// file with a payment, whatever that file's format; a genuine second payment
// of the same day, amount and KID in one file is registered twice; the same
// in another file with another reference is queued possible_duplicate —
// kept, never lost, for a person to apply.
func TestMatch_PossibleDuplicate(t *testing.T) {
	t.Parallel()
	h, older := matchHarness(t)
	a, b := kidInvoice(t, h), kidInvoice(t, h)
	toMatchDay(h)
	c := importer(t, h)

	ocr := imported(t, c, bankfiletest.OCR("1", kidPay(sellerAccount, 3, 400, *a.Kid, "1")))
	changeFormat(t, h, sellerAccount, "camt054")
	again := imported(t, c, camtFile("CAMT-1", sellerAccount, camtPay(3, 400, *a.Kid, "SAME"), camtPay(4, 400, *b.Kid, "AFTER")))
	if got := stateOf(t, h, lineID(t, h, again.File.ID, "SAME")); got != "exception possible_duplicate" {
		t.Errorf("the camt.054 copy of the OCR payment = %s, want possible_duplicate by the cutover", got)
	}
	if got := stateOf(t, h, lineID(t, h, again.File.ID, "AFTER")); got != "matched -" {
		t.Errorf("a camt.054 line booked after the cutover = %s, want matched", got)
	}
	if got := livePaymentsOf(t, h, a.ID); !slices.Equal(got, []string{"400.00 ocr"}) || stateOf(t, h, lineID(t, h, ocr.File.ID, "1")) != "matched -" {
		t.Errorf("invoice a's payments = %v, want the OCR line's alone", got)
	}

	// The soft key across formats: an OCR line on olderAccount — planted,
	// since the cutover would hold back any real one — paid the older
	// invoice; the camt.054 copy is held back by the soft key alone.
	ocrFile := plantID(t, h, `
		INSERT INTO invoices.bank_files (format, sha256, file_identity, object_key, byte_size, accounts, transactions,
		    ignored, ignored_kinds, uploaded_by_user_id, uploaded_at)
		VALUES ('ocr', md5('soft') || md5('key'), 'soft:key', 'bank-files/s.ocr', 400, ARRAY[$1], 1, 0, '{}', gen_random_uuid(), now())
		RETURNING id`, olderAccount)
	ocrLine := plantID(t, h, `
		INSERT INTO invoices.bank_transactions (bank_file_id, line_ref, format, account, direction, booked_on, amount, currency,
		    kid, fingerprint, ordinal, status)
		VALUES ($1, '1/1', 'ocr', $2, 'credit', DATE '2026-10-05', 300, 'NOK', $3, md5('soft-key-line'), 1, 'matched')
		RETURNING id`, ocrFile, olderAccount, *older.Kid)
	h.Exec(t, `INSERT INTO invoices.payments (invoice_id, paid_on, amount, currency, source, bank_transaction_id, reference,
		    registered_by_user_id, registered_at)
		VALUES ($1, DATE '2026-10-05', 300, 'NOK', 'ocr', $2, $3, gen_random_uuid(), now())`, older.ID, ocrLine, *older.Kid)
	soft := imported(t, c, camtFile("SOFT-1", olderAccount, camtPay(5, 300, *older.Kid, "SOFT")))
	if got := stateOf(t, h, lineID(t, h, soft.File.ID, "SOFT")); got != "exception possible_duplicate" {
		t.Errorf("the camt.054 copy of a paid OCR line = %s, want possible_duplicate by the soft key", got)
	}

	// Two genuine payments of one day, amount and KID in one file: both
	// registered (the ordinal keeps them two lines).
	twice := imported(t, c, camtFile("TWICE-1", olderAccount, camtPay(6, 100, *older.Kid, ""), camtPay(6, 100, *older.Kid, "")))
	if twice.Matched != 2 || twice.Exceptions != 0 {
		t.Errorf("two equal payments in one file = %+v, want both matched", twice)
	}
	// The same once more, in another file with another reference: held back,
	// kept as an exception, its money not registered.
	third := imported(t, c, camtFile("TWICE-2", olderAccount, camtPay(6, 100, *older.Kid, "OTHER")))
	id := lineID(t, h, third.File.ID, "OTHER")
	if got := stateOf(t, h, id); got != "exception possible_duplicate" || len(paymentsFrom(t, h, id)) != 0 {
		t.Errorf("a genuine third payment in another file = %s with %v, want possible_duplicate and nothing registered", got, paymentsFrom(t, h, id))
	}
	if got := livePaymentsOf(t, h, older.ID); len(got) != 3 {
		t.Errorf("the older invoice's payments = %v, want the planted one and the file's two", got)
	}
}

// TestMatch_PaidOnIsBookingDate: a match is paid on the line's booking day —
// OCR's settlement date, never its ordering day, camt.054's BookgDt — never
// the clock's; its reference is the KID, its time the request's clock read.
func TestMatch_PaidOnIsBookingDate(t *testing.T) {
	t.Parallel()
	h, older := matchHarness(t)
	a := kidInvoice(t, h)
	toMatchDay(h)
	c := importer(t, h)
	ocr := imported(t, c, bankfiletest.OCR("1",
		bankfiletest.OCRPayment{Type: 10, Account: sellerAccount, Settled: oct(5), Ordered: oct(2), AmountMinor: 25000, KID: *a.Kid, ArchiveRef: "1"}))
	camt := imported(t, c, camtFile("PAID-1", olderAccount, camtPay(6, 250, *older.Kid, "C")))
	if got := paymentsFrom(t, h, lineID(t, h, ocr.File.ID, "1")); !slices.Equal(got, []string{paid(a, "250.00", "ocr", "2026-10-05", *a.Kid)}) {
		t.Errorf("the OCR payment = %v, want paid on its settlement day 2026-10-05", got)
	}
	if got := paymentsFrom(t, h, lineID(t, h, camt.File.ID, "C")); !slices.Equal(got, []string{paid(older, "250.00", "camt054", "2026-10-06", *older.Kid)}) {
		t.Errorf("the camt.054 payment = %v, want paid on its booking day 2026-10-06", got)
	}
	if n := h.Count(t, `SELECT count(*) FROM invoices.payments WHERE source <> 'manual' AND registered_at <> $1`, h.Now()); n != 0 {
		t.Errorf("%d imported payments not registered at the request's clock read", n)
	}
}

// TestMatch_PrincipalThenCharges: a line pays the principal first, then the
// charges (D4, reading 6) — two rows, the payment of what is open and the
// charge payment of the rest; a line short of the open amount pays the
// principal alone, the fee left outstanding.
func TestMatch_PrincipalThenCharges(t *testing.T) {
	t.Parallel()
	h, _ := matchHarness(t)
	both, short := kidInvoice(t, h), kidInvoice(t, h)
	for _, inv := range []invoiceJSON{both, short} {
		plantSent(t, h, inv.ID, sentFacts{1, "2026-09-28", "reminder_fee", "35", "", "0"})
	}
	toMatchDay(h)
	r := imported(t, importer(t, h), bankfiletest.OCR("1", kidPay(sellerAccount, 6, 1020, *both.Kid, "1"), kidPay(sellerAccount, 6, 500, *short.Kid, "2")))
	id := lineID(t, h, r.File.ID, "1")
	if got := paymentsFrom(t, h, id); !slices.Equal(got, []string{paid(both, "1000.00", "ocr", "2026-10-06", *both.Kid)}) {
		t.Errorf("the principal = %v, want the whole open amount", got)
	}
	if got := chargePaymentsFrom(t, h, id); !slices.Equal(got, []string{paid(both, "20.00", "ocr", "2026-10-06", *both.Kid)}) {
		t.Errorf("the charges = %v, want the rest, 20.00", got)
	}
	short2 := lineID(t, h, r.File.ID, "2")
	if got, charges := paymentsFrom(t, h, short2), chargePaymentsFrom(t, h, short2); !slices.Equal(got, []string{paid(short, "500.00", "ocr", "2026-10-06", *short.Kid)}) || len(charges) != 0 {
		t.Errorf("a line short of the open amount = %v and %v, want the principal alone", got, charges)
	}
	chargesAre(t, "the short line's invoice", receivablesOf(t, h, short.ID), 35, 0, 0, 35, nil)
}

// issueOn issues an invoice of 1000.00 with its KID while the seller banks
// with account, then puts the seller back on sellerAccount: the invoice
// prints account, so a file on it pays the invoice.
func issueOn(t *testing.T, h *harness, account string) invoiceJSON {
	t.Helper()
	var current settingsJSON
	h.SignIn(t, "invoices:access").Do(http.MethodGet, settingsPath, nil).JSON(&current)
	body := completeSeller(current.Revision)
	body["bankAccount"], body["iban"], body["bic"] = account, "", ""
	body["kidLength"], body["kidAlgorithm"] = 7, kid.Mod10
	saved := saveSeller(t, h, body)
	inv := kidInvoice(t, h)
	back := completeSeller(saved.Revision)
	back["kidLength"], back["kidAlgorithm"] = 7, kid.Mod10
	saveSeller(t, h, back)
	return inv
}

// TestMatch_DeadlineMetWaiver: four invoices, each with letter 1 (no fee,
// its deadline 29 September) and letter 2 (a fee of 35, sent 1 October).
// A payment meets letter 1's deadline by its ordering day where the bank
// gives one, else by its booking day (reading 32, the coordinator's decision
// at Task 8's review): an OCR line ordered on 28 September, settled after the
// deadline, and a camt.054 line booked on 28 September, imported after letter
// 2 went out, each have letter 2's fee waived deadline_met in the match's own
// transaction, by the uploader at the request's time (D4's last paragraph,
// D9). An OCR line ordered on 30 September and a camt.054 line booked then —
// after the deadline — waive nothing: letter 2's fee stays outstanding (I1).
func TestMatch_DeadlineMetWaiver(t *testing.T) {
	t.Parallel()
	h, onTime := matchHarness(t) // olderAccount, paid by camt.054
	lateCamt := issueOn(t, h, olderAccount)
	ocr, lateOCR := kidInvoice(t, h), kidInvoice(t, h) // sellerAccount, paid by OCR
	letters := map[int64]int64{}
	for _, inv := range []invoiceJSON{onTime, lateCamt, ocr, lateOCR} {
		plantSent(t, h, inv.ID, sentFacts{1, "2026-09-15", "none", "", "", "0"}) // deadline 2026-09-29
		letters[inv.ID] = plantSent(t, h, inv.ID, sentFacts{2, "2026-10-01", "reminder_fee", "35", "", "0"})
	}
	toMatchDay(h)
	c, user := h.SignInUser(t, "invoices:access", "invoices:payments")
	sep := func(d int) time.Time { return time.Date(2026, 9, d, 0, 0, 0, 0, time.UTC) }
	imported(t, c, bankfiletest.OCR("1",
		bankfiletest.OCRPayment{Type: 10, Account: sellerAccount, Settled: oct(2), Ordered: sep(28), AmountMinor: 100000, KID: *ocr.Kid, ArchiveRef: "1"},
		bankfiletest.OCRPayment{Type: 10, Account: sellerAccount, Settled: oct(2), Ordered: sep(30), AmountMinor: 100000, KID: *lateOCR.Kid, ArchiveRef: "2"}))
	imported(t, c, camtFile("WAIVE-1", olderAccount,
		bankfiletest.CamtEntry{BookedOn: sep(28), Txs: []bankfiletest.CamtTx{{AmountMinor: 100000, KID: *onTime.Kid, AcctSvcrRef: "C1"}}},
		bankfiletest.CamtEntry{BookedOn: sep(30), Txs: []bankfiletest.CamtTx{{AmountMinor: 100000, KID: *lateCamt.Kid, AcctSvcrRef: "C2"}}}))

	for _, w := range []struct {
		what   string
		inv    invoiceJSON
		waived bool
	}{
		{"the OCR line ordered within the deadline", ocr, true},
		{"the camt.054 line booked within the deadline", onTime, true},
		{"the OCR line ordered after the deadline", lateOCR, false},
		{"the camt.054 line booked after the deadline", lateCamt, false},
	} {
		got := texts(t, h, `SELECT concat_ws(' ', reminder_id, kind, amount, reason, waived_by_user_id, (waived_at = $2)::text)
			FROM invoices.charge_waivers WHERE invoice_id = $1`, w.inv.ID, h.Now())
		var want []string
		if w.waived {
			want = []string{fmt.Sprintf("%d fee 35.00 deadline_met %s true", letters[w.inv.ID], user)}
			chargesAre(t, w.what, receivablesOf(t, h, w.inv.ID), 35, 35, 0, 0, nil)
		} else {
			chargesAre(t, w.what, receivablesOf(t, h, w.inv.ID), 35, 0, 0, 35, nil)
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s: waivers %v, want %v", w.what, got, want)
		}
	}
}

// TestMatch_HeldAndHandedOffStillMatch: an invoice on hold and one handed off
// to collection are matched like any other — payments are always
// registered (D4, D11).
func TestMatch_HeldAndHandedOffStillMatch(t *testing.T) {
	t.Parallel()
	h, _ := matchHarness(t)
	held, handed := kidInvoice(t, h), kidInvoice(t, h)
	h.Exec(t, `INSERT INTO invoices.invoice_holds (invoice_id, kind, note, placed_at, placed_by_user_id)
		VALUES ($1, 'disputed', 'Bestrider', now(), gen_random_uuid())`, held.ID)
	h.Exec(t, `INSERT INTO invoices.collection_handoffs (invoice_id, handed_on, agency, created_at, created_by_user_id)
		VALUES ($1, DATE '2026-10-01', 'Inkasso AS', now(), gen_random_uuid())`, handed.ID)
	toMatchDay(h)
	r := imported(t, importer(t, h), bankfiletest.OCR("1", kidPay(sellerAccount, 6, 1000, *held.Kid, "1"), kidPay(sellerAccount, 6, 400, *handed.Kid, "2")))
	if r.Matched != 2 {
		t.Errorf("the import = %+v, want both lines matched", r)
	}
	if got := livePaymentsOf(t, h, held.ID); !slices.Equal(got, []string{"1000.00 ocr"}) {
		t.Errorf("the held invoice's payments = %v", got)
	}
	if got := livePaymentsOf(t, h, handed.ID); !slices.Equal(got, []string{"400.00 ocr"}) {
		t.Errorf("the handed-off invoice's payments = %v", got)
	}
}

// TestMatch_PendingFinishedByMatchEndpoint: matching stops at the first error
// — here a seam's, on the third line — the line in hand rolled back and left
// pending with the rest, the stop logged at warn with the file and the line,
// and the import's 201 reports them pending (the file's rows committed
// first). POST …/match by another user finishes them, registering as that
// user; run again, it has nothing to do. An unknown file is 404, and the
// endpoint needs invoices:payments.
func TestMatch_PendingFinishedByMatchEndpoint(t *testing.T) {
	h, _ := matchHarness(t)
	var pays []bankfiletest.OCRPayment
	for i := range 5 {
		inv := kidInvoice(t, h)
		pays = append(pays, kidPay(sellerAccount, 6, 100, *inv.Kid, fmt.Sprint(i+1)))
	}
	toMatchDay(h)
	uploader, uploaderID := h.SignInUser(t, "invoices:access", "invoices:payments")
	var calls atomic.Int32
	var stoppedAt atomic.Int64
	restore := invoices.SetMatchAfterLineLock(func(_ context.Context, id int64) error {
		if calls.Add(1) == 3 {
			stoppedAt.Store(id)
			return errors.New("the seam stops matching")
		}
		return nil
	})
	r := imported(t, uploader, bankfiletest.OCR("1", pays...))
	restore()
	if r.Matched != 2 || r.Pending != 3 || r.Exceptions != 0 || r.File.Matched != 2 || r.File.Pending != 3 {
		t.Errorf("the stopped import = %+v, want 2 matched and 3 pending", r)
	}
	lines := texts(t, h, `SELECT status FROM invoices.bank_transactions WHERE bank_file_id = $1 ORDER BY id`, r.File.ID)
	if !slices.Equal(lines, []string{"matched", "matched", "pending", "pending", "pending"}) {
		t.Errorf("the lines = %v, want the first two matched and the rest pending", lines)
	}
	logs := h.Logs()
	if !strings.Contains(logs, `"level":"WARN"`) || !strings.Contains(logs, "matching stopped early") ||
		!strings.Contains(logs, fmt.Sprintf(`"bankFileId":%d`, r.File.ID)) || !strings.Contains(logs, fmt.Sprintf(`"bankTransactionId":%d`, stoppedAt.Load())) {
		t.Errorf("no warning names the file %d and the line %d:\n%s", r.File.ID, stoppedAt.Load(), logs)
	}

	finisher, finisherID := h.SignInUser(t, "invoices:access", "invoices:payments")
	res := finisher.Do(http.MethodPost, matchPath(r.File.ID), nil)
	if res.Status != http.StatusOK {
		t.Fatalf("POST …/match = %d %s, want 200", res.Status, res.Body)
	}
	var done importJSON
	res.JSON(&done)
	if done.Matched != 3 || done.MatchedAmount != 300 || done.Pending != 0 || done.Exceptions != 0 || done.File.ID != r.File.ID ||
		done.File.Matched != 5 || done.File.Pending != 0 || done.Transactions != 5 {
		t.Errorf("…/match = %+v, want the three left matched and nothing pending", done)
	}
	by := texts(t, h, `SELECT p.registered_by_user_id::text FROM invoices.payments p
		JOIN invoices.bank_transactions t ON t.id = p.bank_transaction_id WHERE t.bank_file_id = $1 ORDER BY t.id`, r.File.ID)
	want := []string{uploaderID.String(), uploaderID.String(), finisherID.String(), finisherID.String(), finisherID.String()}
	if !slices.Equal(by, want) {
		t.Errorf("registered by %v, want the uploader twice, then the caller of …/match", by)
	}
	res = finisher.Do(http.MethodPost, matchPath(r.File.ID), nil)
	res.JSON(&done)
	if res.Status != http.StatusOK || done.Matched != 0 || done.Exceptions != 0 || done.Pending != 0 {
		t.Errorf("…/match again = %d %+v, want nothing to do", res.Status, done)
	}
	if res := finisher.Do(http.MethodPost, matchPath(r.File.ID+100), nil); res.Status != http.StatusNotFound {
		t.Errorf("…/match of an unknown file = %d, want 404", res.Status)
	}
	if res := h.SignIn(t, "invoices:access").Do(http.MethodPost, matchPath(r.File.ID), nil); res.Status != http.StatusForbidden {
		t.Errorf("…/match with invoices:access alone = %d, want 403", res.Status)
	}
}

// TestMatch_InvoiceNotInNokFailsClosed: a bank line is NOK, so a line whose
// KID names an invoice in another currency is never registered against it —
// there is no reason of the queue for it yet, so the match fails closed: the
// line rolls back and stays pending, matching stops with a warning naming
// it, and the import stands (Task 8's review, MINOR 2).
func TestMatch_InvoiceNotInNokFailsClosed(t *testing.T) {
	t.Parallel()
	h, _ := matchHarness(t)
	h.Exec(t, `UPDATE invoices.settings SET default_currency = 'EUR'`)
	euro := kidded(t, issued(t, h, createDraft(t, h, draftBody(customerEuro, line("Konsulenttime", 10, 80, vat25))).ID))
	h.Exec(t, `UPDATE invoices.settings SET default_currency = 'NOK'`)
	if euro.Currency != "EUR" {
		t.Fatalf("the invoice is in %s, want EUR", euro.Currency)
	}
	toMatchDay(h)
	r := imported(t, importer(t, h), bankfiletest.OCR("1", kidPay(sellerAccount, 6, 100, *euro.Kid, "1")))
	id := lineID(t, h, r.File.ID, "1")
	if got := stateOf(t, h, id); got != "pending -" || r.Pending != 1 || r.Matched != 0 {
		t.Errorf("the line = %s, the import %+v; want it left pending", got, r)
	}
	if got := paymentsFrom(t, h, id); len(got) != 0 {
		t.Errorf("payments from the line = %v, want none", got)
	}
	if logs := h.Logs(); !strings.Contains(logs, "matching stopped early") || !strings.Contains(logs, fmt.Sprintf(`"bankTransactionId":%d`, id)) {
		t.Errorf("no warning names line %d:\n%s", id, logs)
	}
}

// TestMatch_QueueingIsConditional: a line is queued only from pending — the
// classification reads the pool, and a concurrent match of the same file may
// have matched the line meanwhile. Here that match is a transaction holding
// the line, as a match does, that has marked it matched and not committed;
// …/match, which read the line pending and classified it kid_unknown, waits
// for it on the line and then finds nothing to queue: the line stays
// matched, with no queued event, and is counted neither way.
func TestMatch_QueueingIsConditional(t *testing.T) {
	h, _ := matchHarness(t, modtest.WithPoolMaxConns(2))
	toMatchDay(h)
	unknown, _ := kid.Compute(999, 7, kid.Mod10)
	fileID := plantID(t, h, `
		INSERT INTO invoices.bank_files (format, sha256, file_identity, object_key, byte_size, accounts, transactions,
		    ignored, ignored_kinds, uploaded_by_user_id, uploaded_at, duplicates)
		VALUES ('ocr', md5('queue') || md5('line'), 'queue:line', 'bank-files/q.ocr', 400, ARRAY[$1], 1, 0, '{}', gen_random_uuid(), now(), 0)
		RETURNING id`, sellerAccount)
	id := plantID(t, h, `
		INSERT INTO invoices.bank_transactions (bank_file_id, line_ref, format, account, direction, booked_on, amount, currency,
		    kid, fingerprint, ordinal)
		VALUES ($1, '1/1', 'ocr', $2, 'credit', DATE '2026-10-06', 100, 'NOK', $3, md5('queue') || md5('fp'), 1)
		RETURNING id`, fileID, sellerAccount, unknown)

	probeConn := ownConn(t, h)
	before := deadlocks(t, probeConn)
	holder := holdRow(t, h, `SELECT 1 FROM invoices.bank_transactions WHERE id = $1 FOR NO KEY UPDATE`, id)
	if _, err := holder.tx.Exec(context.Background(), `UPDATE invoices.bank_transactions SET status = 'matched' WHERE id = $1`, id); err != nil {
		t.Fatalf("mark the line matched: %v", err)
	}
	c := importer(t, h)
	done := make(chan raceRequest, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		done <- raceRequest{c.Do(http.MethodPost, matchPath(fileID), nil, modtest.Context(ctx))}
	}()
	waiter := newWaiter(t, probeConn)
	if got := blockersOf(t, probeConn, waiter); !slices.Equal(got, []uint32{holder.pid}) {
		t.Errorf("…/match waits on %v, want the concurrent match %d", got, holder.pid)
	}
	holder.release(t)
	var r importJSON
	finished(t, "…/match", done, http.StatusOK).JSON(&r)
	if got := stateOf(t, h, id); got != "matched -" {
		t.Errorf("the line = %s, want it left matched", got)
	}
	if got := eventsOf(t, h, id); len(got) != 0 {
		t.Errorf("the line's events = %v, want no queued event", got)
	}
	if r.Exceptions != 0 || r.Matched != 0 || r.Pending != 0 {
		t.Errorf("…/match = %+v, want the line counted neither matched nor queued, and nothing pending", r)
	}
	if after := deadlocks(t, probeConn); after != before {
		t.Errorf("Postgres broke %d deadlock(s)", after-before)
	}
}

// TestCharges_InterestWaiverThenLaterLetter (NI1): Task 4's figures planted —
// letter 1 claimed 20 of interest, 12 were paid and 8 waived, letter 2 claims
// 33 cumulatively, its total 1013 — so a payment of letter 2's total is the
// principal and a charge payment of 13, not exceeds_open.
func TestCharges_InterestWaiverThenLaterLetter(t *testing.T) {
	t.Parallel()
	h, _ := matchHarness(t)
	inv := kidInvoice(t, h)
	first := plantSent(t, h, inv.ID, sentFacts{1, "2026-09-20", "none", "", "", "20"})
	plantChargePayment(t, h, inv.ID, "2026-09-25", "12")
	h.Exec(t, `INSERT INTO invoices.charge_waivers (invoice_id, reminder_id, kind, amount, interest_through, reason, waived_by_user_id, waived_at)
		VALUES ($1, $2, 'interest', 8, DATE '2026-09-20', 'goodwill', gen_random_uuid(), now())`, inv.ID, first)
	plantSent(t, h, inv.ID, sentFacts{2, "2026-10-01", "none", "", "", "33"})
	chargesAre(t, "before the payment", receivablesOf(t, h, inv.ID), 33, 8, 12, 13, nil)
	toMatchDay(h)
	r := imported(t, importer(t, h), bankfiletest.OCR("1", kidPay(sellerAccount, 6, 1013, *inv.Kid, "1")))
	id := lineID(t, h, r.File.ID, "1")
	if got := stateOf(t, h, id); got != "matched -" {
		t.Fatalf("letter 2's total = %s, want matched", got)
	}
	if got := paymentsFrom(t, h, id); !slices.Equal(got, []string{paid(inv, "1000.00", "ocr", "2026-10-06", *inv.Kid)}) {
		t.Errorf("the principal = %v", got)
	}
	if got := chargePaymentsFrom(t, h, id); !slices.Equal(got, []string{paid(inv, "13.00", "ocr", "2026-10-06", *inv.Kid)}) {
		t.Errorf("the charges = %v, want 13.00", got)
	}
	chargesAre(t, "after the payment", receivablesOf(t, h, inv.ID), 33, 8, 25, 0, nil)
}

// TestMatch_EventsAndRegisteringUser: every outcome writes the line's event
// by the uploader — matched, or queued with its reason, on the pool or under
// the lock — and the uploader registers the payments.
func TestMatch_EventsAndRegisteringUser(t *testing.T) {
	t.Parallel()
	h, _ := matchHarness(t)
	a, settled := kidInvoice(t, h), kidInvoice(t, h)
	registered(t, h, settled.ID, pay(1000, "2026-09-12"))
	toMatchDay(h)
	c, user := h.SignInUser(t, "invoices:access", "invoices:payments")
	unknown, _ := kid.Compute(999, 7, kid.Mod10)
	r := imported(t, c, bankfiletest.OCR("1",
		kidPay(sellerAccount, 6, 100, *a.Kid, "1"), kidPay(sellerAccount, 6, 100, unknown, "2"), kidPay(sellerAccount, 6, 100, *settled.Kid, "3")))
	for ref, want := range map[string]string{"1": "matched -", "2": "queued kid_unknown", "3": "queued invoice_settled"} {
		if got := eventsOf(t, h, lineID(t, h, r.File.ID, ref)); !slices.Equal(got, []string{want + " " + user.String()}) {
			t.Errorf("line %s's events = %v, want %s by %s", ref, got, want, user)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM invoices.bank_transaction_events WHERE at <> $1`, h.Now()); n != 0 {
		t.Errorf("%d events not at the request's clock read", n)
	}
	if got := texts(t, h, `SELECT registered_by_user_id::text FROM invoices.payments WHERE source = 'ocr'`); !slices.Equal(got, []string{user.String()}) {
		t.Errorf("the imported payment registered by %v, want the uploader %s", got, user)
	}
}

// TestMatch_LockOrder: each candidate line is matched under two locks — the
// bank line, then its invoice — and nothing else (D18); a line queued on the
// pool takes none. The waiver a match writes takes no further lock.
func TestMatch_LockOrder(t *testing.T) {
	h, _ := matchHarness(t)
	a, b := kidInvoice(t, h), kidInvoice(t, h)
	plantSent(t, h, b.ID, sentFacts{1, "2026-09-15", "none", "", "", "0"})
	plantSent(t, h, b.ID, sentFacts{2, "2026-10-01", "reminder_fee", "35", "", "0"})
	toMatchDay(h)
	c := importer(t, h)
	unknown, _ := kid.Compute(999, 7, kid.Mod10)
	seen := &lockSeen{}
	restore := invoices.SetLockTaken(seen.note)
	defer restore()
	r := imported(t, c, bankfiletest.OCR("1",
		kidPay(sellerAccount, 6, 100, *a.Kid, "1"),
		kidPay(sellerAccount, 6, 100, unknown, "2"),
		bankfiletest.OCRPayment{Type: 10, Account: sellerAccount, Settled: oct(6), Ordered: time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC), AmountMinor: 100000, KID: *b.Kid, ArchiveRef: "3"},
	))
	l1, l3 := lineID(t, h, r.File.ID, "1"), lineID(t, h, r.File.ID, "3")
	var want []string
	for _, p := range [][2]int64{{l1, a.ID}, {l3, b.ID}} {
		want = append(want, "bank_transaction "+idKey(p[0]), "invoice "+idKey(p[1]))
	}
	if l3 < l1 {
		want = append(want[2:], want[:2]...)
	}
	if got := seen.take(); !slices.Equal(got, append([]string{"account " + sellerAccount}, want...)) {
		t.Errorf("the import's locks = %v, want the account, then each candidate's line and invoice %v", got, want)
	}
	if n := h.Count(t, `SELECT count(*) FROM invoices.charge_waivers WHERE invoice_id = $1 AND reason = 'deadline_met'`, b.ID); n != 1 {
		t.Errorf("invoice b has %d deadline_met waivers, want 1", n)
	}
	res := c.Do(http.MethodPost, matchPath(r.File.ID), nil)
	if res.Status != http.StatusOK {
		t.Fatalf("…/match = %d %s", res.Status, res.Body)
	}
	if got := seen.take(); len(got) != 0 {
		t.Errorf("…/match of a file with nothing pending took %v, want no lock", got)
	}
}

// TestMatch_NoCallUnderALock: matching calls nothing out of the module — no
// directory, no object store — so the import's calls are its Exists and Put
// before its transaction, and …/match makes none; the harness's recorder
// fails any call made under a lock.
func TestMatch_NoCallUnderALock(t *testing.T) {
	t.Parallel()
	h, _ := matchHarness(t)
	a := kidInvoice(t, h)
	plantSent(t, h, a.ID, sentFacts{1, "2026-09-28", "reminder_fee", "35", "", "0"})
	toMatchDay(h)
	c, user := h.SignInUser(t, "invoices:access", "invoices:payments")
	r := imported(t, c, bankfiletest.OCR("1", kidPay(sellerAccount, 6, 1035, *a.Kid, "1")))
	if r.Matched != 1 {
		t.Fatalf("the import = %+v, want the line matched", r)
	}
	var methods []string
	for _, call := range contractCalls.by(user) {
		if call.locked {
			t.Errorf("%s was called under a lock", call.method)
		}
		methods = append(methods, call.method)
	}
	if !slices.Equal(methods, []string{"ObjectStore.Exists", "ObjectStore.Put"}) {
		t.Errorf("the import's calls out of the module = %v, want Exists then Put", methods)
	}
	other, otherID := h.SignInUser(t, "invoices:access", "invoices:payments")
	if res := other.Do(http.MethodPost, matchPath(r.File.ID), nil); res.Status != http.StatusOK {
		t.Fatalf("…/match = %d %s", res.Status, res.Body)
	}
	if calls := contractCalls.by(otherID); len(calls) != 0 {
		t.Errorf("…/match called %v, want nothing out of the module", calls)
	}
}

// TestPayments_SourceOnTheWire: every payment answers its source (D2) — a
// manual one manual, without a bank line; an imported one its file's format
// and its line — and its registering user, present on both.
func TestPayments_SourceOnTheWire(t *testing.T) {
	t.Parallel()
	h, _ := matchHarness(t)
	a := kidInvoice(t, h)
	manual, manualID := h.SignInUser(t, "invoices:access", "invoices:payments")
	if res := manual.Do(http.MethodPost, paymentsPath(a.ID), pay(100, "2026-09-12")); res.Status != http.StatusOK {
		t.Fatalf("the manual payment = %d %s", res.Status, res.Body)
	}
	toMatchDay(h)
	c, uploaderID := h.SignInUser(t, "invoices:access", "invoices:payments")
	r := imported(t, c, bankfiletest.OCR("1", kidPay(sellerAccount, 6, 200, *a.Kid, "1")))
	line := lineID(t, h, r.File.ID, "1")
	got := getInvoice(t, h, a.ID).Payments
	if len(got) != 2 {
		t.Fatalf("payments = %+v, want two", got)
	}
	if p := got[0]; p.Source != "manual" || p.BankTransactionID != nil || p.RegisteredByUserID != manualID.String() {
		t.Errorf("the manual payment = %+v, want source manual, no bank line, registered by %s", p, manualID)
	}
	if p := got[1]; p.Source != "ocr" || p.BankTransactionID == nil || *p.BankTransactionID != line || p.RegisteredByUserID != uploaderID.String() ||
		p.Reference != *a.Kid || p.PaidOn != "2026-10-06" {
		t.Errorf("the imported payment = %+v, want source ocr, line %d, registered by %s, the KID, paid on 2026-10-06", p, line, uploaderID)
	}
}
