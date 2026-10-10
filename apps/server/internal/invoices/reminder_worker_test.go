package invoices_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
	"github.com/vantigo-io/vantigo/server/internal/mail"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The invoices-reminders worker (invoices payments and reminders design
// D10), driven by ProcessOne on the fixed clock: each claim judges its
// letter again under the invoice's lock and the letter's, writes its facts
// on the day it is mailed, renders and stores the PDF once under its hash,
// and mails it through the SMTP seam (MAIL_DRIVER smtp) with the letter's
// own Message-ID. The clock starts on Saturday 12 September 2026 at noon.

const remindersPath = invoicesPath + "/reminders"

func letterPath(id int64, op string) string { return fmt.Sprintf("%s/%d/%s", remindersPath, id, op) }

// letterMail stands in for the SMTP send: it records every attempt the
// worker makes and every mail that went, fails while err is set, and holds
// each send in hold while it is set.
type letterMail struct {
	mu       sync.Mutex
	attempts []mail.Outbound
	sent     []mail.Outbound
	err      error
	hold     func(ctx context.Context, out mail.Outbound)
}

func (m *letterMail) send(ctx context.Context, _ config.MailConfig, out mail.Outbound) error {
	m.mu.Lock()
	m.attempts = append(m.attempts, out)
	hold, err := m.hold, m.err
	m.mu.Unlock()
	if hold != nil {
		hold(ctx, out)
	}
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, out)
	return nil
}

// failWith makes every send fail with err; nil lets them through again.
func (m *letterMail) failWith(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.err = err
}

// holdWith holds every send in fn; nil lets them through at once.
func (m *letterMail) holdWith(fn func(ctx context.Context, out mail.Outbound)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hold = fn
}

func (m *letterMail) tried() []mail.Outbound {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.attempts)
}

func (m *letterMail) delivered() []mail.Outbound {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.sent)
}

// sellerMailbox is where the seller wants replies: the settings' e-mail.
const sellerMailbox = "faktura@selger.example"

// workerHarness is an installation that sends e-mail through m, reminders on
// with set (see remindersOn), the seller's e-mail sellerMailbox and Acme's
// reminder address purring@acme.example.
func workerHarness(t *testing.T, set string, opts ...modtest.Option) (*harness, *letterMail) {
	t.Helper()
	m := &letterMail{}
	h := newHarness(t, append([]modtest.Option{modtest.WithSMTPSend(m.send), modtest.WithEnv("MAIL_DRIVER", "smtp"),
		modtest.WithEnv("SMTP_HOST", "smtp.example.invalid"), modtest.WithEnv("SMTP_FROM", "faktura@example.invalid")}, opts...)...)
	h.customers.edit(customerAcme, func(p *contracts.CustomerBillingProfile) { p.ReminderEmail = "purring@acme.example" })
	h.Exec(t, `UPDATE invoices.settings SET email = $1`, sellerMailbox)
	remindersOn(t, h, set)
	return h, m
}

// letterInvoice is an issued invoice planted with every snapshot a letter
// prints: the seller Selger AS with its account, the buyer's address, its
// language and type, and a KID when kid is set. It was handed over on its
// issue day, 1 July 2026.
type letterInvoice struct {
	number   int64
	customer int32
	due      string
	gross    string // "1000" when ""
	language string // "" for NULL (Norwegian)
	business bool   // a business with an organisation number
	kid      string // "" for none
}

func plantLetterInvoice(t *testing.T, h *harness, spec letterInvoice) int64 {
	t.Helper()
	gross := spec.gross
	if gross == "" {
		gross = "1000"
	}
	buyerType, orgNo := "person", ""
	if spec.business {
		buyerType, orgNo = "business", "923456783"
	}
	algorithm := ""
	if spec.kid != "" {
		algorithm = "mod10"
	}
	id := plantID(t, h, `
		INSERT INTO invoices.invoices (kind, status, number, customer_id, issue_date, due_date, exchange_rate_date,
		    seller_legal_name, seller_organisation_number, seller_vat_registered, seller_in_foretaksregisteret,
		    seller_address_line1, seller_postal_code, seller_city, seller_country, seller_bank_account, seller_email,
		    buyer_name, buyer_type, buyer_organisation_number, buyer_language, buyer_address_line1, buyer_postal_code,
		    buyer_city, buyer_country, kid, kid_algorithm, gross_total, issued_at, created_by_user_id, created_at, updated_at)
		VALUES ('invoice', 'issued', $1, $2, DATE '2026-07-01', $3::date, DATE '2026-07-01',
		    'Selger AS', '987654321', true, true, 'Storgata 1', '0155', 'Oslo', 'NO', '86011117947', 'post@selger.example',
		    $4, $5, NULLIF($6, ''), NULLIF($7, ''), 'Kundeveien 2', '0150', 'Oslo', 'NO',
		    NULLIF($8, ''), NULLIF($9, ''), $10::numeric, now(), gen_random_uuid(), now(), now())
		RETURNING id`, spec.number, spec.customer, spec.due, fmt.Sprintf("Kunde %d", spec.number), buyerType, orgNo,
		spec.language, spec.kid, algorithm, gross)
	plantManualDelivery(t, h, id, "2026-07-01")
	return id
}

