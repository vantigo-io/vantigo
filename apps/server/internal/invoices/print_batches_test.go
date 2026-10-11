package invoices_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// Paper letters (invoices payments and reminders design D10, reading 39): a
// batch printed for the day it will be posted, each letter judged again on
// that day under its invoice's lock and its own and given that day's facts;
// its combined PDF rendered from the rows; the batch sent only when it is
// confirmed posted on that very day — re-judged under the batch's locks,
// with a charge the day no longer supports waived claimed_in_error — and
// reprinted otherwise. The clock starts on Saturday 12 September 2026 at
// noon; Acme's 1 000 due 3 August, handed over on its issue day, gets a
// reminder with the fee 38 (750 ÷ 20, rounded).

const printBatchesPath = invoicesPath + "/reminder-print-batches"

func batchPath(id int64, op string) string { return fmt.Sprintf("%s/%d/%s", printBatchesPath, id, op) }

type printBatchJSON struct {
	ID          int64          `json:"id"`
	PostOn      string         `json:"postOn"`
	PostedOn    *string        `json:"postedOn"`
	CreatedBy   string         `json:"createdBy"`
	PostedBy    *string        `json:"postedBy"`
	PostedAt    *string        `json:"postedAt"`
	ReprintedAt *string        `json:"reprintedAt"`
	Letters     []reminderJSON `json:"letters"`
}

type leftOutJSON struct {
	ReminderID int64  `json:"reminderId"`
	InvoiceID  int64  `json:"invoiceId"`
	Reason     string `json:"reason"`
	Outdated   *struct {
		Kind     string `json:"kind"`
		HalfYear string `json:"halfYear"`
	} `json:"outdated"`
}

type batchResultJSON struct {
	Batch   printBatchJSON `json:"batch"`
	PdfURL  string         `json:"pdfUrl"`
	LeftOut []leftOutJSON  `json:"leftOut"`
}

type batchWaiverJSON struct {
	ReminderID int64    `json:"reminderId"`
	InvoiceID  int64    `json:"invoiceId"`
	Kinds      []string `json:"kinds"`
	Reason     string   `json:"reason"`
}

type postedJSON struct {
	Batch   printBatchJSON    `json:"batch"`
	Skipped []int64           `json:"skipped"`
	Waived  []batchWaiverJSON `json:"waived"`
}

// paperHarness is an installation with reminders on, with set (see
// remindersOn); it sends no e-mail, so every letter is paper.
func paperHarness(t *testing.T, set string, opts ...modtest.Option) *harness {
	t.Helper()
	h := newHarness(t, opts...)
	remindersOn(t, h, set)
	return h
}

// paperLetter plants letter sequence of invoice id, a reminder in
// Norwegian awaiting print, and answers its id.
func paperLetter(t *testing.T, h *harness, id int64, sequence int) int64 {
	t.Helper()
	return plantID(t, h, `
		INSERT INTO invoices.reminders (invoice_id, run_id, sequence, level, channel, recipient, language, created_at,
		    created_by_user_id, status)
		VALUES ($1, $2, $3, 'reminder', 'paper', '', 'nb', now(), gen_random_uuid(), 'awaiting_print')
		RETURNING id`, id, plantRun(t, h), sequence)
}

// paperDue is Acme's invoice number, 1 000 due 3 August and handed over, with
// a reminder awaiting print. It answers the invoice and the letter.
func paperDue(t *testing.T, h *harness, number int64) (invoiceID, letterID int64) {
	t.Helper()
	invoiceID = deliveredOn(t, h, number, customerAcme, "2026-08-03")
	return invoiceID, paperLetter(t, h, invoiceID, 1)
}

// printBody is a print batch's body.
func printBody(postOn string, ids ...int64) map[string]any {
	return map[string]any{"reminderIds": ids, "postOn": postOn}
}

// printed asserts res is a 201 print batch and answers it.
func printed(t *testing.T, what string, res *modtest.Response) batchResultJSON {
	t.Helper()
	if res.Status != http.StatusCreated {
		t.Fatalf("%s = %d %s, want 201", what, res.Status, res.Body)
	}
	var r batchResultJSON
	res.JSON(&r)
	return r
}

// printFor prints ids for postOn as a payer, failing unless 201.
func printFor(t *testing.T, h *harness, postOn string, ids ...int64) batchResultJSON {
	t.Helper()
	return printed(t, "the batch for "+postOn, payer(t, h).Do(http.MethodPost, printBatchesPath, printBody(postOn, ids...)))
}

// postedOn is a posting's body.
func postedOn(day string) map[string]any { return map[string]any{"postedOn": day} }

// posted asserts res is a 200 posting and answers it.
func posted(t *testing.T, what string, res *modtest.Response) postedJSON {
	t.Helper()
	if res.Status != http.StatusOK {
		t.Fatalf("%s = %d %s, want 200", what, res.Status, res.Body)
	}
	var r postedJSON
	res.JSON(&r)
	return r
}

// pdfPage is a PDF's page object: /Type /Page, never /Pages.
var pdfPage = regexp.MustCompile(`/Type\s*/Page[^s]`)

// ratesInForce is the collection rates in force on day, by id — what a
// letter printed for day relies on, as a rate's DELETE judges it.
func ratesInForce(t *testing.T, h *harness, day string) []string {
	t.Helper()
	return texts(t, h, `
		SELECT 'collection_rate ' || r.id FROM invoices.collection_rates r
		WHERE r.valid_from <= $1::date AND NOT EXISTS (SELECT 1 FROM invoices.collection_rates n
		    WHERE n.kind = r.kind AND n.valid_from > r.valid_from AND n.valid_from <= $1::date)
		ORDER BY r.id`, day)
}

// A batch for a later day (D10, amendment 12): Acme's 1 000 fell due on
// Wednesday 2 September; with the first reminder 10 days on, a letter is
// due today, Saturday 12 September, but a fee only from Wednesday 16 (R7's
// 14 days). Printed today for Wednesday, the letter carries Wednesday's
// facts — sent_on the 16th, the deadline 14 days on, the 30th, and the fee
// 38 judged on the 16th — is printed in the batch, and its PDF is stored
// under a key carrying its hash, the one its download answers.
func TestPrintBatch_ForALaterPostOn(t *testing.T) {
	t.Parallel()
	h := paperHarness(t, "first_reminder_days = 10")
	id := deliveredOn(t, h, 1, customerAcme, "2026-09-02")
	letter := paperLetter(t, h, id, 1)
	r := printFor(t, h, "2026-09-16", letter)
	if r.Batch.PostOn != "2026-09-16" || r.Batch.PostedOn != nil || r.Batch.ReprintedAt != nil ||
		r.PdfURL != batchPath(r.Batch.ID, "pdf") || len(r.LeftOut) != 0 || len(r.Batch.Letters) != 1 ||
		r.Batch.Letters[0].ID != letter || r.Batch.Letters[0].Status != "printed" {
		t.Fatalf("the batch = %+v, want letter %d printed for the 16th with its PDF's URL", r, letter)
	}
	row := letterOf(t, h, letter)
	got := fmt.Sprintf("%s batch %v sent_on %s deadline %s fee %s total %s", row.Status, derefID(row.PrintBatchID),
		dayText(row.SentOn), dayText(row.Deadline), amountText(row.Fee), amountText(row.Total))
	want := fmt.Sprintf("printed batch %d sent_on 2026-09-16 deadline 2026-09-30 fee 38.00 total 1038.00", r.Batch.ID)
	if got != want {
		t.Errorf("the letter = %s\nwant          %s", got, want)
	}
	if row.SentAt != nil || row.FirstAttemptAt != nil {
		t.Errorf("sent_at %v first_attempt_at %v; a printed letter is not sent", row.SentAt, row.FirstAttemptAt)
	}
	key, sha := deref(row.PdfObjectKey), deref(row.PdfSha256)
	prefix := fmt.Sprintf("reminders/%d/%d-2026-09-16-", id, letter)
	stored := h.objects.object(key)
	sum := sha256.Sum256(stored)
	if !strings.HasPrefix(key, prefix) || !strings.HasSuffix(key, sha+".pdf") || stored == nil || hex.EncodeToString(sum[:]) != sha {
		t.Fatalf("the PDF's key %q sha %q (%d bytes stored), want %s<sha256>.pdf holding those bytes", key, sha, len(stored), prefix)
	}
	res := reader(t, h).Do(http.MethodGet, letterPath(letter, "pdf"), nil)
	if res.Status != http.StatusOK || string(res.Body) != string(stored) {
		t.Errorf("the letter's PDF = %d (%d bytes), want the stored object", res.Status, len(res.Body))
	}
}

