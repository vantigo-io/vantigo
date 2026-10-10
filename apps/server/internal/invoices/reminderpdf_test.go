package invoices_test

import (
	"bytes"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/invoices/kid"
)

// The letter's content (invoices payments and reminders design D10), pinned
// as text: every word a letter prints, in order, with its mail's subject,
// attachment name and cover — read through SetReminderPDFModelText while the
// worker sends it, and held in testdata/reminders/*.txt (plan reading 15).
// `go test -run TestReminderLetter -update` rewrites them. The PDF itself is
// asserted to be one, its bytes stored under their hash.

var updateLetters = flag.Bool("update", false, "rewrite testdata/reminders from the letters the worker sends")

// letterTexts collects the text of every letter laid out while it is
// installed, by letter.
type letterTexts struct {
	mu    sync.Mutex
	texts map[int64]string
}

func collectLetters(t *testing.T) *letterTexts {
	t.Helper()
	l := &letterTexts{texts: map[int64]string{}}
	t.Cleanup(invoices.SetReminderPDFModelText(func(id int64, text []string) {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.texts[id] = strings.Join(text, "\n") + "\n"
	}))
	return l
}

func (l *letterTexts) of(id int64) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.texts[id]
}

// golden compares got with testdata/reminders/name, or rewrites it.
func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "reminders", name)
	if *updateLetters {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil { //nolint:gosec // a golden file in the repository
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path) //nolint:gosec // a golden file in the repository
	if err != nil {
		t.Fatalf("read %s (run with -update to write it): %v", path, err)
	}
	if got != string(want) {
		t.Errorf("%s differs from the letter sent:\n--- got\n%s\n--- want\n%s", name, got, want)
	}
}