// queueLetter plants letter sequence of invoice id, queued for e-mail to
// Acme's reminder address and due now, at level — announcing the hand-off
// when announces — in language, and answers its id.
func queueLetter(t *testing.T, h *harness, id int64, sequence int, level string, announces bool, language string) int64 {
	t.Helper()
	return plantID(t, h, `
		INSERT INTO invoices.reminders (invoice_id, run_id, sequence, level, announces_collection, channel, recipient,
		    language, created_at, created_by_user_id, status, next_attempt_at)
		VALUES ($1, $2, $3, $4, $5, 'email', 'purring@acme.example', $6, now(), gen_random_uuid(), 'queued', $7)
		RETURNING id`, id, plantRun(t, h), sequence, level, announces, language, h.Now())
}

// dispatch runs one claim of w, failing the test on an error, and answers
// whether a letter was claimed.
func dispatch(t *testing.T, w *invoices.ReminderWorker) bool {
	t.Helper()
	processed, err := w.ProcessOne(context.Background())
	if err != nil {
		t.Fatalf("ProcessOne: %v", err)
	}
	return processed
}

// letterOf reads letter id's row.
func letterOf(t *testing.T, h *harness, id int64) store.InvoicesReminder {
	t.Helper()
	r, err := store.New(h.Pool()).GetReminder(context.Background(), id)
	if err != nil {
		t.Fatalf("read letter %d: %v", id, err)
	}
	return r
}

// dayText is a date column as text, "<nil>" for NULL.
func dayText(d pgtype.Date) string {
	if !d.Valid {
		return "<nil>"
	}
	return d.Time.Format(time.DateOnly)
}

// amountText is a numeric column as text with two decimals, "<nil>" for
// NULL.
func amountText(n pgtype.Numeric) string {
	f, err := n.Float64Value()
	if err != nil || !f.Valid {
		return "<nil>"
	}
	return fmt.Sprintf("%.2f", f.Float64)
}

// factsCleared reports whether a letter carries none of its facts, nor a
// lease.
func factsCleared(r store.InvoicesReminder) bool {
	return !r.SentOn.Valid && !r.Deadline.Valid && r.Regime == nil && !r.PrincipalOpen.Valid && r.FeeKind == nil &&
		!r.Total.Valid && r.InterestSegments == nil && r.ChargeNotes == nil && r.PdfObjectKey == nil &&
		r.LeaseID == nil && r.LeaseUntil == nil
}

// runOne makes a run of invoice id's action as a payer, the stale bank data
// confirmed, and answers its one letter.
func runOne(t *testing.T, h *harness, id int64, action string) reminderJSON {
	t.Helper()
	r := made(t, "the run", run(payer(t, h), runBody(yes, item(id, action))))
	if len(r.Created) != 1 {
		t.Fatalf("the run made %d letters (skipped %+v), want one", len(r.Created), r.Skipped)
	}
	return r.Created[0]
}

// The facts are the sending's, never the run's (amendment 12): a letter
// queued on Monday 14 September and mailed on Thursday 17 September has
// sent_on Thursday, its deadline 14 days from Thursday — 1 October — and its
// fee judged on Thursday. Acme's 1 000 fell due on Wednesday 2 September:
// with the first reminder 10 days on, a letter is due from 12 September, but
// a fee only from 16 September (R7's 14 days), so Monday's run made it
// fee-free and Thursday's sending carries the fee 38 and late interest from
// 3 September — 15 days at 12.25 %, 5.03 — the total 1 043.03.
func TestReminderWorker_FactsWrittenAtSending(t *testing.T) {
	t.Parallel()
	h, mails := workerHarness(t, "late_interest = true, first_reminder_days = 10")
	id := deliveredOn(t, h, 1, customerAcme, "2026-09-02")
	moveClockTo(t, h, time.Date(2026, time.September, 14, 8, 0, 0, 0, time.UTC))
	queued := runOne(t, h, id, "reminder")
	if queued.Status != "queued" || queued.SentOn != nil || queued.Fee != nil {
		t.Fatalf("Monday's letter = %+v, want queued without facts", queued)
	}
	moveClockTo(t, h, time.Date(2026, time.September, 17, 8, 0, 0, 0, time.UTC))
	w := invoices.NewReminderWorker(h.Deps())
	if !dispatch(t, w) {
		t.Fatal("the worker claimed nothing")
	}
	r := letterOf(t, h, queued.ID)
	got := fmt.Sprintf("%s sent_on %s deadline %s fee %s interest %s from %s total %s notes %v",
		r.Status, dayText(r.SentOn), dayText(r.Deadline), amountText(r.Fee), amountText(r.Interest),
		dayText(r.InterestFrom), amountText(r.Total), r.ChargeNotes)
	want := "sent sent_on 2026-09-17 deadline 2026-10-01 fee 38.00 interest 5.03 from 2026-09-03 total 1043.03 notes []"
	if got != want {
		t.Errorf("the letter = %s\nwant          %s", got, want)
	}
	if r.SentAt == nil || !r.SentAt.Equal(h.Now()) || r.LeaseID != nil || r.LeaseUntil != nil {
		t.Errorf("sent_at %v lease %v %v; want the claim's time and no lease", r.SentAt, r.LeaseID, r.LeaseUntil)
	}
	if n := len(mails.delivered()); n != 1 {
		t.Errorf("%d mails went, want 1", n)
	}
	if dispatch(t, w) {
		t.Error("a sent letter was claimed again")
	}
}