// derefID is a nullable id, 0 for NULL.
func derefID(id *int64) int64 {
	if id == nil {
		return 0
	}
	return *id
}

// The batch's refusals (D10), before anything is written: postOn yesterday
// or eight days on; no letter, 201 letters, one named twice, an id that
// names none — each a 400 on its field; a letter that is not awaiting print
// a 409 reminder_not_awaiting_print naming it; a caller without
// invoices:payments a 403; an installation without an object store a 503.
func TestPrintBatch_Refusals(t *testing.T) {
	t.Parallel()
	h := paperHarness(t, "")
	c := payer(t, h)
	_, letter := paperDue(t, h, 1)
	queued := queueLetter(t, h, deliveredOn(t, h, 2, customerAcme, "2026-08-03"), 1, "reminder", false, "nb")
	many := make([]int64, 201)
	for i := range many {
		many[i] = int64(i + 1)
	}
	for _, tc := range []struct {
		what  string
		body  map[string]any
		field string
	}{
		{"postOn yesterday", printBody("2026-09-11", letter), "postOn"},
		{"postOn eight days on", printBody("2026-09-20", letter), "postOn"},
		{"no letter", printBody("2026-09-12"), "reminderIds"},
		{"201 letters", printBody("2026-09-12", many...), "reminderIds"},
		{"a letter twice", printBody("2026-09-12", letter, letter), "reminderIds"},
		{"no such letter", printBody("2026-09-12", letter, 999999), "reminderIds"},
	} {
		res := c.Do(http.MethodPost, printBatchesPath, tc.body)
		if res.Status != http.StatusBadRequest || !strings.Contains(string(res.Body), `"`+tc.field+`"`) {
			t.Errorf("%s = %d %s, want 400 on %s", tc.what, res.Status, res.Body, tc.field)
		}
	}
	res := c.Do(http.MethodPost, printBatchesPath, printBody("2026-09-12", letter, queued))
	refusedLetter(t, "a queued letter", res, "reminder_not_awaiting_print")
	if !strings.Contains(string(res.Body), fmt.Sprint(queued)) {
		t.Errorf("the refusal %s does not name letter %d", res.Body, queued)
	}
	if res := reader(t, h).Do(http.MethodPost, printBatchesPath, printBody("2026-09-12", letter)); res.Status != http.StatusForbidden {
		t.Errorf("a reader's batch = %d, want 403", res.Status)
	}
	if n := h.Count(t, `SELECT count(*) FROM invoices.reminder_print_batches`); n != 0 {
		t.Errorf("%d batches were written, want none", n)
	}
	if r := letterOf(t, h, letter); r.Status != "awaiting_print" || r.SentOn.Valid {
		t.Errorf("the letter = %s sent_on %s, want awaiting print, untouched", r.Status, dayText(r.SentOn))
	}
	if r := printFor(t, h, "2026-09-19", letter); len(r.Batch.Letters) != 1 {
		t.Errorf("the batch seven days on = %+v, want the letter printed", r)
	}

	bare := newHarnessWithoutStore(t)
	remindersOn(t, bare, "")
	_, unstored := paperDue(t, bare, 1)
	res = payer(t, bare).Do(http.MethodPost, printBatchesPath, printBody("2026-09-12", unstored))
	if res.Status != http.StatusServiceUnavailable || !strings.Contains(string(res.Body), "storage_unavailable") ||
		bare.Count(t, `SELECT count(*) FROM invoices.reminder_print_batches`) != 0 {
		t.Errorf("a batch without an object store = %d %s, want 503 storage_unavailable, nothing written", res.Status, res.Body)
	}
}

// The letters a batch leaves out (D10, m9), on Monday 28 December 2026 for
// Friday 1 January 2027: a fee letter past the regime review (31 December)
// is left out collection_regime_unreviewed and stays awaiting print with no
// facts; with the review moved and late interest on, the same letter needs
// 2027-H1's interest rate, which has no row, and is left out
// collection_rates_outdated naming it; a letter withdrawn while the batch
// waited on its invoice is left out not_awaiting_print, still withdrawn; one
// whose invoice was paid is withdrawn settled by the batch's re-judge and
// left out with that reason.
func TestPrintBatch_LettersLeftOut(t *testing.T) {
	h := paperHarness(t, "")
	fee := deliveredOn(t, h, 1, customerAcme, "2026-11-02")
	feeLetter := paperLetter(t, h, fee, 1)
	gone := deliveredOn(t, h, 2, customerAcme, "2026-11-02")
	goneLetter := paperLetter(t, h, gone, 1)
	paid := deliveredOn(t, h, 3, customerAcme, "2026-11-02")
	paidLetter := paperLetter(t, h, paid, 1)
	moveClockTo(t, h, time.Date(2026, time.December, 28, 9, 0, 0, 0, time.UTC))

	r := printFor(t, h, "2027-01-01", feeLetter)
	if len(r.Batch.Letters) != 0 || len(r.LeftOut) != 1 || r.LeftOut[0].ReminderID != feeLetter ||
		r.LeftOut[0].InvoiceID != fee || r.LeftOut[0].Reason != "collection_regime_unreviewed" {
		t.Errorf("past the review the batch = %+v, want the fee letter left out collection_regime_unreviewed", r)
	}
	if row := letterOf(t, h, feeLetter); row.Status != "awaiting_print" || !factsCleared(row) || row.PrintBatchID != nil {
		t.Errorf("the fee letter = %s cleared %v batch %v, want awaiting print without facts", row.Status, factsCleared(row), row.PrintBatchID)
	}

	h.Exec(t, `UPDATE invoices.reminder_settings SET regime_reviewed_through = DATE '2027-06-30', late_interest = true`)
	registered(t, h, paid, pay(1000, "2026-12-28"))
	restore := invoices.SetLockTaken(func(_ context.Context, what, key string) {
		if what == "invoice" && key == idKey(gone) {
			h.Exec(t, `UPDATE invoices.reminders SET status = 'withdrawn', withdrawn_at = now(), withdrawn_by_user_id = gen_random_uuid(),
				withdrawal_reason = 'Kunden ringte' WHERE id = $1`, goneLetter)
		}
	})
	r = printFor(t, h, "2027-01-01", feeLetter, goneLetter, paidLetter)
	restore()
	left := map[int64]leftOutJSON{}
	for _, l := range r.LeftOut {
		left[l.ReminderID] = l
	}
	if l := left[feeLetter]; l.Reason != "collection_rates_outdated" || l.Outdated == nil ||
		l.Outdated.Kind != "late_interest_percent" || l.Outdated.HalfYear != "2027-H1" {
		t.Errorf("the fee letter left out = %+v, want collection_rates_outdated naming late_interest_percent 2027-H1", l)
	}
	if l := left[goneLetter]; l.Reason != "not_awaiting_print" || l.InvoiceID != gone {
		t.Errorf("the letter withdrawn meanwhile left out = %+v, want not_awaiting_print", l)
	}
	if l := left[paidLetter]; l.Reason != "settled" {
		t.Errorf("the paid invoice's letter left out = %+v, want settled", l)
	}
	if len(r.Batch.Letters) != 0 || len(r.LeftOut) != 3 {
		t.Errorf("the batch = %+v, want nothing printed and three letters left out", r)
	}
	if row := letterOf(t, h, feeLetter); row.Status != "awaiting_print" || !factsCleared(row) {
		t.Errorf("the fee letter = %s, want still awaiting print without facts", row.Status)
	}
	if s := letterStateOf(t, h, goneLetter); s.status != "withdrawn" || s.reason != "Kunden ringte" || !s.byUser {
		t.Errorf("the letter withdrawn meanwhile = %+v, want the person's withdrawal kept", s)
	}
	if s := letterStateOf(t, h, paidLetter); s.status != "withdrawn" || s.reason != "settled" || s.byUser {
		t.Errorf("the paid invoice's letter = %+v, want withdrawn settled by no one", s)
	}
}