// kidOf is invoice number's KID under the seller's agreement, seven digits
// with MOD10.
func kidOf(t *testing.T, number int64) string {
	t.Helper()
	k, err := kid.Compute(number, 7, kid.Mod10)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// assertPDF fails unless letter id was stored as a PDF under its hash.
func assertPDF(t *testing.T, h *harness, id int64) {
	t.Helper()
	r := letterOf(t, h, id)
	body := h.objects.object(deref(r.PdfObjectKey))
	if !bytes.HasPrefix(body, []byte("%PDF-")) || !bytes.Contains(body, []byte("/Type /Page")) ||
		!bytes.HasSuffix(bytes.TrimRight(body, "\r\n"), []byte("%%EOF")) || sha(body) != deref(r.PdfSha256) ||
		!strings.Contains(deref(r.PdfObjectKey), deref(r.PdfSha256)) {
		t.Errorf("letter %d's stored object (%d bytes, key %s) is not a PDF stored under its hash %s",
			id, len(body), deref(r.PdfObjectKey), deref(r.PdfSha256))
	}
}

// Each level in each language, through the worker on Saturday 12 September
// 2026 (late interest on but where named): a fee-free reminder in Norwegian
// (with the KID) and in English (without, so the invoice number is quoted),
// a reminder with its fee and interest, the creditor's inkassovarsel in both
// languages, a reminder announcing the hand-off under the 2026 act, and a
// business's first letter with the compensation. Each holds D10's fixed
// sentences in its language, and its PDF is stored under its hash.
func TestReminderLetter_Goldens(t *testing.T) {
	letters := collectLetters(t)
	for _, c := range []struct {
		file, set string
		invoice   letterInvoice
		sentFirst bool // one fee-free reminder sent on 20 August before
		level     string
		announces bool
		has       []string
	}{
		{"reminder-nb.txt", "person_charge = 'none', business_charge = 'none'",
			letterInvoice{number: 101, customer: customerAcme, due: "2026-08-03", kid: "kid"}, false, "reminder", false,
			[]string{"# Purring", "Merk betalingen med KID", "Har du betalt i mellomtiden, kan du se bort fra dette brevet.",
				"Har du innsigelser mot kravet, gi oss beskjed før fristen.", "Subject: Purring: faktura 101"}},
		{"reminder-en.txt", "person_charge = 'none', business_charge = 'none'",
			letterInvoice{number: 102, customer: customerAcme, due: "2026-08-03", language: "en"}, false, "reminder", false,
			[]string{"# Payment reminder", "Please quote invoice number 102 with your payment.",
				"If you have paid in the meantime, please disregard this letter.",
				"If you dispute the claim, let us know before the deadline.", "Subject: Reminder: invoice 102"}},
		{"reminder-fee-nb.txt", "late_interest = true",
			letterInvoice{number: 103, customer: customerAcme, due: "2026-08-03", kid: "kid"}, false, "reminder", false,
			[]string{"Purregebyr: 38,00", "Forsinkelsesrente fra 04.08.2026", "Å betale: NOK"}},
		{"notice-nb.txt", "late_interest = true",
			letterInvoice{number: 104, customer: customerAcme, due: "2026-07-20", kid: "kid"}, true, "collection_notice", false,
			[]string{"# Inkassovarsel", "Kravet vil bli sendt til inkasso dersom det ikke er betalt innen 28.09.2026.",
				"Inkasso kan føre til at det påløper ytterligere kostnader.", "Subject: Inkassovarsel: faktura 104",
				"Attachment: inkassovarsel-104-2.pdf"}},
		{"notice-en.txt", "late_interest = true",
			letterInvoice{number: 105, customer: customerAcme, due: "2026-07-20", language: "en"}, true, "collection_notice", false,
			[]string{"# Debt collection notice", "The claim will be sent to debt collection if it is not paid by 2026-09-28.",
				"Debt collection may add further costs.", "Subject: Debt collection notice: invoice 105"}},
		{"announces-collection-nb.txt", "inkassolov_2026_from = DATE '2026-09-01'",
			letterInvoice{number: 106, customer: customerAcme, due: "2026-07-20", kid: "kid"}, true, "reminder", true,
			[]string{"# Purring", "Kravet vil bli oversendt til et inkassoforetak dersom det ikke er betalt innen 28.09.2026."}},
		{"compensation-en.txt", "business_charge = 'compensation', late_interest = true",
			letterInvoice{number: 107, customer: customerAcme, due: "2026-08-03", language: "en", business: true}, false, "reminder", false,
			[]string{"Compensation for recovery costs: 430.00", "Late payment interest from 2026-08-04"}},
	} {
		t.Run(c.file, func(t *testing.T) {
			h, mails := workerHarness(t, c.set)
			spec := c.invoice
			if spec.kid != "" {
				spec.kid = kidOf(t, spec.number)
			}
			id := plantLetterInvoice(t, h, spec)
			sequence := 1
			if c.sentFirst {
				plantLetterSent(t, h, id, 1, "reminder", "2026-08-20", "", "")
				sequence = 2
			}
			language := "nb"
			if spec.language == "en" {
				language = "en"
			}
			letter := queueLetter(t, h, id, sequence, c.level, c.announces, language)
			dispatch(t, invoices.NewReminderWorker(h.Deps()))
			if r := letterOf(t, h, letter); r.Status != "sent" || len(mails.delivered()) != 1 {
				t.Fatalf("the letter = %s (%s), want sent", r.Status, deref(r.WithdrawalReason))
			}
			text := letters.of(letter)
			for _, s := range c.has {
				if !strings.Contains(text, s) {
					t.Errorf("the letter lacks %q", s)
				}
			}
			golden(t, c.file, text)
			assertPDF(t, h, letter)
		})
	}
}

// The B2 pair (D9) through the worker: letter 1 on Saturday 12 September —
// the fee 38 and interest 13.42 from 4 August; a charge payment of 50 on 20
// September, which pays the fee and 12 of the interest; letter 2, the
// inkassovarsel, on Friday 2 October once letter 1's deadline (28 September)
// and its grace have passed — the earlier charges settled (charges_earlier
// 0), its own fee 38 (R10: the deadline passed unmet), interest 20.14 to
// that day, 12 of it paid — its total 1 046.14, the principal and the
// charges outstanding once it is sent.
func TestReminderLetter_TwoLettersWithInterestAndAChargePayment(t *testing.T) {
	letters := collectLetters(t)
	h, mails := workerHarness(t, "late_interest = true")
	w := invoices.NewReminderWorker(h.Deps())
	id := plantLetterInvoice(t, h, letterInvoice{number: 201, customer: customerAcme, due: "2026-08-03", kid: kidOf(t, 201)})
	first := queueLetter(t, h, id, 1, "reminder", false, "nb")
	dispatch(t, w)
	r1 := letterOf(t, h, first)
	if got := fmt.Sprintf("%s fee %s interest %s total %s deadline %s", r1.Status, amountText(r1.Fee), amountText(r1.Interest),
		amountText(r1.Total), dayText(r1.Deadline)); got != "sent fee 38.00 interest 13.42 total 1051.42 deadline 2026-09-28" {
		t.Fatalf("letter 1 = %s", got)
	}
	golden(t, "pair-1-nb.txt", letters.of(first))

	moveClockTo(t, h, time.Date(2026, time.September, 20, 10, 0, 0, 0, time.UTC))
	okAs(t, "the charge payment", chargePayer(t, h).Do(http.MethodPost, chargePaymentsPath(id), map[string]any{
		"amount": 50, "paidOn": "2026-09-20",
	}))
	moveClockTo(t, h, time.Date(2026, time.October, 2, 8, 0, 0, 0, time.UTC))
	second := queueLetter(t, h, id, 2, "collection_notice", false, "nb")
	dispatch(t, w)
	r2 := letterOf(t, h, second)
	got := fmt.Sprintf("%s charges_earlier %s fee %s interest %s interest_paid %s total %s",
		r2.Status, amountText(r2.ChargesEarlier), amountText(r2.Fee), amountText(r2.Interest), amountText(r2.InterestPaid), amountText(r2.Total))
	if want := "sent charges_earlier 0.00 fee 38.00 interest 20.14 interest_paid 12.00 total 1046.14"; got != want {
		t.Errorf("letter 2 = %s\nwant       %s", got, want)
	}
	golden(t, "pair-2-nb.txt", letters.of(second))
	doc := receivablesOf(t, h, id)
	if doc.Charges == nil || doc.OpenAmount == nil || fmt.Sprintf("%.2f", *doc.OpenAmount+doc.Charges.Outstanding) != amountText(r2.Total) {
		t.Errorf("the principal and the charges outstanding = %v + %+v, want letter 2's total %s", doc.OpenAmount, doc.Charges, amountText(r2.Total))
	}
	if len(mails.delivered()) != 2 {
		t.Errorf("%d mails, want the two letters", len(mails.delivered()))
	}
	assertPDF(t, h, second)
}

// A credit in charges_earlier is worded as one (plan reading 55): letter 1
// claimed the fee 38 and interest 13.42; 38 was paid and the fee then waived
// as goodwill, so the 38 pays the interest — 20.14 by 2 October — and 17.86
// is left over, which pays letter 2's own fee: its charges_earlier is
// -17.86, printed as a credit from earlier payments of charges, and its
// total 1 020.14 is the principal and the charges outstanding.
func TestReminderLetter_ACreditFromChargesPaid(t *testing.T) {
	letters := collectLetters(t)
	h, _ := workerHarness(t, "late_interest = true")
	w := invoices.NewReminderWorker(h.Deps())
	id := plantLetterInvoice(t, h, letterInvoice{number: 202, customer: customerAcme, due: "2026-08-03"})
	first := queueLetter(t, h, id, 1, "reminder", false, "nb")
	dispatch(t, w)
	moveClockTo(t, h, time.Date(2026, time.September, 20, 10, 0, 0, 0, time.UTC))
	c := chargePayer(t, h)
	okAs(t, "the charge payment", c.Do(http.MethodPost, chargePaymentsPath(id), map[string]any{"amount": 38, "paidOn": "2026-09-20"}))
	okAs(t, "the waiver", c.Do(http.MethodPost, waivePath(id), map[string]any{
		"waivers": []map[string]any{waiver(first, "fee")}, "reason": "goodwill",
	}))
	moveClockTo(t, h, time.Date(2026, time.October, 2, 8, 0, 0, 0, time.UTC))
	second := queueLetter(t, h, id, 2, "collection_notice", false, "nb")
	dispatch(t, w)
	r := letterOf(t, h, second)
	if got := fmt.Sprintf("%s charges_earlier %s total %s", r.Status, amountText(r.ChargesEarlier), amountText(r.Total)); got !=
		"sent charges_earlier -17.86 total 1020.14" {
		t.Errorf("letter 2 = %s, want sent with charges_earlier -17.86, total 1020.14", got)
	}
	text := letters.of(second)
	if !strings.Contains(text, "Til gode fra tidligere innbetalinger av gebyrer: -17,86") || strings.Contains(text, "som står ute") {
		t.Errorf("letter 2 does not word its charges_earlier as a credit:\n%s", text)
	}
	doc := receivablesOf(t, h, id)
	if doc.Charges == nil || doc.OpenAmount == nil || fmt.Sprintf("%.2f", *doc.OpenAmount+doc.Charges.Outstanding) != amountText(r.Total) {
		t.Errorf("the principal and the charges outstanding = %v + %+v, want letter 2's total", doc.OpenAmount, doc.Charges)
	}
}