// dueLetter is Acme's 1 000 due 3 August, handed over on its issue day,
// with a reminder queued for it and due now: the engine gives a reminder
// with the fee 38 on the fixed clock. It answers the invoice and the letter.
func dueLetter(t *testing.T, h *harness, number int64) (invoiceID, letterID int64) {
	t.Helper()
	invoiceID = deliveredOn(t, h, number, customerAcme, "2026-08-03")
	return invoiceID, queueLetter(t, h, invoiceID, 1, "reminder", false, "nb")
}

// heldSend holds the next send until the test releases it, and answers a
// channel told when a send reached the hold and the release.
func heldSend(m *letterMail) (reached <-chan struct{}, release func()) {
	in, out := make(chan struct{}, 4), make(chan struct{})
	var once sync.Once
	m.holdWith(func(context.Context, mail.Outbound) {
		in <- struct{}{}
		select {
		case <-out:
		case <-time.After(30 * time.Second):
		}
	})
	return in, func() { once.Do(func() { close(out) }) }
}

// waitFor waits for a signal on ch, failing the test after 10 s.
func waitFor(t *testing.T, what string, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(10 * time.Second):
		t.Fatalf("%s never happened", what)
	}
}

// claimed runs w.ProcessOne on its own goroutine and answers its outcome.
func claimed(w *invoices.ReminderWorker) <-chan error {
	done := make(chan error, 1)
	go func() {
		_, err := w.ProcessOne(context.Background())
		done <- err
	}()
	return done
}

// finishedClaim waits for a claim started by claimed.
func finishedClaim(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the claim: %v", err)
		}
	case <-time.After(40 * time.Second):
		t.Fatal("the claim never finished")
	}
}

// One claim per letter, under a lease (D10), on a pool of two: two workers
// never take one letter — the second finds nothing while the first's lease
// runs, even while the first is mailing it; the lease is the claim's time
// plus 60 seconds, free again at exactly its end — the complement of "being
// sent" — and a write of a claim whose lease another claim took does
// nothing.
func TestReminderWorker_ClaimAndLease(t *testing.T) {
	h, mails := workerHarness(t, "", modtest.WithPoolMaxConns(2))
	_, letter := dueLetter(t, h, 1)
	w1, w2 := invoices.NewReminderWorker(h.Deps()), invoices.NewReminderWorker(h.Deps())

	// Two workers at once: exactly one claims.
	got := make(chan bool, 2)
	for _, w := range []*invoices.ReminderWorker{w1, w2} {
		go func() {
			processed, err := w.ProcessOne(context.Background())
			if err != nil {
				t.Errorf("ProcessOne: %v", err)
			}
			got <- processed
		}()
	}
	if a, b := <-got, <-got; a == b {
		t.Fatalf("claimed = %v, %v; want exactly one of the two workers to claim the letter", a, b)
	}
	if r := letterOf(t, h, letter); r.Status != "sent" || len(mails.delivered()) != 1 {
		t.Fatalf("the letter = %s after %d mails, want sent once", r.Status, len(mails.delivered()))
	}

	// The lease while the letter is being mailed.
	_, second := dueLetter(t, h, 2)
	reached, release := heldSend(mails)
	defer release()
	claim := h.Now()
	done := claimed(w1)
	waitFor(t, "the first claim's send", reached)
	r := letterOf(t, h, second)
	if r.LeaseID == nil || r.LeaseUntil == nil || !r.LeaseUntil.Equal(claim.Add(60*time.Second)) || !r.SentOn.Valid {
		t.Fatalf("the letter being sent = lease %v until %v, sent_on %s; want a lease to the claim plus 60 s and its facts",
			r.LeaseID, r.LeaseUntil, dayText(r.SentOn))
	}
	h.Advance(59 * time.Second)
	if dispatch(t, w2) {
		t.Fatal("a second worker claimed a letter whose lease still runs")
	}

	// Another claim took the lease (as it may once the lease ran out): the
	// first claim's completion is a lease-checked write and does nothing.
	h.Exec(t, `UPDATE invoices.reminders SET lease_id = 'another-claim' WHERE id = $1`, second)
	mails.holdWith(nil)
	release()
	finishedClaim(t, done)
	r = letterOf(t, h, second)
	if r.Status != "queued" || r.SentAt != nil || deref(r.LeaseID) != "another-claim" {
		t.Errorf("after a lost lease the letter = %s sent_at %v lease %s; want it queued, unsent, the other claim's",
			r.Status, r.SentAt, deref(r.LeaseID))
	}

	// Free at exactly the lease's end: the next claim takes it, judges it
	// again and sends it.
	h.Advance(time.Second)
	if !dispatch(t, w2) {
		t.Fatal("the letter was not claimable at exactly its lease's end")
	}
	if r := letterOf(t, h, second); r.Status != "sent" || r.LeaseID != nil {
		t.Errorf("the second claim left the letter %s lease %v, want sent", r.Status, r.LeaseID)
	}
}