// The combined PDF (D10), as often as it is asked for: the same bytes, one
// letter on each of its own pages, each laid out from its row, private and
// never cached; a batch that does not exist is a 404.
func TestPrintBatch_Redownload(t *testing.T) {
	h := paperHarness(t, "")
	_, a := paperDue(t, h, 1)
	_, b := paperDue(t, h, 2)
	r := printFor(t, h, "2026-09-12", a, b)
	var mu sync.Mutex
	var laidOut []int64
	defer invoices.SetReminderPDFModelText(func(id int64, _ []string) {
		mu.Lock()
		defer mu.Unlock()
		laidOut = append(laidOut, id)
	})()
	c := payer(t, h)
	first := c.Do(http.MethodGet, r.PdfURL, nil)
	second := c.Do(http.MethodGet, r.PdfURL, nil)
	want := fmt.Sprintf(`attachment; filename="paper-letters-%d-2026-09-12.pdf"`, r.Batch.ID)
	for _, res := range []*modtest.Response{first, second} {
		if res.Status != http.StatusOK || res.Header("Content-Type") != "application/pdf" ||
			res.Header("Cache-Control") != "private, no-store" || res.Header("Content-Disposition") != want {
			t.Fatalf("the batch's PDF = %d %s %q %q, want a PDF named %s, never cached", res.Status, res.Header("Content-Type"),
				res.Header("Cache-Control"), res.Header("Content-Disposition"), want)
		}
	}
	if string(first.Body) != string(second.Body) {
		t.Error("the two downloads differ, want the same bytes")
	}
	if n := len(pdfPage.FindAll(first.Body, -1)); n != 2 {
		t.Errorf("the PDF has %d pages, want one for each letter", n)
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(laidOut, []int64{a, b, a, b}) {
		t.Errorf("laid out %v, want each letter, in order, on each download", laidOut)
	}
	if res := c.Do(http.MethodGet, batchPath(999999, "pdf"), nil); res.Status != http.StatusNotFound {
		t.Errorf("the PDF of no batch = %d, want 404", res.Status)
	}
}

// Posted on its postOn (D10): every printed letter sent, sent_at the
// confirmation's time, its facts and PDF those it was printed with; the
// batch posted that day, by the person, at that time; nothing skipped or
// waived.
func TestPrintBatch_PostedOnPostOn(t *testing.T) {
	t.Parallel()
	h := paperHarness(t, "")
	_, letter := paperDue(t, h, 1)
	r := printFor(t, h, "2026-09-12", letter)
	before := letterOf(t, h, letter)
	c, by := h.SignInUser(t, "invoices:access", "invoices:payments")
	p := posted(t, "the posting", c.Do(http.MethodPost, batchPath(r.Batch.ID, "posted"), postedOn("2026-09-12")))
	if p.Batch.PostedOn == nil || *p.Batch.PostedOn != "2026-09-12" || p.Batch.PostedBy == nil || *p.Batch.PostedBy != by.String() ||
		p.Batch.PostedAt == nil || len(p.Skipped) != 0 || len(p.Waived) != 0 || len(p.Batch.Letters) != 1 ||
		p.Batch.Letters[0].Status != "sent" {
		t.Fatalf("the posting = %+v, want the batch posted today by %s, its letter sent, nothing skipped or waived", p, by)
	}
	row := letterOf(t, h, letter)
	if row.Status != "sent" || row.SentAt == nil || !row.SentAt.Equal(h.Now()) || dayText(row.SentOn) != "2026-09-12" ||
		amountText(row.Fee) != "38.00" || deref(row.PdfObjectKey) != deref(before.PdfObjectKey) {
		t.Errorf("the letter = %s sent_at %v sent_on %s fee %s key %s; want sent now with its printed facts and PDF",
			row.Status, row.SentAt, dayText(row.SentOn), amountText(row.Fee), deref(row.PdfObjectKey))
	}
	if res := reader(t, h).Do(http.MethodGet, letterPath(letter, "pdf"), nil); res.Status != http.StatusOK {
		t.Errorf("the sent letter's PDF = %d, want 200", res.Status)
	}
}

// Posted on another day (D10, NB1, reading 39): a batch printed for Tuesday
// 15 September confirmed posted on Saturday the 12th is 409
// reminder_posted_early; confirmed for the 15th while it is still the 12th
// is a 400 — no day to come is posted; on Wednesday the 16th, posted on the
// 16th is 409 reminder_posted_late — nothing written each time. The reprint
// returns the letter to awaiting print with its facts, its batch and its
// PDF key cleared, and the batch is closed: neither reprinted nor posted
// again. A new batch for the 16th prints the letter with the 16th's facts
// under a new key, and is posted that day — then closed too.
func TestPrintBatch_PostedEarlyOrLateAndReprint(t *testing.T) {
	t.Parallel()
	h := paperHarness(t, "")
	_, letter := paperDue(t, h, 1)
	r := printFor(t, h, "2026-09-15", letter)
	c := payer(t, h)
	post := func(id int64, day string) *modtest.Response {
		return c.Do(http.MethodPost, batchPath(id, "posted"), postedOn(day))
	}
	refusedLetter(t, "posted early", post(r.Batch.ID, "2026-09-12"), "reminder_posted_early")
	if res := post(r.Batch.ID, "2026-09-15"); res.Status != http.StatusBadRequest || !strings.Contains(string(res.Body), `"postedOn"`) {
		t.Errorf("posted on its postOn, a day to come = %d %s, want 400 on postedOn", res.Status, res.Body)
	}
	moveClockTo(t, h, time.Date(2026, time.September, 16, 8, 0, 0, 0, time.UTC))
	c = payer(t, h) // a session of the new day
	refusedLetter(t, "posted late", post(r.Batch.ID, "2026-09-16"), "reminder_posted_late")
	printedRow := letterOf(t, h, letter)
	if printedRow.Status != "printed" || dayText(printedRow.SentOn) != "2026-09-15" {
		t.Fatalf("after the refusals the letter = %s sent_on %s, want printed for the 15th", printedRow.Status, dayText(printedRow.SentOn))
	}

	res := c.Do(http.MethodPost, batchPath(r.Batch.ID, "reprint"), nil)
	if res.Status != http.StatusOK {
		t.Fatalf("the reprint = %d %s, want 200", res.Status, res.Body)
	}
	var reprinted printBatchJSON
	res.JSON(&reprinted)
	if reprinted.ReprintedAt == nil || len(reprinted.Letters) != 0 || reprinted.PostedOn != nil {
		t.Errorf("the reprinted batch = %+v, want reprinted, no letter naming it", reprinted)
	}
	row := letterOf(t, h, letter)
	if row.Status != "awaiting_print" || !factsCleared(row) || row.PdfSha256 != nil || row.PrintBatchID != nil ||
		row.Fee.Valid || row.ChargesEarlier.Valid || row.Interest.Valid || row.InterestFrom.Valid || row.Inkassosats.Valid {
		t.Errorf("the reprinted letter = %s cleared %v sha %v batch %v fee %s; want awaiting print, every fact, the batch and the PDF cleared",
			row.Status, factsCleared(row), row.PdfSha256, row.PrintBatchID, amountText(row.Fee))
	}
	refusedLetter(t, "a second reprint", c.Do(http.MethodPost, batchPath(r.Batch.ID, "reprint"), nil), "print_batch_closed")
	refusedLetter(t, "a reprinted batch posted", post(r.Batch.ID, "2026-09-15"), "print_batch_closed")

	again := printFor(t, h, "2026-09-16", letter)
	row = letterOf(t, h, letter)
	if row.Status != "printed" || derefID(row.PrintBatchID) != again.Batch.ID || dayText(row.SentOn) != "2026-09-16" ||
		dayText(row.Deadline) != "2026-09-30" || row.PdfObjectKey == nil || *row.PdfObjectKey == deref(printedRow.PdfObjectKey) {
		t.Errorf("printed again = %s batch %v sent_on %s deadline %s key %s; want the new batch's, the 16th's facts, a new key",
			row.Status, row.PrintBatchID, dayText(row.SentOn), dayText(row.Deadline), deref(row.PdfObjectKey))
	}
	p := posted(t, "the new batch posted on its day", post(again.Batch.ID, "2026-09-16"))
	if len(p.Batch.Letters) != 1 || p.Batch.Letters[0].Status != "sent" {
		t.Errorf("the posting = %+v, want the letter sent", p)
	}
	refusedLetter(t, "a posted batch posted again", post(again.Batch.ID, "2026-09-16"), "print_batch_closed")
	refusedLetter(t, "a posted batch reprinted", c.Do(http.MethodPost, batchPath(again.Batch.ID, "reprint"), nil), "print_batch_closed")
	for _, path := range []string{batchPath(999999, "posted"), batchPath(999999, "reprint")} {
		body := any(nil)
		if strings.HasSuffix(path, "posted") {
			body = postedOn("2026-09-16")
		}
		if res := c.Do(http.MethodPost, path, body); res.Status != http.StatusNotFound {
			t.Errorf("POST %s = %d, want 404", path, res.Status)
		}
	}
}

// One letter named by two batches at once (D10): the first, held right
// after it locked the letter, prints it; the second waits on the invoice
// the first holds (pg_blocking_pids), then finds the letter printed and
// leaves it out not_awaiting_print — never a 409 once its batch row exists
// — with no deadlock.
func TestPrintBatch_ALetterInTwoBatches(t *testing.T) {
	h := paperHarness(t, "", modtest.WithPoolMaxConns(2))
	id, letter := paperDue(t, h, 1)
	probeConn := ownConn(t, h)
	before := deadlocks(t, probeConn)
	parked, release := parkFirstLock(t, "reminder")

	firstDone := startRequest(payer(t, h), http.MethodPost, printBatchesPath, printBody("2026-09-12", letter))
	waitFor(t, "the first batch's letter lock", parked)
	firstPID := idleInTransaction(t, probeConn)
	if got := heldMode(t, probeConn, "invoices.invoices", "id = $1", id); got != modeUpdate {
		t.Errorf("the invoice is held %q, want FOR UPDATE", got)
	}
	if got := heldMode(t, probeConn, "invoices.reminders", "id = $1", letter); got != modeNoKeyUpdate {
		t.Errorf("the letter is held %q, want FOR NO KEY UPDATE", got)
	}
	secondDone := startRequest(payer(t, h), http.MethodPost, printBatchesPath, printBody("2026-09-12", letter))
	waiter := newWaiter(t, probeConn)
	if got := blockersOf(t, probeConn, waiter); !slices.Equal(got, []uint32{firstPID}) {
		t.Errorf("the second batch waits on %v, want the first %d", got, firstPID)
	}
	close(release)
	var first, second batchResultJSON
	finished(t, "the first batch", firstDone, http.StatusCreated).JSON(&first)
	finished(t, "the second batch", secondDone, http.StatusCreated).JSON(&second)
	if len(first.Batch.Letters) != 1 || len(first.LeftOut) != 0 {
		t.Errorf("the first batch = %+v, want the letter printed", first)
	}
	if len(second.Batch.Letters) != 0 || len(second.LeftOut) != 1 || second.LeftOut[0].Reason != "not_awaiting_print" {
		t.Errorf("the second batch = %+v, want the letter left out not_awaiting_print", second)
	}
	if row := letterOf(t, h, letter); derefID(row.PrintBatchID) != first.Batch.ID {
		t.Errorf("the letter is in batch %v, want the first's %d", row.PrintBatchID, first.Batch.ID)
	}
	if after := deadlocks(t, probeConn); after != before {
		t.Errorf("Postgres broke %d deadlock(s)", after-before)
	}
}

// A printed letter withdrawn by hand (D10, plan reading 9): absent from the
// combined PDF, skipped by the posting and listed, never sent; the other
// letter is sent.
func TestPrintBatch_WithdrawnLettersSkipped(t *testing.T) {
	h := paperHarness(t, "")
	_, kept := paperDue(t, h, 1)
	_, pulled := paperDue(t, h, 2)
	r := printFor(t, h, "2026-09-12", kept, pulled)
	c := payer(t, h)
	letterAnswer(t, "the withdrawal", c.Do(http.MethodPost, letterPath(pulled, "withdraw"), map[string]any{"reason": "Lagt i feil konvolutt"}))
	var laidOut []int64
	restore := invoices.SetReminderPDFModelText(func(id int64, _ []string) { laidOut = append(laidOut, id) })
	res := c.Do(http.MethodGet, r.PdfURL, nil)
	restore()
	if res.Status != http.StatusOK || !slices.Equal(laidOut, []int64{kept}) || len(pdfPage.FindAll(res.Body, -1)) != 1 {
		t.Errorf("the batch's PDF = %d laid out %v, want the kept letter alone", res.Status, laidOut)
	}
	p := posted(t, "the posting", c.Do(http.MethodPost, batchPath(r.Batch.ID, "posted"), postedOn("2026-09-12")))
	if !slices.Equal(p.Skipped, []int64{pulled}) || len(p.Waived) != 0 {
		t.Errorf("the posting skipped %v waived %+v, want the withdrawn letter skipped", p.Skipped, p.Waived)
	}
	if s := letterStateOf(t, h, pulled); s.status != "withdrawn" || s.reason != "Lagt i feil konvolutt" {
		t.Errorf("the withdrawn letter = %+v, want still withdrawn", s)
	}
	if row := letterOf(t, h, kept); row.Status != "sent" {
		t.Errorf("the kept letter = %s, want sent", row.Status)
	}
}

// The posted re-judge (NI4): four letters printed with the fee 38 each;
// between printing and posting one invoice is paid in full, one put on hold
// and one handed off to collection — each leaves its printed letter alone
// (plan reading 9). Posted on its day: all four sent; the three changed
// letters' fees waived claimed_in_error in the posting's transaction by the
// person posting, and listed with what changed; the fourth's fee stands.
func TestPrintBatch_PostedRejudges(t *testing.T) {
	t.Parallel()
	h := paperHarness(t, "")
	paid, paidLetter := paperDue(t, h, 1)
	held, heldLetter := paperDue(t, h, 2)
	handed, handedLetter := paperDue(t, h, 3)
	_, plainLetter := paperDue(t, h, 4)
	r := printFor(t, h, "2026-09-12", paidLetter, heldLetter, handedLetter, plainLetter)
	if len(r.Batch.Letters) != 4 {
		t.Fatalf("the batch = %+v, want four letters printed", r)
	}
	registered(t, h, paid, pay(1000, "2026-09-12"))
	answered(t, "the hold", payer(t, h).Do(http.MethodPost, holdPath(held), holdNote("Kunden bestrider timene")))
	answered(t, "the hand-off", payer(t, h).Do(http.MethodPost, handoffPath(handed), handoffBody("2026-09-12", "Inkasso AS")))

	c, by := h.SignInUser(t, "invoices:access", "invoices:payments")
	p := posted(t, "the posting", c.Do(http.MethodPost, batchPath(r.Batch.ID, "posted"), postedOn("2026-09-12")))
	waived := map[int64]batchWaiverJSON{}
	for _, w := range p.Waived {
		waived[w.ReminderID] = w
	}
	for _, tc := range []struct {
		letter, invoice int64
		reason          string
	}{{paidLetter, paid, "settled"}, {heldLetter, held, "on_hold"}, {handedLetter, handed, "handed_off"}} {
		if w := waived[tc.letter]; w.Reason != tc.reason || w.InvoiceID != tc.invoice || !slices.Equal(w.Kinds, []string{"fee"}) {
			t.Errorf("letter %d's waiver = %+v, want its fee waived, %s", tc.letter, w, tc.reason)
		}
		if got := letterWaivers(t, h, tc.letter); !slices.Equal(got, []string{"fee claimed_in_error"}) {
			t.Errorf("letter %d's waivers = %v, want its fee waived claimed_in_error", tc.letter, got)
		}
		if n := h.Count(t, `SELECT count(*) FROM invoices.charge_waivers WHERE reminder_id = $1 AND waived_by_user_id = $2`,
			tc.letter, by); n != 1 {
			t.Errorf("letter %d's waiver was not the posting person's", tc.letter)
		}
	}
	if len(p.Waived) != 3 || len(letterWaivers(t, h, plainLetter)) != 0 {
		t.Errorf("waived %+v and the unchanged letter's %v, want the three changed letters alone", p.Waived, letterWaivers(t, h, plainLetter))
	}
	for _, l := range []int64{paidLetter, heldLetter, handedLetter, plainLetter} {
		if row := letterOf(t, h, l); row.Status != "sent" || amountText(row.Fee) != "38.00" {
			t.Errorf("letter %d = %s fee %s, want sent with the fee it printed", l, row.Status, amountText(row.Fee))
		}
	}
}

// A printed letter is in flight (D8): the overdue list blocks its invoice
// letter_pending, and a run over it skips it action_changed.
func TestPrintBatch_APrintedLetterBlocksARun(t *testing.T) {
	t.Parallel()
	h := paperHarness(t, "")
	id, letter := paperDue(t, h, 1)
	printFor(t, h, "2026-09-12", letter)
	got := overdueOf(t, reader(t, h), "")
	i := slices.IndexFunc(got.Items, func(it overdueItemJSON) bool { return it.InvoiceID == id })
	if i < 0 || got.Items[i].NextAction.Action != "blocked" || !slices.Contains(got.Items[i].NextAction.Reasons, "letter_pending") {
		t.Fatalf("the overdue list = %+v, want the invoice blocked letter_pending", got.Items)
	}
	r := made(t, "the run", run(payer(t, h), runBody(yes, item(id, "reminder"))))
	if len(r.Created) != 0 || len(r.Skipped) != 1 || r.Skipped[0].Reason != "action_changed" || letterRows(t, h, id) != 1 {
		t.Errorf("the run = %+v, want the invoice skipped action_changed and no letter made", r)
	}
}

// The list (plan reading 12): the batches newest first, each with its
// letters; posted true the posted ones, false the open ones — neither posted
// nor reprinted — absent all; paged; invoices:payments' only.
func TestPrintBatch_List(t *testing.T) {
	t.Parallel()
	h := paperHarness(t, "")
	_, a := paperDue(t, h, 1)
	_, b := paperDue(t, h, 2)
	_, c := paperDue(t, h, 3)
	open := printFor(t, h, "2026-09-14", a)
	done := printFor(t, h, "2026-09-12", b)
	redo := printFor(t, h, "2026-09-13", c)
	cl := payer(t, h)
	posted(t, "the posting", cl.Do(http.MethodPost, batchPath(done.Batch.ID, "posted"), postedOn("2026-09-12")))
	if res := cl.Do(http.MethodPost, batchPath(redo.Batch.ID, "reprint"), nil); res.Status != http.StatusOK {
		t.Fatalf("the reprint = %d %s", res.Status, res.Body)
	}
	list := func(query string) ([]int64, int) {
		t.Helper()
		res := cl.Do(http.MethodGet, printBatchesPath+query, nil)
		if res.Status != http.StatusOK {
			t.Fatalf("GET %s = %d %s", query, res.Status, res.Body)
		}
		var got struct {
			Data       []printBatchJSON `json:"data"`
			Pagination struct {
				TotalCount int `json:"totalCount"`
			} `json:"pagination"`
		}
		res.JSON(&got)
		var ids []int64
		for _, b := range got.Data {
			ids = append(ids, b.ID)
			if b.ID == open.Batch.ID && (len(b.Letters) != 1 || b.Letters[0].ID != a) {
				t.Errorf("the open batch's letters = %+v, want letter %d", b.Letters, a)
			}
		}
		return ids, got.Pagination.TotalCount
	}
	if ids, n := list(""); !slices.Equal(ids, []int64{redo.Batch.ID, done.Batch.ID, open.Batch.ID}) || n != 3 {
		t.Errorf("all = %v (%d), want the three newest first", ids, n)
	}
	if ids, n := list("?posted=true"); !slices.Equal(ids, []int64{done.Batch.ID}) || n != 1 {
		t.Errorf("posted = %v (%d), want the posted batch", ids, n)
	}
	if ids, n := list("?posted=false"); !slices.Equal(ids, []int64{open.Batch.ID}) || n != 1 {
		t.Errorf("open = %v (%d), want the batch neither posted nor reprinted", ids, n)
	}
	if ids, n := list("?pageSize=1&page=2"); !slices.Equal(ids, []int64{done.Batch.ID}) || n != 3 {
		t.Errorf("page 2 of 1 = %v (%d), want the second newest", ids, n)
	}
	if res := cl.Do(http.MethodGet, printBatchesPath+"?page=0", nil); res.Status != http.StatusBadRequest {
		t.Errorf("page 0 = %d, want 400", res.Status)
	}
	if res := reader(t, h).Do(http.MethodGet, printBatchesPath, nil); res.Status != http.StatusForbidden {
		t.Errorf("a reader's list = %d, want 403", res.Status)
	}
}

// The lock order (D18): a batch's letter takes its batch FOR SHARE, its
// invoice, then the letter, then the rates in force on postOn — and reads
// the engine's rates only after that share; the posting takes the batch,
// its letters' invoices in descending id, then the letters; the reprint the
// batch, then its printed letters — and no invoice.
func TestPrintBatch_LockOrder(t *testing.T) {
	h := paperHarness(t, "")
	x, lx := paperDue(t, h, 1)
	y, ly := paperDue(t, h, 2)
	seen := &lockSeen{}
	defer invoices.SetLockTaken(seen.note)()
	defer invoices.SetRatesRead(func(ctx context.Context) { seen.note(ctx, "rates", "read") })()
	r := printFor(t, h, "2026-09-12", lx, ly)
	rates := ratesInForce(t, h, "2026-09-12")
	batch := "print_batch " + idKey(r.Batch.ID)
	want := append(append([]string{batch, "invoice " + idKey(x), "reminder " + idKey(lx)}, rates...), "rates read",
		batch, "invoice "+idKey(y), "reminder "+idKey(ly))
	want = append(want, rates...)
	want = append(want, "rates read")
	if got := seen.take(); !slices.Equal(got, want) || len(rates) != 3 {
		t.Errorf("the batch locked %v, want %v", got, want)
	}
	posted(t, "the posting", payer(t, h).Do(http.MethodPost, batchPath(r.Batch.ID, "posted"), postedOn("2026-09-12")))
	want = []string{"print_batch " + idKey(r.Batch.ID), "invoice " + idKey(y), "invoice " + idKey(x),
		"reminder " + idKey(lx), "rates read", "reminder " + idKey(ly), "rates read"}
	if got := seen.take(); !slices.Equal(got, want) {
		t.Errorf("the posting locked %v, want %v", got, want)
	}

	_, lz := paperDue(t, h, 3)
	_, lw := paperDue(t, h, 4)
	again := printFor(t, h, "2026-09-13", lz, lw)
	seen.take()
	if res := payer(t, h).Do(http.MethodPost, batchPath(again.Batch.ID, "reprint"), nil); res.Status != http.StatusOK {
		t.Fatalf("the reprint = %d %s", res.Status, res.Body)
	}
	want = []string{"print_batch " + idKey(again.Batch.ID), "reminder " + idKey(lz), "reminder " + idKey(lw)}
	if got := seen.take(); !slices.Equal(got, want) {
		t.Errorf("the reprint locked %v, want %v", got, want)
	}
}

// No call leaves the module from inside one of its transactions (MB rule
// 10): the batch stores each letter's PDF after its letter's transaction
// committed — the store's two calls per letter, none under a lock — and the
// combined PDF, the posting and the reprint make no call at all.
func TestPrintBatch_NoCallUnderALock(t *testing.T) {
	h := paperHarness(t, "")
	_, a := paperDue(t, h, 1)
	_, b := paperDue(t, h, 2)
	before := lockedContractCalls.count()
	all := len(contractCalls.byMethod("ObjectStore.Exists", "ObjectStore.Put", "ObjectStore.Get"))
	r := printFor(t, h, "2026-09-12", a, b)
	c := payer(t, h)
	if res := c.Do(http.MethodGet, r.PdfURL, nil); res.Status != http.StatusOK {
		t.Fatalf("the batch's PDF = %d", res.Status)
	}
	posted(t, "the posting", c.Do(http.MethodPost, batchPath(r.Batch.ID, "posted"), postedOn("2026-09-12")))
	_, z := paperDue(t, h, 3)
	again := printFor(t, h, "2026-09-13", z)
	if res := c.Do(http.MethodPost, batchPath(again.Batch.ID, "reprint"), nil); res.Status != http.StatusOK {
		t.Fatalf("the reprint = %d %s", res.Status, res.Body)
	}
	if calls := lockedContractCalls.since(before); len(calls) > 0 {
		t.Errorf("calls made inside a transaction:\n%s", strings.Join(calls, "\n"))
	}
	var methods []string
	for _, call := range contractCalls.byMethod("ObjectStore.Exists", "ObjectStore.Put", "ObjectStore.Get")[all:] {
		methods = append(methods, call.method)
		if call.locked {
			t.Errorf("%s was made under a lock", call.method)
		}
	}
	want := []string{"ObjectStore.Exists", "ObjectStore.Put", "ObjectStore.Exists", "ObjectStore.Put", "ObjectStore.Exists", "ObjectStore.Put"}
	if !slices.Equal(methods, want) {
		t.Errorf("the batches' calls = %v, want the store's two per letter printed, and nothing else", methods)
	}
}

// TestPrintBatch_RacesRateDelete: a user's inkassosats of 800 from Tuesday
// 15 September, not yet in force, which a letter printed for Wednesday the
// 16th relies on (the fee 40). The batch held right after it read the rates
// in force on the 16th FOR KEY SHARE: the rate's DELETE — FOR UPDATE first —
// waits on the batch (pg_blocking_pids); the batch prints the letter with
// the fee 40 and commits; the DELETE then sees a printed letter relying on
// the row and is refused collection_rate_in_force (plan reading 6) — no
// deadlock.
func TestPrintBatch_RacesRateDelete(t *testing.T) {
	h := paperHarness(t, "", modtest.WithPoolMaxConns(2))
	_, letter := paperDue(t, h, 1)
	rate := plantID(t, h, `INSERT INTO invoices.collection_rates (kind, valid_from, value, source_ref, created_by_user_id, created_at)
		VALUES ('inkassosats', DATE '2026-09-15', 800, 'FOR-2026-09-01-1', gen_random_uuid(), now()) RETURNING id`)
	probeConn := ownConn(t, h)
	before := deadlocks(t, probeConn)
	parked, release := parkFirstLock(t, "collection_rate")

	batchDone := startRequest(payer(t, h), http.MethodPost, printBatchesPath, printBody("2026-09-16", letter))
	waitFor(t, "the batch's rates", parked)
	batchPID := idleInTransaction(t, probeConn)
	if got := heldMode(t, probeConn, "invoices.collection_rates", "id = $1", rate); got != modeKeyShare {
		t.Errorf("the rate is held %q, want FOR KEY SHARE", got)
	}
	deleteDone := startRequest(manager(t, h), http.MethodDelete, fmt.Sprintf("%s/%d", collectionRatesPath, rate), nil)
	waiter := newWaiter(t, probeConn)
	if got := blockersOf(t, probeConn, waiter); !slices.Equal(got, []uint32{batchPID}) {
		t.Errorf("the DELETE waits on %v, want the batch %d", got, batchPID)
	}
	close(release)
	var r batchResultJSON
	finished(t, "the batch", batchDone, http.StatusCreated).JSON(&r)
	refusedLetter(t, "the DELETE", finished(t, "the DELETE", deleteDone, http.StatusConflict), "collection_rate_in_force")
	if row := letterOf(t, h, letter); row.Status != "printed" || amountText(row.Fee) != "40.00" || amountText(row.Inkassosats) != "800.00" {
		t.Errorf("the letter = %s fee %s inkassosats %s, want printed on the user's 800", row.Status, amountText(row.Fee), amountText(row.Inkassosats))
	}
	if n := h.Count(t, `SELECT count(*) FROM invoices.collection_rates WHERE id = $1`, rate); n != 1 {
		t.Error("the rate a printed letter relies on was deleted")
	}
	if after := deadlocks(t, probeConn); after != before {
		t.Errorf("Postgres broke %d deadlock(s)", after-before)
	}
}

// TestPrintBatch_PostedRacesHold: the posting and a hold of one of its
// letters' invoices. The posting held right after it locked the batch FOR
// NO KEY UPDATE (the batch's letters' key-share locks never conflict with
// it, m11), the invoice not yet locked: the hold goes through, leaving the
// printed letter; the posting then locks the invoice, judges the letter
// held and waives its fee claimed_in_error. A raw transaction holding the
// invoice FOR UPDATE: the posting waits on it after taking the batch, and
// finishes once it is released. No deadlock either way.
func TestPrintBatch_PostedRacesHold(t *testing.T) {
	t.Run("a hold while the posting holds the batch", func(t *testing.T) {
		h := paperHarness(t, "", modtest.WithPoolMaxConns(2))
		id, letter := paperDue(t, h, 1)
		r := printFor(t, h, "2026-09-12", letter)
		probeConn := ownConn(t, h)
		before := deadlocks(t, probeConn)
		var hits atomic.Int32
		parked, released := make(chan struct{}, 1), make(chan struct{})
		defer invoices.SetPostedAfterBatchLock(func(context.Context, int64) error {
			hits.Add(1)
			parked <- struct{}{}
			select {
			case <-released:
			case <-time.After(30 * time.Second):
			}
			return nil
		})()

		postDone := startRequest(payer(t, h), http.MethodPost, batchPath(r.Batch.ID, "posted"), postedOn("2026-09-12"))
		waitFor(t, "the posting's batch lock", parked)
		if got := heldMode(t, probeConn, "invoices.reminder_print_batches", "id = $1", r.Batch.ID); got != modeNoKeyUpdate {
			t.Errorf("the batch is held %q, want FOR NO KEY UPDATE", got)
		}
		if got := heldMode(t, probeConn, "invoices.invoices", "id = $1", id); got != "" {
			t.Errorf("the invoice is held %q before the posting locks it, want free", got)
		}
		held := answered(t, "the hold", payer(t, h).Do(http.MethodPost, holdPath(id), holdNote("Kunden bestrider timene")))
		if len(held.LettersLeft) != 1 || held.LettersLeft[0].ReminderID != letter || held.LettersLeft[0].Status != "printed" {
			t.Errorf("lettersLeft = %+v, want the printed letter", held.LettersLeft)
		}
		close(released)
		var p postedJSON
		finished(t, "the posting", postDone, http.StatusOK).JSON(&p)
		if len(p.Waived) != 1 || p.Waived[0].Reason != "on_hold" || hits.Load() != 1 {
			t.Errorf("the posting waived %+v, want the letter's fee, on_hold", p.Waived)
		}
		if row := letterOf(t, h, letter); row.Status != "sent" {
			t.Errorf("the letter = %s, want sent", row.Status)
		}
		if after := deadlocks(t, probeConn); after != before {
			t.Errorf("Postgres broke %d deadlock(s)", after-before)
		}
	})

	t.Run("the posting waits on a held invoice", func(t *testing.T) {
		h := paperHarness(t, "", modtest.WithPoolMaxConns(2))
		id, letter := paperDue(t, h, 1)
		r := printFor(t, h, "2026-09-12", letter)
		probeConn := ownConn(t, h)
		before := deadlocks(t, probeConn)
		raw := holdRow(t, h, `SELECT 1 FROM invoices.invoices WHERE id = $1 FOR UPDATE`, id)

		postDone := startRequest(payer(t, h), http.MethodPost, batchPath(r.Batch.ID, "posted"), postedOn("2026-09-12"))
		waiter := newWaiter(t, probeConn, raw.pid)
		if got := blockersOf(t, probeConn, waiter); !slices.Equal(got, []uint32{raw.pid}) {
			t.Errorf("the posting waits on %v, want the raw lock %d", got, raw.pid)
		}
		if got := heldMode(t, probeConn, "invoices.reminder_print_batches", "id = $1", r.Batch.ID); got != modeNoKeyUpdate {
			t.Errorf("the waiting posting holds the batch %q, want FOR NO KEY UPDATE", got)
		}
		raw.release(t)
		finished(t, "the posting", postDone, http.StatusOK)
		if row := letterOf(t, h, letter); row.Status != "sent" {
			t.Errorf("the letter = %s, want sent", row.Status)
		}
		if after := deadlocks(t, probeConn); after != before {
			t.Errorf("Postgres broke %d deadlock(s)", after-before)
		}
	})
}

// TestPrintBatch_ReprintTakesNoInvoice: a raw transaction holds a printed
// letter's invoice FOR UPDATE; the reprint, which takes the batch and the
// letter alone (D18), returns the letter to awaiting print without waiting
// on it.
func TestPrintBatch_ReprintTakesNoInvoice(t *testing.T) {
	h := paperHarness(t, "", modtest.WithPoolMaxConns(2))
	id, letter := paperDue(t, h, 1)
	r := printFor(t, h, "2026-09-13", letter)
	raw := holdRow(t, h, `SELECT 1 FROM invoices.invoices WHERE id = $1 FOR UPDATE`, id)
	done := startRequest(payer(t, h), http.MethodPost, batchPath(r.Batch.ID, "reprint"), nil)
	select {
	case res := <-done:
		if res.res.Status != http.StatusOK {
			t.Errorf("the reprint = %d %s, want 200", res.res.Status, res.res.Body)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the reprint waited on the invoice")
	}
	raw.release(t)
	if row := letterOf(t, h, letter); row.Status != "awaiting_print" {
		t.Errorf("the letter = %s, want awaiting print", row.Status)
	}
}

// A batch posted or reprinted while its own creation is still printing
// (D18; Task 13's review, IMPORTANT-1): the request is parked right after it
// locked its first letter, holding the batch FOR SHARE, and the posting or
// the reprint then waits on the batch (pg_blocking_pids). The first letter
// is printed and committed, and the posting sends it — or the reprint
// returns it to awaiting print; the second letter's transaction then finds
// the batch closed and leaves the letter out, print_batch_closed, still
// awaiting print and free for another batch — never printed into a batch
// that is closed.
func TestPrintBatch_PostedWhilePrinting(t *testing.T) {
	for _, tc := range []struct {
		name, op, firstStatus string
		body                  any
		firstInBatch          bool
	}{
		{"posted", "posted", "sent", postedOn("2026-09-12"), true},
		{"reprinted", "reprint", "awaiting_print", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := paperHarness(t, "")
			_, a := paperDue(t, h, 1)
			_, b := paperDue(t, h, 2)
			probeConn := ownConn(t, h)
			before := deadlocks(t, probeConn)
			var once sync.Once
			var done <-chan raceRequest
			waited := make(chan uint32, 1)
			restore := invoices.SetLockTaken(func(_ context.Context, what, key string) {
				if what != "reminder" || key != idKey(a) {
					return
				}
				once.Do(func() {
					id := int64(h.Count(t, `SELECT max(id)::int FROM invoices.reminder_print_batches`))
					done = startRequest(payer(t, h), http.MethodPost, batchPath(id, tc.op), tc.body)
					waited <- newWaiter(t, probeConn)
				})
			})
			r := printFor(t, h, "2026-09-12", a, b)
			restore()
			if waiter := <-waited; len(blockersOf(t, probeConn, waiter)) != 0 {
				t.Errorf("the %s still waits after the batch was made", tc.name)
			}
			finished(t, "the "+tc.name, done, http.StatusOK)
			if len(r.LeftOut) != 1 || r.LeftOut[0].ReminderID != b || r.LeftOut[0].Reason != "print_batch_closed" {
				t.Errorf("left out %+v, want letter %d, print_batch_closed", r.LeftOut, b)
			}
			if got := len(r.Batch.Letters) == 1 && r.Batch.Letters[0].ID == a; got != tc.firstInBatch {
				t.Errorf("the batch's letters = %+v, want the first letter %v", r.Batch.Letters, tc.firstInBatch)
			}
			if row := letterOf(t, h, a); row.Status != tc.firstStatus {
				t.Errorf("the first letter = %s, want %s", row.Status, tc.firstStatus)
			}
			if row := letterOf(t, h, b); row.Status != "awaiting_print" || row.PrintBatchID != nil || !factsCleared(row) {
				t.Errorf("the second letter = %s batch %v cleared %v, want awaiting print, in no batch, without facts",
					row.Status, row.PrintBatchID, factsCleared(row))
			}
			if again := printFor(t, h, "2026-09-12", b); len(again.Batch.Letters) != 1 {
				t.Errorf("a new batch = %+v, want the second letter printed", again)
			}
			if after := deadlocks(t, probeConn); after != before {
				t.Errorf("Postgres broke %d deadlock(s)", after-before)
			}
		})
	}
}

// A letter whose PDF could not be stored when it was printed (Task 13's
// review, IMPORTANT-2): it is printed all the same, its own download a 500
// until its PDF is stored, and the batch's PDF renders it from its row. The
// next posting stores it — the letter sent by then, whose key and hash the
// trigger lets be set once — and so does the next download of the batch's
// PDF; either way its PDF is the one its row renders.
func TestPrintBatch_AStoreFailureHealed(t *testing.T) {
	h := paperHarness(t, "")
	_, posting := paperDue(t, h, 1)
	_, download := paperDue(t, h, 2)
	c := payer(t, h)
	h.objects.failPuts(errors.New("disk full"))
	first := printFor(t, h, "2026-09-12", posting)
	second := printFor(t, h, "2026-09-12", download)
	for _, l := range []int64{posting, download} {
		if row := letterOf(t, h, l); row.Status != "printed" || row.PdfObjectKey != nil {
			t.Fatalf("letter %d = %s key %v, want printed without a PDF", l, row.Status, row.PdfObjectKey)
		}
	}
	if res := reader(t, h).Do(http.MethodGet, letterPath(posting, "pdf"), nil); res.Status != http.StatusInternalServerError {
		t.Errorf("the PDF of a letter whose store failed = %d, want 500", res.Status)
	}
	if res := c.Do(http.MethodGet, first.PdfURL, nil); res.Status != http.StatusOK || letterOf(t, h, posting).PdfObjectKey != nil {
		t.Errorf("the batch's PDF while the store fails = %d, want 200 and still no stored PDF", res.Status)
	}
	h.objects.failPuts(nil)

	posted(t, "the posting", c.Do(http.MethodPost, batchPath(first.Batch.ID, "posted"), postedOn("2026-09-12")))
	if res := c.Do(http.MethodGet, second.PdfURL, nil); res.Status != http.StatusOK {
		t.Fatalf("the second batch's PDF = %d", res.Status)
	}
	for _, tc := range []struct {
		letter int64
		status string
	}{{posting, "sent"}, {download, "printed"}} {
		row := letterOf(t, h, tc.letter)
		want, err := invoices.RenderLetterForTest(context.Background(), h.Deps(), tc.letter)
		if err != nil {
			t.Fatalf("render letter %d: %v", tc.letter, err)
		}
		res := reader(t, h).Do(http.MethodGet, letterPath(tc.letter, "pdf"), nil)
		if row.Status != tc.status || row.PdfObjectKey == nil || res.Status != http.StatusOK || string(res.Body) != string(want) {
			t.Errorf("letter %d = %s key %v, its PDF %d; want %s with its PDF stored and served", tc.letter, row.Status,
				row.PdfObjectKey, res.Status, tc.status)
		}
	}
}

// Every cause the posting's re-judge names (NI4; Task 13's review,
// IMPORTANT-3): four letters printed with the fee 38, and since printing the
// customer of one anonymised, another's policy made none, a third invoice
// held and the hold lifted barring charges, and reminder fees switched off
// for the fourth's customer type, so the engine no longer gives the fee.
// Posted: all four sent, each fee waived claimed_in_error, listed with its
// cause — customer_anonymised, policy_none, charges_barred, action_changed.
func TestPrintBatch_PostedRejudgesEveryCause(t *testing.T) {
	t.Parallel()
	h := paperHarness(t, "")
	erased := deliveredOn(t, h, 1, customerPerson, "2026-08-03")
	erasedLetter := paperLetter(t, h, erased, 1)
	none := deliveredOn(t, h, 2, customerForeign, "2026-08-03")
	noneLetter := paperLetter(t, h, none, 1)
	barred, barredLetter := paperDue(t, h, 3)
	changed, changedLetter := paperDue(t, h, 4)
	r := printFor(t, h, "2026-09-12", erasedLetter, noneLetter, barredLetter, changedLetter)
	for _, l := range r.Batch.Letters {
		if l.Fee == nil || *l.Fee != 38 {
			t.Fatalf("letter %d printed with fee %v, want 38", l.ID, l.Fee)
		}
	}
	if len(r.Batch.Letters) != 4 {
		t.Fatalf("the batch = %+v, want four letters printed", r)
	}
	h.Exec(t, `INSERT INTO invoices.erased_customers (customer_id, erased_at) VALUES ($1, now())`, customerPerson)
	plantPolicy(t, h, customerForeign, "none", "Avtalt med kunden")
	c := payer(t, h)
	answered(t, "the hold", c.Do(http.MethodPost, holdPath(barred), holdNote("Kunden bestrider timene")))
	answered(t, "the barring lift", c.Do(http.MethodPost, liftPath(barred), liftBody(false, "Innsigelsen var begrunnet")))
	h.Exec(t, `UPDATE invoices.reminder_settings SET person_charge = 'none', business_charge = 'none'`)

	p := posted(t, "the posting", c.Do(http.MethodPost, batchPath(r.Batch.ID, "posted"), postedOn("2026-09-12")))
	waived := map[int64]batchWaiverJSON{}
	for _, w := range p.Waived {
		waived[w.ReminderID] = w
	}
	for _, tc := range []struct {
		letter, invoice int64
		reason          string
	}{
		{erasedLetter, erased, "customer_anonymised"}, {noneLetter, none, "policy_none"},
		{barredLetter, barred, "charges_barred"}, {changedLetter, changed, "action_changed"},
	} {
		if w := waived[tc.letter]; w.Reason != tc.reason || w.InvoiceID != tc.invoice || !slices.Equal(w.Kinds, []string{"fee"}) {
			t.Errorf("letter %d's waiver = %+v, want its fee waived, %s", tc.letter, w, tc.reason)
		}
		if got := letterWaivers(t, h, tc.letter); !slices.Equal(got, []string{"fee claimed_in_error"}) {
			t.Errorf("letter %d's waivers = %v, want its fee waived claimed_in_error", tc.letter, got)
		}
		if row := letterOf(t, h, tc.letter); row.Status != "sent" || amountText(row.Fee) != "38.00" {
			t.Errorf("letter %d = %s fee %s, want sent with the fee it printed", tc.letter, row.Status, amountText(row.Fee))
		}
	}
	if len(p.Waived) != 4 {
		t.Errorf("waived %+v, want the four letters", p.Waived)
	}
}

// A letter renders as it went (Task 13's review, IMPORTANT-4): what it
// prints as credited is fixed with its facts. A paper letter printed for
// Tuesday 15 September, and a credit note of 200 issued on Saturday the
// 12th after it was printed: the batch's PDF downloaded again is the same
// bytes, and the letter's own render is its stored PDF. An e-mail letter
// mailed before a credit note renders, after it, as the PDF it was mailed
// with.
func TestPrintBatch_ACreditNoteAfterPrinting(t *testing.T) {
	t.Run("paper", func(t *testing.T) {
		h := paperHarness(t, "")
		id, letter := paperDue(t, h, 1)
		r := printFor(t, h, "2026-09-15", letter)
		c := payer(t, h)
		before := c.Do(http.MethodGet, r.PdfURL, nil)
		plantCreditNote(t, h, id, 200)
		after := c.Do(http.MethodGet, r.PdfURL, nil)
		if before.Status != http.StatusOK || after.Status != http.StatusOK || string(before.Body) != string(after.Body) {
			t.Errorf("the batch's PDF before and after the credit note = %d and %d, differing %v; want the same bytes",
				before.Status, after.Status, string(before.Body) != string(after.Body))
		}
		stored := h.objects.object(deref(letterOf(t, h, letter).PdfObjectKey))
		rendered, err := invoices.RenderLetterForTest(context.Background(), h.Deps(), letter)
		if err != nil || string(rendered) != string(stored) {
			t.Errorf("the letter rendered after the credit note (%v) is not its stored PDF", err)
		}
	})
	t.Run("e-mail", func(t *testing.T) {
		h, mails := workerHarness(t, "")
		id, letter := dueLetter(t, h, 1)
		dispatch(t, invoices.NewReminderWorker(h.Deps()))
		if len(mails.delivered()) != 1 {
			t.Fatal("the letter was not mailed")
		}
		plantCreditNote(t, h, id, 200)
		rendered, err := invoices.RenderLetterForTest(context.Background(), h.Deps(), letter)
		if err != nil || string(rendered) != string(mails.delivered()[0].Attachments[0].Content) {
			t.Errorf("the letter rendered after the credit note (%v) is not the PDF it was mailed with", err)
		}
	})
}

// plantCreditNote plants an issued credit note of gross against invoice
// id, issued on the fixed clock's day.
func plantCreditNote(t *testing.T, h *harness, id int64, gross int) {
	t.Helper()
	h.Exec(t, `
		INSERT INTO invoices.invoices (kind, status, number, customer_id, issue_date, credits_invoice_id, exchange_rate_date,
		    seller_legal_name, buyer_name, gross_total, issued_at, created_by_user_id, created_at, updated_at)
		SELECT 'credit_note', 'issued', 9000 + id, customer_id, DATE '2026-09-12', id, DATE '2026-09-12', 'Selger AS',
		    buyer_name, $2, now(), gen_random_uuid(), now(), now()
		FROM invoices.invoices WHERE id = $1`, id, gross)
}

// The rates are shared before the letter is judged (D6, plan reading 6;
// Task 13's review, MINOR 3): a DELETE of the user's inkassosats of 800 from
// 15 September, in flight on a raw transaction, holds the row FOR UPDATE; a
// batch for the 16th waits on it at the share (pg_blocking_pids), and once
// it commits the batch judges the letter without the row — the fee 38 of
// the release's 750, never 40 from a row deleted under it.
func TestPrintBatch_RatesSharedBeforeTheJudgement(t *testing.T) {
	h := paperHarness(t, "", modtest.WithPoolMaxConns(2))
	_, letter := paperDue(t, h, 1)
	rate := plantID(t, h, `INSERT INTO invoices.collection_rates (kind, valid_from, value, source_ref, created_by_user_id, created_at)
		VALUES ('inkassosats', DATE '2026-09-15', 800, 'FOR-2026-09-01-1', gen_random_uuid(), now()) RETURNING id`)
	probeConn := ownConn(t, h)
	before := deadlocks(t, probeConn)
	seen := &lockSeen{}
	defer invoices.SetLockTaken(seen.note)()
	raw := holdRow(t, h, `DELETE FROM invoices.collection_rates WHERE id = $1`, rate)
	done := startRequest(payer(t, h), http.MethodPost, printBatchesPath, printBody("2026-09-16", letter))
	waiter := newWaiter(t, probeConn, raw.pid)
	if got := blockersOf(t, probeConn, waiter); !slices.Equal(got, []uint32{raw.pid}) {
		t.Errorf("the batch waits on %v, want the DELETE %d", got, raw.pid)
	}
	raw.release(t)
	finished(t, "the batch", done, http.StatusCreated)
	// The share waited on the DELETE and so judged "in force" on a snapshot
	// that still had the user's row: it shared no inkassosats at all. The
	// rows read again after the judgement differ, and the letter's
	// transaction is tried again, sharing the release's row it relies on.
	var shared []string
	for _, s := range seen.take() {
		if strings.HasPrefix(s, "collection_rate ") {
			shared = append(shared, s)
		}
	}
	rates := ratesInForce(t, h, "2026-09-16")
	if len(shared) < len(rates) || !slices.Equal(shared[len(shared)-len(rates):], rates) {
		t.Errorf("the batch shared %v, its last share want %v", shared, rates)
	}
	if row := letterOf(t, h, letter); row.Status != "printed" || amountText(row.Fee) != "38.00" || amountText(row.Inkassosats) != "750.00" {
		t.Errorf("the letter = %s fee %s inkassosats %s, want printed on the release's 750", row.Status, amountText(row.Fee), amountText(row.Inkassosats))
	}
	if after := deadlocks(t, probeConn); after != before {
		t.Errorf("Postgres broke %d deadlock(s)", after-before)
	}
}