// The re-judge at sending withdraws a letter that no longer goes (D10),
// each with its reason and no user, its facts and lease cleared and nothing
// mailed: the invoice paid, put on hold, handed off, its customer's policy
// none, the customer anonymised, the engine now giving another level, or
// another letter now on its way.
func TestReminderWorker_RejudgeWithdraws(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, reason string
		meanwhile    func(t *testing.T, h *harness, id int64)
		level        string
	}{
		{"settled", "settled", func(t *testing.T, h *harness, id int64) { plantPayment(t, h, id, "1000", "2026-09-10") }, "reminder"},
		{"held", "on_hold", func(t *testing.T, h *harness, id int64) {
			h.Exec(t, `INSERT INTO invoices.invoice_holds (invoice_id, kind, note, placed_at, placed_by_user_id)
				VALUES ($1, 'disputed', 'Bestridt', now(), gen_random_uuid())`, id)
		}, "reminder"},
		{"handed off", "handed_off", func(t *testing.T, h *harness, id int64) {
			h.Exec(t, `INSERT INTO invoices.collection_handoffs (invoice_id, handed_on, agency, created_at, created_by_user_id)
				VALUES ($1, DATE '2026-09-11', 'Inkasso AS', now(), gen_random_uuid())`, id)
		}, "reminder"},
		{"policy none", "policy_none", func(t *testing.T, h *harness, _ int64) {
			h.Exec(t, `INSERT INTO invoices.customer_reminder_policies (customer_id, mode, updated_by_user_id, updated_at)
				VALUES ($1, 'none', gen_random_uuid(), now())`, customerAcme)
		}, "reminder"},
		{"anonymised", "customer_anonymised", func(t *testing.T, h *harness, _ int64) {
			h.Exec(t, `INSERT INTO invoices.erased_customers (customer_id, erased_at) VALUES ($1, now())`, customerAcme)
		}, "reminder"},
		{"another level", "action_changed", func(*testing.T, *harness, int64) {}, "collection_notice"},
		{"another letter on its way", "action_changed", func(t *testing.T, h *harness, id int64) {
			plantLetterIn(t, h, id, 2, "awaiting_print")
		}, "reminder"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			h, mails := workerHarness(t, "")
			id := deliveredOn(t, h, 1, customerAcme, "2026-08-03")
			letter := queueLetter(t, h, id, 1, c.level, false, "nb")
			c.meanwhile(t, h, id)
			if !dispatch(t, invoices.NewReminderWorker(h.Deps())) {
				t.Fatal("the worker claimed nothing")
			}
			r := letterOf(t, h, letter)
			if r.Status != "withdrawn" || deref(r.WithdrawalReason) != c.reason || r.WithdrawnByUserID != nil ||
				r.WithdrawnAt == nil || !r.WithdrawnAt.Equal(h.Now()) {
				t.Errorf("the letter = %s %s by %v at %v; want withdrawn %s by no one, now",
					r.Status, deref(r.WithdrawalReason), r.WithdrawnByUserID, r.WithdrawnAt, c.reason)
			}
			if !factsCleared(r) || len(mails.tried()) != 0 {
				t.Errorf("facts cleared %v, %d sends; want no facts, no lease and nothing mailed", factsCleared(r), len(mails.tried()))
			}
		})
	}
}

// A letter whose rates or regime review are missing waits an hour at a
// time without its attempt counted (plan readings 17, 46): queued, its
// held_reason the cause — collection_regime_unreviewed for a fee past the
// review, collection_rates_outdated for interest over a half-year with no
// rate — attempts and first_attempt_at untouched, nothing mailed, so 48
// hours of it never fail it; the next success clears held_reason.
func TestReminderWorker_OutdatedOrUnreviewedReschedulesUncounted(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, set, issue, due, cause, fix string
	}{
		{"unreviewed", "regime_reviewed_through = DATE '2026-09-01'", "2026-07-01", "2026-08-03", "collection_regime_unreviewed",
			"regime_reviewed_through = DATE '2026-12-31'"},
		// Interest from 2 November 2023: the second half of 2023 has no rate.
		{"outdated", "late_interest = true", "2023-10-02", "2023-11-01", "collection_rates_outdated", "late_interest = false"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			h, mails := workerHarness(t, c.set)
			id := plantOverdue(t, h, overdueSpec{number: 1, customer: customerAcme, issue: c.issue, due: c.due, deliveredOnIssueOn: true})
			letter := queueLetter(t, h, id, 1, "reminder", false, "nb")
			w := invoices.NewReminderWorker(h.Deps())
			for i := range 50 {
				if !dispatch(t, w) {
					t.Fatalf("hour %d: the worker claimed nothing", i)
				}
				r := letterOf(t, h, letter)
				if r.Status != "queued" || deref(r.HeldReason) != c.cause || r.Attempts != 0 || r.FirstAttemptAt != nil ||
					r.NextAttemptAt == nil || !r.NextAttemptAt.Equal(h.Now().Add(time.Hour)) || !factsCleared(r) {
					t.Fatalf("hour %d: the letter = %s held %s attempts %d first %v next %v (cleared %v); want queued, held %s, "+
						"nothing counted, an hour out, no facts", i, r.Status, deref(r.HeldReason), r.Attempts, r.FirstAttemptAt,
						r.NextAttemptAt, factsCleared(r), c.cause)
				}
				h.Advance(time.Hour)
			}
			if n := len(mails.tried()); n != 0 {
				t.Fatalf("%d sends while held, want none", n)
			}
			h.Exec(t, `UPDATE invoices.reminder_settings SET `+c.fix)
			if !dispatch(t, w) {
				t.Fatal("the worker claimed nothing once the cause was mended")
			}
			if r := letterOf(t, h, letter); r.Status != "sent" || r.HeldReason != nil {
				t.Errorf("the letter = %s held %s, want sent with held_reason cleared", r.Status, deref(r.HeldReason))
			}
		})
	}
}

// The PDF is stored once under a key carrying its hash (plan reading 35):
// a failed attempt and a same-day retry with nothing changed render the
// same bytes — the object is there, so it is not put again — and the row
// renders those bytes every time; a same-day retry after a partial payment
// renders other bytes, stored under a second key, and the download serves
// the one mailed; a store that fails is a failed attempt.
func TestReminderWorker_PDFStoredOnce(t *testing.T) {
	t.Parallel()
	h, mails := workerHarness(t, "")
	w := invoices.NewReminderWorker(h.Deps())
	keyOf := regexp.MustCompile(`^reminders/(\d+)/(\d+)-2026-09-12-([0-9a-f]{64})\.pdf$`)

	// Unchanged: one object.
	id, letter := dueLetter(t, h, 1)
	mails.failWith(errors.New("421 try again later"))
	dispatch(t, w)
	keys, puts, _ := h.objects.stored()
	if len(keys) != 1 || puts != 1 {
		t.Fatalf("after the failed attempt the store holds %v (%d puts), want one object", keys, puts)
	}
	m := keyOf.FindStringSubmatch(keys[0])
	if m == nil || m[1] != itoa(id) || m[2] != itoa(letter) || m[3] != sha(h.objects.object(keys[0])) {
		t.Fatalf("the key %s, want reminders/%d/%d-2026-09-12-<the bytes' hash>.pdf", keys[0], id, letter)
	}
	mails.failWith(nil)
	h.Advance(2 * time.Second)
	dispatch(t, w)
	r := letterOf(t, h, letter)
	if keys2, puts2, _ := h.objects.stored(); len(keys2) != 1 || puts2 != 1 {
		t.Errorf("after the retry the store holds %v (%d puts), want the one object, not put again", keys2, puts2)
	}
	if r.Status != "sent" || deref(r.PdfObjectKey) != keys[0] || deref(r.PdfSha256) != m[3] {
		t.Errorf("the letter = %s key %s sha %s, want sent with the stored object", r.Status, deref(r.PdfObjectKey), deref(r.PdfSha256))
	}
	again, err := invoices.RenderLetterForTest(context.Background(), h.Deps(), letter)
	if err != nil || string(again) != string(h.objects.object(keys[0])) {
		t.Errorf("rendering the row again (%v) gave other bytes than the stored ones", err)
	}

	// A partial payment between two attempts of one day: a second object.
	id2, letter2 := dueLetter(t, h, 2)
	mails.failWith(errors.New("421 try again later"))
	dispatch(t, w)
	plantPayment(t, h, id2, "400", "2026-09-11")
	mails.failWith(nil)
	h.Advance(2 * time.Second)
	dispatch(t, w)
	r2 := letterOf(t, h, letter2)
	var mine []string
	all, _, _ := h.objects.stored()
	for _, k := range all {
		if strings.HasPrefix(k, fmt.Sprintf("reminders/%d/", id2)) {
			mine = append(mine, k)
		}
	}
	if len(mine) != 2 || r2.Status != "sent" || amountText(r2.PrincipalOpen) != "600.00" || !slices.Contains(mine, deref(r2.PdfObjectKey)) {
		t.Fatalf("after the payment the letter's objects = %v, the letter %s principal %s key %s; want two objects, "+
			"sent with 600 and the second key", mine, r2.Status, amountText(r2.PrincipalOpen), deref(r2.PdfObjectKey))
	}
	res := reader(t, h).Do(http.MethodGet, letterPath(letter2, "pdf"), nil)
	mailed := mails.delivered()[len(mails.delivered())-1].Attachments[0].Content
	if res.Status != http.StatusOK || string(res.Body) != string(mailed) || sha(res.Body) != deref(r2.PdfSha256) {
		t.Errorf("the download = %d (%d bytes), want the mailed PDF", res.Status, len(res.Body))
	}

	// A store that fails: a failed attempt, nothing mailed.
	_, letter3 := dueLetter(t, h, 3)
	before := len(mails.tried())
	h.objects.failPuts(errors.New("the bucket is gone"))
	dispatch(t, w)
	r3 := letterOf(t, h, letter3)
	if r3.Status != "queued" || r3.Attempts != 1 || deref(r3.LastError) != "The document store could not store the letter." ||
		!factsCleared(r3) || len(mails.tried()) != before {
		t.Errorf("after a store failure the letter = %s attempts %d error %s cleared %v; want a failed attempt, nothing mailed",
			r3.Status, r3.Attempts, deref(r3.LastError), factsCleared(r3))
	}
}

// The mail (D10): to the letter's recipient, Reply-To the seller's mailbox,
// from the seller's name, the subject by level and language, a short cover,
// the PDF attached under its name — the bytes stored — and the letter's own
// Message-ID: a UUID made at its first claim, kept on the retry, and never
// another letter's.
func TestReminderWorker_TheMail(t *testing.T) {
	t.Parallel()
	h, mails := workerHarness(t, "")
	w := invoices.NewReminderWorker(h.Deps())
	nb := plantLetterInvoice(t, h, letterInvoice{number: 7, customer: customerAcme, due: "2026-08-03"})
	nbLetter := queueLetter(t, h, nb, 1, "reminder", false, "nb")
	mails.failWith(errors.New("421 try again later"))
	dispatch(t, w)
	stored := letterOf(t, h, nbLetter).MessageID
	mails.failWith(nil)
	h.Advance(2 * time.Second)
	dispatch(t, w)
	en := plantLetterInvoice(t, h, letterInvoice{number: 8, customer: customerAcme, due: "2026-08-03", language: "en"})
	enLetter := queueLetter(t, h, en, 1, "reminder", false, "en")
	dispatch(t, w)

	tried := mails.tried()
	if len(tried) != 3 {
		t.Fatalf("%d sends, want the failed one, its retry and the English letter", len(tried))
	}
	uuidID := regexp.MustCompile(`^reminder-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}@vantigo\.invalid$`)
	if stored == nil || !uuidID.MatchString(*stored) || tried[0].MessageID != *stored || tried[1].MessageID != *stored {
		t.Errorf("Message-IDs = %q, %q, stored %s; want one UUID id, made at the first claim and kept on the retry",
			tried[0].MessageID, tried[1].MessageID, deref(stored))
	}
	if tried[2].MessageID == *stored || !uuidID.MatchString(tried[2].MessageID) || deref(letterOf(t, h, enLetter).MessageID) != tried[2].MessageID {
		t.Errorf("the English letter's Message-ID %q, want one of its own", tried[2].MessageID)
	}
	for i, c := range []struct {
		out              mail.Outbound
		subject, file    string
		letter           int64
		coverHas, coverL string
	}{
		{tried[1], "Purring: faktura 7", "purring-7-1.pdf", nbLetter, "Vedlagt følger en purring på faktura 7 fra Selger AS.", "Med vennlig hilsen"},
		{tried[2], "Reminder: invoice 8", "reminder-8-1.pdf", enLetter, "Please find attached a payment reminder for invoice 8 from Selger AS.", "Kind regards"},
	} {
		r := letterOf(t, h, c.letter)
		a := c.out.Attachments
		if !slices.Equal(c.out.To, []string{"purring@acme.example"}) || c.out.ReplyTo != sellerMailbox || c.out.DisplayName != "Selger AS" ||
			c.out.Subject != c.subject || len(a) != 1 || a[0].FileName != c.file || a[0].ContentType != "application/pdf" ||
			string(a[0].Content) != string(h.objects.object(deref(r.PdfObjectKey))) {
			t.Errorf("mail %d = to %v reply-to %q from %q subject %q attachments %d; want to Acme, reply-to %s, from Selger AS, %q, %s",
				i, c.out.To, c.out.ReplyTo, c.out.DisplayName, c.out.Subject, len(a), sellerMailbox, c.subject, c.file)
		}
		if !strings.Contains(c.out.TextBody, c.coverHas) || !strings.Contains(c.out.TextBody, c.coverL) ||
			!strings.Contains(c.out.TextBody, "1\u00a0038,00") && !strings.Contains(c.out.TextBody, "1,038.00") {
			t.Errorf("mail %d's cover = %q, want the letter, its amount and a closing", i, c.out.TextBody)
		}
	}
}

// Before the send the claim judges the lease it has left (plan reading 45):
// with less than the send's timeout and a margin — 30 seconds — it does not
// send; the attempt fails and clears the lease and the facts, and the letter
// may be withdrawn again. With exactly 30 seconds left it sends.
func TestReminderWorker_LeaseLeftBeforeSend(t *testing.T) {
	h, mails := workerHarness(t, "")
	w := invoices.NewReminderWorker(h.Deps())
	_, letter := dueLetter(t, h, 1)
	restore := invoices.SetReminderLease(29 * time.Second)
	dispatch(t, w)
	restore()
	r := letterOf(t, h, letter)
	if len(mails.tried()) != 0 || r.Status != "queued" || r.Attempts != 1 || !factsCleared(r) ||
		deref(r.LastError) != "The claim ran out of time before the send; it is retried." {
		t.Fatalf("a claim with 29 s left: %d sends, the letter %s attempts %d error %s cleared %v; "+
			"want no send, a failed attempt, no lease, no facts", len(mails.tried()), r.Status, r.Attempts, deref(r.LastError), factsCleared(r))
	}
	res := payer(t, h).Do(http.MethodPost, letterPath(letter, "withdraw"), map[string]any{"reason": "Kunden betalte kontant"})
	if res.Status != http.StatusOK {
		t.Errorf("the withdrawal after the failed attempt = %d %s, want 200", res.Status, res.Body)
	}

	_, other := dueLetter(t, h, 2)
	defer invoices.SetReminderLease(30 * time.Second)()
	dispatch(t, w)
	if r := letterOf(t, h, other); r.Status != "sent" || len(mails.delivered()) != 1 {
		t.Errorf("a claim with 30 s left: the letter %s after %d mails, want it sent", r.Status, len(mails.delivered()))
	}
}

// A failed attempt backs off 2^n seconds (D10) — 2, 4, 8 — with its facts and
// lease cleared; a letter still unsent 48 hours after its first attempt is
// failed, and is claimed no more.
func TestReminderWorker_BackoffAndFailedAfter48Hours(t *testing.T) {
	t.Parallel()
	h, mails := workerHarness(t, "")
	w := invoices.NewReminderWorker(h.Deps())
	_, letter := dueLetter(t, h, 1)
	mails.failWith(errors.New("554 rejected"))
	first := h.Now()
	for n, wait := range []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second} {
		dispatch(t, w)
		r := letterOf(t, h, letter)
		if r.Status != "queued" || r.Attempts != int32(n+1) || r.NextAttemptAt == nil || !r.NextAttemptAt.Equal(h.Now().Add(wait)) ||
			r.FirstAttemptAt == nil || !r.FirstAttemptAt.Equal(first) || !factsCleared(r) ||
			deref(r.LastError) != "The mail server did not accept the letter." {
			t.Fatalf("attempt %d: %s attempts %d next %v first %v cleared %v error %s; want queued, %v on, first %v",
				n+1, r.Status, r.Attempts, r.NextAttemptAt, r.FirstAttemptAt, factsCleared(r), deref(r.LastError), wait, first)
		}
		h.Advance(wait)
	}
	// Short of the 48 hours: still queued.
	moveClockTo(t, h, first.Add(48*time.Hour-time.Second))
	dispatch(t, w)
	r := letterOf(t, h, letter)
	if r.Status != "queued" || r.Attempts != 4 {
		t.Fatalf("a second short of 48 hours: %s attempts %d, want queued, 4", r.Status, r.Attempts)
	}
	moveClockTo(t, h, *r.NextAttemptAt)
	dispatch(t, w)
	r = letterOf(t, h, letter)
	if r.Status != "failed" || r.FailedAt == nil || !r.FailedAt.Equal(h.Now()) || r.Attempts != 5 || !factsCleared(r) {
		t.Fatalf("48 hours on: %s failed_at %v attempts %d cleared %v; want failed now, 5 attempts, no facts",
			r.Status, r.FailedAt, r.Attempts, factsCleared(r))
	}
	h.Advance(24 * time.Hour)
	if dispatch(t, w) {
		t.Error("a failed letter was claimed")
	}
}

// Both workers per switch (D1): INVOICES_EHF_ENABLED off, only the
// reminder worker; on, the two EHF workers and it.
func TestWorkers_PerSwitch(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	for _, c := range []struct {
		ehf  bool
		want []string
	}{
		{false, []string{"invoices-reminders"}},
		{true, []string{"invoices-ehf", "invoices-ehf-events", "invoices-reminders"}},
	} {
		d := h.Deps()
		cfg := *d.Config
		cfg.InvoicesEhfEnabled = c.ehf
		d.Config = &cfg
		var names []string
		for _, w := range invoices.Module().Workers(d) {
			names = append(names, w.Name())
		}
		if !slices.Equal(names, c.want) {
			t.Errorf("INVOICES_EHF_ENABLED %v: workers %v, want %v", c.ehf, names, c.want)
		}
	}
}

// No call leaves the module from inside one of its transactions (MB rule
// 10): the store's two calls and the send are made between the dispatch's
// two transactions, and the harness's recorder holds none made inside one.
func TestReminderWorker_NoCallUnderALock(t *testing.T) {
	h, mails := workerHarness(t, "")
	_, letter := dueLetter(t, h, 1)
	before := lockedContractCalls.count()
	all := len(contractCalls.byMethod("SMTPSend", "ObjectStore.Exists", "ObjectStore.Put"))
	dispatch(t, invoices.NewReminderWorker(h.Deps()))
	if r := letterOf(t, h, letter); r.Status != "sent" || len(mails.delivered()) != 1 {
		t.Fatalf("the letter = %s, want sent", r.Status)
	}
	if calls := lockedContractCalls.since(before); len(calls) > 0 {
		t.Errorf("calls made inside a transaction:\n%s", strings.Join(calls, "\n"))
	}
	made := contractCalls.byMethod("SMTPSend", "ObjectStore.Exists", "ObjectStore.Put")[all:]
	var methods []string
	for _, c := range made {
		methods = append(methods, c.method)
		if c.locked {
			t.Errorf("%s was made under a lock", c.method)
		}
	}
	if !slices.Equal(methods, []string{"ObjectStore.Exists", "ObjectStore.Put", "SMTPSend"}) {
		t.Errorf("the dispatch's calls = %v, want the store's two, then the send", methods)
	}
}

// The lock order (D18): each of the dispatch's two transactions takes the
// invoice, then the letter; a withdrawal and a retry take the letter alone.
func TestReminderWorker_LockOrder(t *testing.T) {
	h, mails := workerHarness(t, "")
	id, letter := dueLetter(t, h, 1)
	seen := &lockSeen{}
	defer invoices.SetLockTaken(seen.note)()
	dispatch(t, invoices.NewReminderWorker(h.Deps()))
	inv, rem := "invoice "+idKey(id), "reminder "+idKey(letter)
	if got, want := seen.take(), []string{inv, rem, inv, rem}; !slices.Equal(got, want) {
		t.Errorf("the dispatch locked %v, want %v", got, want)
	}
	if len(mails.delivered()) != 1 {
		t.Fatal("the letter was not sent")
	}
	_, queued := dueLetter(t, h, 2)
	payer(t, h).Do(http.MethodPost, letterPath(queued, "withdraw"), map[string]any{"reason": "Avtalt betalingsplan"})
	if got, want := seen.take(), []string{"reminder " + idKey(queued)}; !slices.Equal(got, want) {
		t.Errorf("the withdrawal locked %v, want %v", got, want)
	}
	failed := plantFailed(t, h, deliveredOn(t, h, 3, customerAcme, "2026-08-03"))
	payer(t, h).Do(http.MethodPost, letterPath(failed, "retry"), nil)
	if got, want := seen.take(), []string{"reminder " + idKey(failed)}; !slices.Equal(got, want) {
		t.Errorf("the retry locked %v, want %v", got, want)
	}
}

// plantFailed plants a failed e-mail letter of invoice id and answers it.
func plantFailed(t *testing.T, h *harness, id int64) int64 {
	t.Helper()
	return plantID(t, h, `
		INSERT INTO invoices.reminders (invoice_id, run_id, sequence, level, channel, recipient, language, created_at,
		    created_by_user_id, status, attempts, next_attempt_at, first_attempt_at, failed_at, last_error, message_id)
		VALUES ($1, $2, 1, 'reminder', 'email', 'purring@acme.example', 'nb', now(), gen_random_uuid(), 'failed', 31,
		    $3::timestamptz, $3::timestamptz - interval '48 hours', $3::timestamptz, '554 rejected',
		    'reminder-00000000-0000-4000-8000-000000000001@vantigo.invalid')
		RETURNING id`, id, plantRun(t, h), h.Now())
}

// byMethod is every call out of the module of one of methods, in order.
func (a *allCalls) byMethod(methods ...string) []contractCall {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []contractCall
	for _, c := range a.calls {
		if slices.Contains(methods, c.method) {
			out = append(out, c)
		}
	}
	return out
}
