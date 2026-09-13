package communications_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/communications"
	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/mail"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// This file is task 11: the outbox delivery worker (SV/OutboxJobProcessor.cs,
// communications inventory §13, design doc §4). Every test here drives the
// real worker over a real job queued by the real POST /conversations handler —
// nothing seeds outbox_jobs by hand except the two crash-recovery fixtures,
// which force a state (an expired lease) no API call can produce.
//
// The .NET tests these port are TS/Integration/OutboxWorkerTests.cs
// (crash recovery with the possible-duplicate counter, `:20-69`; terminality
// at Attempts = MaxAttempts, `:71-81`).

// ---- fakes and fixtures ----

// fakeSMTP is Deps.SMTPSend for these tests: it records every envelope and
// the per-channel configuration it was resolved from, can fail on demand, and
// can run a hook *inside* the send — the only place a test can observe the
// database as the worker sees it mid-flight, which is what the
// delivery_attempted_at ordering and the two stolen-lease tests need.
type fakeSMTP struct {
	mu     sync.Mutex
	sent   []mail.Outbound
	cfgs   []config.MailConfig
	err    error
	during func()
}

func (f *fakeSMTP) send(_ context.Context, cfg config.MailConfig, _ bool, msg mail.Outbound) error {
	f.mu.Lock()
	during, err := f.during, f.err
	f.mu.Unlock()
	if during != nil {
		during()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, msg)
	f.cfgs = append(f.cfgs, cfg)
	return err
}

func (f *fakeSMTP) sends() []mail.Outbound {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]mail.Outbound, len(f.sent))
	copy(out, f.sent)
	return out
}

func (f *fakeSMTP) configs() []config.MailConfig {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]config.MailConfig, len(f.cfgs))
	copy(out, f.cfgs)
	return out
}

func (f *fakeSMTP) failWith(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

func (f *fakeSMTP) onSend(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.during = fn
}

// outboxFixture is one queued outbound message: a channel, a conversation and
// the message whose outbox job the worker will claim.
type outboxFixture struct {
	channelID      uuid.UUID
	channelAddress string
	conversationID uuid.UUID
	messageID      uuid.UUID
	recipients     []string
}

// newOutboxHarness builds a harness whose SMTP sending is f, so no test in
// this file ever reaches a network.
func newOutboxHarness(t *testing.T, f *fakeSMTP) *modtest.Harness {
	t.Helper()
	return newHarness(t, modtest.WithSMTPSend(f.send))
}

// seedOutboxJob creates a channel and a conversation through the real API, so
// the outbox job, its deliveries and its queued events are written by the
// production enqueue path (conversations_create.go) rather than by a fixture.
// to/cc are the recipient addresses; cc may be empty.
func seedOutboxJob(t *testing.T, h *modtest.Harness, cc ...string) outboxFixture {
	t.Helper()
	admin := h.SignIn(t, "communications:channels-manage")
	address := channelAddress(t)
	ch := createChannel(t, admin, newChannelBody(address))

	to := fmt.Sprintf("recipient-%s@example.test", uuid.NewString())
	body := newConversationBody(to)
	recipients := []string{to}
	if len(cc) > 0 {
		ccEntries := make([]map[string]any, 0, len(cc))
		for _, a := range cc {
			ccEntries = append(ccEntries, map[string]any{"email": a})
			recipients = append(recipients, a)
		}
		body["cc"] = ccEntries
	}

	c := h.SignIn(t, "communications:conversations-reply")
	created := createConversation(t, c, body)
	if created.MessageId == nil {
		t.Fatal("create conversation returned no messageId")
	}
	return outboxFixture{
		channelID:      uuid.MustParse(ch.Id),
		channelAddress: address,
		conversationID: uuid.MustParse(created.ConversationId),
		messageID:      uuid.MustParse(*created.MessageId),
		recipients:     recipients,
	}
}

type outboxJobRow struct {
	id                  uuid.UUID
	status              string
	attempts            int32
	nextAttemptAt       time.Time
	leaseID             *string
	leaseUntil          *time.Time
	completedAt         *time.Time
	lastError           *string
	deliveryAttemptedAt *time.Time
}

func readOutboxJob(t *testing.T, h *modtest.Harness, messageID uuid.UUID) outboxJobRow {
	t.Helper()
	var j outboxJobRow
	err := h.Pool().QueryRow(context.Background(),
		`SELECT id, status, attempts, next_attempt_at, lease_id, lease_until, completed_at, last_error, delivery_attempted_at
		 FROM communications.outbox_jobs WHERE message_id = $1`, messageID).
		Scan(&j.id, &j.status, &j.attempts, &j.nextAttemptAt, &j.leaseID, &j.leaseUntil,
			&j.completedAt, &j.lastError, &j.deliveryAttemptedAt)
	if err != nil {
		t.Fatalf("read outbox job: %v", err)
	}
	return j
}

type deliveryRow struct {
	id         uuid.UUID
	address    string
	kind       string
	status     string
	attempts   int32
	lastError  *string
	acceptedAt *time.Time
}

func readDeliveries(t *testing.T, h *modtest.Harness, messageID uuid.UUID) []deliveryRow {
	t.Helper()
	rows, err := h.Pool().Query(context.Background(),
		`SELECT id, recipient_address, recipient_type, status, attempts, last_error, accepted_at
		 FROM communications.message_deliveries WHERE message_id = $1 ORDER BY recipient_type, recipient_address`, messageID)
	if err != nil {
		t.Fatalf("read deliveries: %v", err)
	}
	defer rows.Close()
	var out []deliveryRow
	for rows.Next() {
		var d deliveryRow
		if err := rows.Scan(&d.id, &d.address, &d.kind, &d.status, &d.attempts, &d.lastError, &d.acceptedAt); err != nil {
			t.Fatalf("scan delivery: %v", err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read deliveries: %v", err)
	}
	return out
}

// countEvents returns how many message_events rows of eventType exist for the
// message, and the data_json of the first one.
func countEvents(t *testing.T, h *modtest.Harness, messageID uuid.UUID, eventType string) (int, *string) {
	t.Helper()
	n := h.Count(t, `SELECT count(*) FROM communications.message_events WHERE message_id = $1 AND event_type = $2`,
		messageID, eventType)
	var data *string
	if n > 0 {
		if err := h.Pool().QueryRow(context.Background(),
			`SELECT data_json FROM communications.message_events WHERE message_id = $1 AND event_type = $2 LIMIT 1`,
			messageID, eventType).Scan(&data); err != nil {
			t.Fatalf("read event data: %v", err)
		}
	}
	return n, data
}

// expectedMessageID is EmailMessageId.For(message.Id) in its bare form —
// mail.Outbound.MessageID carries no angle brackets, go-mail adds them
// (inventory §15.4: 32 lowercase hex, no dashes, the hardcoded literal
// vantigo.invalid).
func expectedMessageID(messageID uuid.UUID) string {
	return strings.ReplaceAll(messageID.String(), "-", "") + "@vantigo.invalid"
}

// ---- the happy path ----

// TestOutboxWorker_SendsAndCompletesTheJob is §13.3's success ordering end to
// end: the claim increments attempts and moves every sendable delivery to
// "sending", the send goes out on the channel's own SMTP settings, and steps
// 11-13 mark the job completed and every delivery relay_accepted with a
// relay_accepted event.
func TestOutboxWorker_SendsAndCompletesTheJob(t *testing.T) {
	t.Parallel()
	f := &fakeSMTP{}
	h := newOutboxHarness(t, f)
	fx := seedOutboxJob(t, h)
	w := communications.NewOutboxWorker(h.Deps())

	processed, err := w.ProcessOne(context.Background())
	if err != nil {
		t.Fatalf("ProcessOne: %v", err)
	}
	if !processed {
		t.Fatal("ProcessOne = false, want true: a pending job was queued")
	}

	job := readOutboxJob(t, h, fx.messageID)
	if job.status != "completed" {
		t.Errorf("job status = %q, want completed", job.status)
	}
	if job.completedAt == nil {
		t.Error("completed_at is null, want the completion stamp")
	}
	if job.leaseID != nil || job.leaseUntil != nil {
		t.Errorf("lease = (%v, %v), want both cleared on completion", job.leaseID, job.leaseUntil)
	}
	// attempts is incremented AT CLAIM (inventory §13.2), which is the whole
	// reason terminality is `attempts >= max_attempts` post-increment and the
	// first failure's backoff is 2 s rather than 1 s.
	if job.attempts != 1 {
		t.Errorf("attempts = %d, want 1 (incremented at claim, not at failure)", job.attempts)
	}
	// Never cleared on completion — only the next claim clears it (D5,
	// inventory `:1320`). A completed job keeps the stamp.
	if job.deliveryAttemptedAt == nil {
		t.Error("delivery_attempted_at is null on the completed job, want the stamp retained (inventory §13.3)")
	}

	deliveries := readDeliveries(t, h, fx.messageID)
	if len(deliveries) != 1 {
		t.Fatalf("deliveries = %d, want 1", len(deliveries))
	}
	d := deliveries[0]
	if d.status != "relay_accepted" {
		t.Errorf("delivery status = %q, want relay_accepted", d.status)
	}
	if d.acceptedAt == nil {
		t.Error("delivery accepted_at is null, want the acceptance stamp")
	}
	if d.lastError != nil {
		t.Errorf("delivery last_error = %q, want null", *d.lastError)
	}
	if d.attempts != 1 {
		t.Errorf("delivery attempts = %d, want 1 (incremented by the claim)", d.attempts)
	}
	if n, _ := countEvents(t, h, fx.messageID, "relay_accepted"); n != 1 {
		t.Errorf("relay_accepted events = %d, want 1", n)
	}

	sent := f.sends()
	if len(sent) != 1 {
		t.Fatalf("sends = %d, want exactly 1", len(sent))
	}
	if got, want := sent[0].MessageID, expectedMessageID(fx.messageID); got != want {
		t.Errorf("Message-Id = %q, want %q (deterministic from the message id, inventory §15.4)", got, want)
	}
	if len(sent[0].To) != 1 || sent[0].To[0] != fx.recipients[0] {
		t.Errorf("To = %v, want [%s]", sent[0].To, fx.recipients[0])
	}
	if sent[0].TextBody != "Body text" {
		t.Errorf("TextBody = %q, want the message body", sent[0].TextBody)
	}
	cfgs := f.configs()
	if len(cfgs) != 1 {
		t.Fatalf("configs = %d, want 1", len(cfgs))
	}
	// The send resolves the *channel's* stored credentials, not the process's
	// own mail configuration (inventory §15.2 step 1).
	if cfgs[0].Host != "smtp.example.test" || cfgs[0].Port != 587 || cfgs[0].TLS != "starttls" {
		t.Errorf("smtp config = %+v, want the channel's own host/port and STARTTLS on 587", cfgs[0])
	}
	if cfgs[0].From != fx.channelAddress {
		t.Errorf("From = %q, want the channel address %q", cfgs[0].From, fx.channelAddress)
	}

	// The queue is drained: a second call finds no claimable job.
	again, err := w.ProcessOne(context.Background())
	if err != nil {
		t.Fatalf("second ProcessOne: %v", err)
	}
	if again {
		t.Error("second ProcessOne = true, want false on an empty queue")
	}
}

// TestOutboxWorker_StampsDeliveryAttemptedAtBeforeTheSend proves step 7's
// ordering (inventory §13.3, D5): the marker is written by a bare
// auto-committing UPDATE *outside* any transaction, so it is already visible
// to another connection at the moment the external send runs. That visibility
// is the whole point of the marker — it is what makes a crash between the send
// and the completion commit detectable on the next claim.
func TestOutboxWorker_StampsDeliveryAttemptedAtBeforeTheSend(t *testing.T) {
	t.Parallel()
	f := &fakeSMTP{}
	h := newOutboxHarness(t, f)
	fx := seedOutboxJob(t, h)
	w := communications.NewOutboxWorker(h.Deps())

	var stampedDuringSend bool
	f.onSend(func() {
		// A separate pool connection: if the marker write were inside the
		// worker's transaction it would still be uncommitted here.
		stampedDuringSend = readOutboxJob(t, h, fx.messageID).deliveryAttemptedAt != nil
	})

	if _, err := w.ProcessOne(context.Background()); err != nil {
		t.Fatalf("ProcessOne: %v", err)
	}
	if !stampedDuringSend {
		t.Error("delivery_attempted_at was not committed before the send (inventory §13.3 step 7)")
	}
	if readOutboxJob(t, h, fx.messageID).deliveryAttemptedAt == nil {
		t.Error("delivery_attempted_at was cleared on completion, want it retained")
	}
}

// ---- claim semantics ----

// TestOutboxWorker_ClaimClearsDeliveryAttemptedAt pins the property that makes
// the marker a *crash* signal and never a retry signal (inventory §13.2 step 4,
// D5): every claim clears it to NULL. The fixture leaves nothing sendable, so
// the run completes without sending and therefore never re-stamps — a NULL
// afterwards can only be the claim's own clear.
func TestOutboxWorker_ClaimClearsDeliveryAttemptedAt(t *testing.T) {
	t.Parallel()
	f := &fakeSMTP{}
	h := newOutboxHarness(t, f)
	fx := seedOutboxJob(t, h)

	// A crashed worker's leftovers: processing, lease expired, marker set.
	h.Exec(t, `UPDATE communications.outbox_jobs
	           SET status = 'processing', lease_id = $2, lease_until = $3, delivery_attempted_at = $4
	           WHERE message_id = $1`,
		fx.messageID, "0123456789abcdef0123456789abcdef", h.Now().Add(-time.Minute), h.Now().Add(-2*time.Minute))
	// Nothing sendable: relay_accepted is one of IsSendable's three excluded
	// statuses (inventory §13.2's IsSendable).
	h.Exec(t, `UPDATE communications.message_deliveries SET status = 'relay_accepted' WHERE message_id = $1`, fx.messageID)

	w := communications.NewOutboxWorker(h.Deps())
	processed, err := w.ProcessOne(context.Background())
	if err != nil {
		t.Fatalf("ProcessOne: %v", err)
	}
	if !processed {
		t.Fatal("ProcessOne = false, want true: an expired lease is claimable")
	}

	job := readOutboxJob(t, h, fx.messageID)
	if job.deliveryAttemptedAt != nil {
		t.Error("delivery_attempted_at survived the claim, want it cleared to NULL on every claim (inventory §13.2)")
	}
	if job.status != "completed" {
		t.Errorf("job status = %q, want completed (nothing sendable -> CompleteWithoutSending)", job.status)
	}
	if len(f.sends()) != 0 {
		t.Errorf("sends = %d, want 0: no sendable delivery means nothing is sent", len(f.sends()))
	}
	// The candidate was processing with the marker set, so the
	// possible-duplicate counter fires — on the lease-expired branch only.
	if got := w.PossibleDuplicateSends(); got != 1 {
		t.Errorf("possible-duplicate counter = %d, want 1", got)
	}
}

// TestOutboxWorker_ReclaimsAnExpiredLeaseAndResendsTheSameMessageID is
// TS/Integration/OutboxWorkerTests.cs:20-69 (inventory §13.6's crash-recovery
// bullet): a job left processing with an expired lease and the marker set is
// re-claimed, the counter increments exactly once, and the resend carries a
// byte-identical Message-Id.
func TestOutboxWorker_ReclaimsAnExpiredLeaseAndResendsTheSameMessageID(t *testing.T) {
	t.Parallel()
	f := &fakeSMTP{}
	h := newOutboxHarness(t, f)
	fx := seedOutboxJob(t, h)

	h.Exec(t, `UPDATE communications.outbox_jobs
	           SET status = 'processing', lease_id = $2, lease_until = $3, delivery_attempted_at = $4, attempts = 1
	           WHERE message_id = $1`,
		fx.messageID, "ffffffffffffffffffffffffffffffff", h.Now().Add(-time.Second), h.Now().Add(-time.Minute))
	h.Exec(t, `UPDATE communications.message_deliveries SET status = 'sending' WHERE message_id = $1`, fx.messageID)

	w := communications.NewOutboxWorker(h.Deps())
	if _, err := w.ProcessOne(context.Background()); err != nil {
		t.Fatalf("ProcessOne: %v", err)
	}

	if got := w.PossibleDuplicateSends(); got != 1 {
		t.Errorf("possible-duplicate counter = %d, want exactly 1", got)
	}
	sent := f.sends()
	if len(sent) != 1 {
		t.Fatalf("sends = %d, want 1", len(sent))
	}
	if got, want := sent[0].MessageID, expectedMessageID(fx.messageID); got != want {
		t.Errorf("Message-Id on resend = %q, want the same deterministic %q", got, want)
	}
	job := readOutboxJob(t, h, fx.messageID)
	if job.status != "completed" {
		t.Errorf("job status = %q, want completed", job.status)
	}
	if job.attempts != 2 {
		t.Errorf("attempts = %d, want 2 (the re-claim increments again)", job.attempts)
	}
}

// TestOutboxWorker_LiveLeaseIsNotClaimable is the other half of the lease
// rule: a processing job whose lease has NOT expired is invisible to the claim
// predicate, so a second worker finds nothing to do.
func TestOutboxWorker_LiveLeaseIsNotClaimable(t *testing.T) {
	t.Parallel()
	f := &fakeSMTP{}
	h := newOutboxHarness(t, f)
	fx := seedOutboxJob(t, h)

	h.Exec(t, `UPDATE communications.outbox_jobs
	           SET status = 'processing', lease_id = $2, lease_until = $3 WHERE message_id = $1`,
		fx.messageID, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", h.Now().Add(time.Minute))

	w := communications.NewOutboxWorker(h.Deps())
	processed, err := w.ProcessOne(context.Background())
	if err != nil {
		t.Fatalf("ProcessOne: %v", err)
	}
	if processed {
		t.Error("ProcessOne = true, want false: a live lease is not claimable")
	}
	if len(f.sends()) != 0 {
		t.Errorf("sends = %d, want 0", len(f.sends()))
	}
	if got := w.PossibleDuplicateSends(); got != 0 {
		t.Errorf("possible-duplicate counter = %d, want 0: the counter is for the lease-expired branch only", got)
	}
}

// TestOutboxWorker_FutureNextAttemptIsNotClaimable pins the other predicate
// term: a retry scheduled for later is not picked up early.
func TestOutboxWorker_FutureNextAttemptIsNotClaimable(t *testing.T) {
	t.Parallel()
	f := &fakeSMTP{}
	h := newOutboxHarness(t, f)
	fx := seedOutboxJob(t, h)

	h.Exec(t, `UPDATE communications.outbox_jobs SET status = 'retry', next_attempt_at = $2 WHERE message_id = $1`,
		fx.messageID, h.Now().Add(time.Minute))

	w := communications.NewOutboxWorker(h.Deps())
	processed, err := w.ProcessOne(context.Background())
	if err != nil {
		t.Fatalf("ProcessOne: %v", err)
	}
	if processed {
		t.Error("ProcessOne = true, want false: next_attempt_at is in the future")
	}
}

// ---- the non-sending completions ----

// TestOutboxWorker_NothingSendableCompletesWithoutSending is §13.3 step 3:
// a job whose every delivery is already in one of IsSendable's three excluded
// statuses completes without any send at all.
func TestOutboxWorker_NothingSendableCompletesWithoutSending(t *testing.T) {
	t.Parallel()
	f := &fakeSMTP{}
	h := newOutboxHarness(t, f)
	fx := seedOutboxJob(t, h)
	h.Exec(t, `UPDATE communications.message_deliveries SET status = 'suppressed' WHERE message_id = $1`, fx.messageID)

	w := communications.NewOutboxWorker(h.Deps())
	processed, err := w.ProcessOne(context.Background())
	if err != nil {
		t.Fatalf("ProcessOne: %v", err)
	}
	if !processed {
		t.Fatal("ProcessOne = false, want true: the job was touched")
	}
	if len(f.sends()) != 0 {
		t.Errorf("sends = %d, want 0", len(f.sends()))
	}
	job := readOutboxJob(t, h, fx.messageID)
	if job.status != "completed" || job.completedAt == nil {
		t.Errorf("job = (%s, completedAt %v), want completed with a stamp", job.status, job.completedAt)
	}
	if job.deliveryAttemptedAt != nil {
		t.Error("delivery_attempted_at is set, want null: step 7 is never reached when nothing is sendable")
	}
}

// TestOutboxWorker_SuppressedRecipientCancelsTheWholeJob is §13.3 steps 5-6:
// the worker re-checks suppressions itself (this is the ONLY place suppression
// is enforced post-port, since reply's own check is unreachable — design §1.1's
// sharpening), and one suppressed recipient cancels the entire job,
// all-or-nothing, without sending to anyone.
func TestOutboxWorker_SuppressedRecipientCancelsTheWholeJob(t *testing.T) {
	t.Parallel()
	f := &fakeSMTP{}
	h := newOutboxHarness(t, f)
	ccAddress := fmt.Sprintf("cc-%s@example.test", uuid.NewString())
	fx := seedOutboxJob(t, h, ccAddress)

	admin := h.SignIn(t, "communications:suppressions-manage")
	createSuppression(t, admin, map[string]any{"emailAddress": ccAddress}, 201)

	w := communications.NewOutboxWorker(h.Deps())
	processed, err := w.ProcessOne(context.Background())
	if err != nil {
		t.Fatalf("ProcessOne: %v", err)
	}
	if !processed {
		t.Fatal("ProcessOne = false, want true")
	}
	if len(f.sends()) != 0 {
		t.Errorf("sends = %d, want 0: a suppression hit cancels before the send", len(f.sends()))
	}

	job := readOutboxJob(t, h, fx.messageID)
	if job.status != "cancelled" {
		t.Errorf("job status = %q, want cancelled", job.status)
	}
	if job.completedAt == nil {
		t.Error("completed_at is null on the cancelled job, want the stamp")
	}
	if job.leaseID != nil || job.leaseUntil != nil {
		t.Error("lease survived the cancellation, want it cleared")
	}
	for _, d := range readDeliveries(t, h, fx.messageID) {
		if d.status != "suppressed" {
			t.Errorf("delivery %s status = %q, want suppressed (all-or-nothing)", d.address, d.status)
		}
		if d.lastError != nil {
			t.Errorf("delivery %s last_error = %q, want null", d.address, *d.lastError)
		}
	}
	if n, _ := countEvents(t, h, fx.messageID, "suppressed"); n != 2 {
		t.Errorf("suppressed events = %d, want 2 (one per delivery)", n)
	}
}

// ---- the failure path ----

// TestOutboxWorker_NonCleanAttachmentFailsTheJob is §13.3 step 4 and §5.5's
// fourth load-bearing "clean" site: a message carrying any non-clean
// attachment is refused before the send and goes down the failure path.
func TestOutboxWorker_NonCleanAttachmentFailsTheJob(t *testing.T) {
	t.Parallel()
	f := &fakeSMTP{}
	h := newOutboxHarness(t, f)
	fx := seedOutboxJob(t, h)

	h.Exec(t, `INSERT INTO communications.message_attachments
	           (id, message_id, file_name, content_type, size_bytes, content_hash, storage_key, scan_status, is_inline, created_at)
	           VALUES ($1, $2, 'quarantined.pdf', 'application/pdf', 10, 'hash', 'communications/staged/x', 'quarantined', false, $3)`,
		uuid.New(), fx.messageID, h.Now())

	w := communications.NewOutboxWorker(h.Deps())
	processed, err := w.ProcessOne(context.Background())
	if err != nil {
		t.Fatalf("ProcessOne: %v", err)
	}
	if !processed {
		t.Fatal("ProcessOne = false, want true: failing a job still counts as touching it")
	}
	if len(f.sends()) != 0 {
		t.Errorf("sends = %d, want 0", len(f.sends()))
	}

	job := readOutboxJob(t, h, fx.messageID)
	if job.status != "retry" {
		t.Errorf("job status = %q, want retry", job.status)
	}
	if job.lastError == nil || *job.lastError != "Outbound delivery failed." {
		t.Errorf("last_error = %v, want the constant %q (the exception text is never stored)", job.lastError, "Outbound delivery failed.")
	}
	if job.deliveryAttemptedAt != nil {
		t.Error("delivery_attempted_at is set, want null: the gate fires before step 7")
	}
	if got := job.nextAttemptAt.Sub(h.Now()); got != 2*time.Second {
		t.Errorf("backoff = %v, want 2s (attempts = 1 after the claim)", got)
	}
	for _, d := range readDeliveries(t, h, fx.messageID) {
		if d.status != "retrying" {
			t.Errorf("delivery status = %q, want retrying", d.status)
		}
		if d.lastError == nil || *d.lastError != "Outbound delivery failed." {
			t.Errorf("delivery last_error = %v, want the same constant", d.lastError)
		}
	}
	n, data := countEvents(t, h, fx.messageID, "retrying")
	if n != 1 {
		t.Errorf("retrying events = %d, want 1", n)
	}
	if data == nil || *data != `{"error":"Outbound delivery failed."}` {
		t.Errorf("retrying event data_json = %v, want the constant error object", data)
	}
}

// TestOutboxWorker_BackoffSequenceAndTerminality pins BOTH halves of §13.4's
// arithmetic, which the module documentation gets wrong (D1, design divergence
// 4): the sequence is 2, 4, 8, 16, 32, 64, 128 — starting at 2 s because
// attempts is incremented at claim — and the job goes terminal at
// attempts >= max_attempts (8), so 128 s is the largest backoff a default
// deployment ever actually waits. The documented 3600 s cap is unreachable
// dead code and 256/512/1024 are never scheduled here either.
func TestOutboxWorker_BackoffSequenceAndTerminality(t *testing.T) {
	t.Parallel()
	f := &fakeSMTP{}
	f.failWith(fmt.Errorf("smtp is down"))
	h := newOutboxHarness(t, f)
	fx := seedOutboxJob(t, h)
	w := communications.NewOutboxWorker(h.Deps())

	want := []time.Duration{2, 4, 8, 16, 32, 64, 128}
	for i, backoff := range want {
		processed, err := w.ProcessOne(context.Background())
		if err != nil {
			t.Fatalf("attempt %d: ProcessOne: %v", i+1, err)
		}
		if !processed {
			t.Fatalf("attempt %d: ProcessOne = false, want true", i+1)
		}
		job := readOutboxJob(t, h, fx.messageID)
		if job.status != "retry" {
			t.Fatalf("attempt %d: job status = %q, want retry", i+1, job.status)
		}
		if job.attempts != int32(i+1) {
			t.Errorf("attempt %d: attempts = %d, want %d", i+1, job.attempts, i+1)
		}
		if got := job.nextAttemptAt.Sub(h.Now()); got != backoff*time.Second {
			t.Errorf("attempt %d (attempts = %d): backoff = %v, want %v", i+1, job.attempts, got, backoff*time.Second)
		}
		if job.leaseID != nil {
			t.Errorf("attempt %d: lease survived the failure, want it cleared", i+1)
		}
		// Move to the scheduled retry time so the next claim sees the job.
		h.Advance(backoff * time.Second)
	}

	// The eighth attempt is the terminal one: attempts reaches 8 == the
	// default max_attempts, checked post-increment.
	processed, err := w.ProcessOne(context.Background())
	if err != nil {
		t.Fatalf("terminal attempt: ProcessOne: %v", err)
	}
	if !processed {
		t.Fatal("terminal attempt: ProcessOne = false, want true")
	}
	job := readOutboxJob(t, h, fx.messageID)
	if job.status != "failed" {
		t.Errorf("job status = %q, want failed at attempts >= 8", job.status)
	}
	if job.attempts != 8 {
		t.Errorf("attempts = %d, want 8", job.attempts)
	}
	for _, d := range readDeliveries(t, h, fx.messageID) {
		if d.status != "submission_failed" {
			t.Errorf("delivery status = %q, want submission_failed", d.status)
		}
	}
	if n, _ := countEvents(t, h, fx.messageID, "submission_failed"); n != 1 {
		t.Errorf("submission_failed events = %d, want 1", n)
	}
	// A terminal job is no longer claimable, however far the clock moves.
	h.Advance(time.Hour)
	if again, _ := w.ProcessOne(context.Background()); again {
		t.Error("a failed job was claimed again, want it terminal")
	}
	if got := len(f.sends()); got != 8 {
		t.Errorf("sends = %d, want 8 (every attempt reaches the SMTP call)", got)
	}
}

// ---- the stolen-lease paths: the module's principal duplicate source ----

// TestOutboxWorker_StolenLeaseMakesTheCompletionASilentNoOp is §13.3 step 10
// (`:1315`), the single most consequential line in the worker: if the lease
// changed while the send was in flight, the completion commits NOTHING — the
// mail has already gone out, the job stays processing, and the new lease
// holder will send it again. That is at-least-once delivery as .NET implements
// it, and this test exists so nobody "fixes" it into a silent data change.
func TestOutboxWorker_StolenLeaseMakesTheCompletionASilentNoOp(t *testing.T) {
	t.Parallel()
	f := &fakeSMTP{}
	h := newOutboxHarness(t, f)
	fx := seedOutboxJob(t, h)
	w := communications.NewOutboxWorker(h.Deps())

	const thief = "deadbeefdeadbeefdeadbeefdeadbeef"
	f.onSend(func() {
		h.Exec(t, `UPDATE communications.outbox_jobs SET lease_id = $2 WHERE message_id = $1`, fx.messageID, thief)
	})

	processed, err := w.ProcessOne(context.Background())
	if err != nil {
		t.Fatalf("ProcessOne: %v", err)
	}
	if !processed {
		t.Error("ProcessOne = false, want true: the job was handled, not errored")
	}
	if len(f.sends()) != 1 {
		t.Fatalf("sends = %d, want 1: the send really happened", len(f.sends()))
	}

	job := readOutboxJob(t, h, fx.messageID)
	if job.status != "processing" {
		t.Errorf("job status = %q, want processing: nothing is committed on a stolen lease", job.status)
	}
	if job.leaseID == nil || *job.leaseID != thief {
		t.Errorf("lease_id = %v, want the thief's %q untouched", job.leaseID, thief)
	}
	if job.completedAt != nil {
		t.Error("completed_at was written, want the whole completion to be a no-op")
	}
	for _, d := range readDeliveries(t, h, fx.messageID) {
		if d.status != "sending" {
			t.Errorf("delivery status = %q, want sending: the completion wrote nothing", d.status)
		}
	}
	if n, _ := countEvents(t, h, fx.messageID, "relay_accepted"); n != 0 {
		t.Errorf("relay_accepted events = %d, want 0", n)
	}
}

// TestOutboxWorker_StolenLeaseMakesTheFailureASilentNoOp is MarkFailedAsync's
// own guard (`:1332`): a worker whose lease was stolen does not get to record
// a failure either — no status change, no backoff, no delivery writes.
func TestOutboxWorker_StolenLeaseMakesTheFailureASilentNoOp(t *testing.T) {
	t.Parallel()
	f := &fakeSMTP{}
	f.failWith(fmt.Errorf("smtp is down"))
	h := newOutboxHarness(t, f)
	fx := seedOutboxJob(t, h)
	w := communications.NewOutboxWorker(h.Deps())

	const thief = "cafecafecafecafecafecafecafecafe"
	f.onSend(func() {
		h.Exec(t, `UPDATE communications.outbox_jobs SET lease_id = $2 WHERE message_id = $1`, fx.messageID, thief)
	})

	before := readOutboxJob(t, h, fx.messageID)
	if _, err := w.ProcessOne(context.Background()); err != nil {
		t.Fatalf("ProcessOne: %v", err)
	}

	job := readOutboxJob(t, h, fx.messageID)
	if job.status != "processing" {
		t.Errorf("job status = %q, want processing: MarkFailed is a no-op on a stolen lease", job.status)
	}
	if job.lastError != nil {
		t.Errorf("last_error = %q, want null", *job.lastError)
	}
	if !job.nextAttemptAt.Equal(before.nextAttemptAt) {
		t.Errorf("next_attempt_at = %v, want it unchanged at %v", job.nextAttemptAt, before.nextAttemptAt)
	}
	for _, d := range readDeliveries(t, h, fx.messageID) {
		if d.status != "sending" {
			t.Errorf("delivery status = %q, want sending", d.status)
		}
	}
}

// TestOutboxWorker_ChannelWithoutCredentialFallsBackToHostSmtpConfig is
// inventory §15.2 step 1's first branch: "credential is null -> fall back to
// host config SmtpOptions". It matters more than its obscure fixture suggests —
// this is how any deployment that never configured PER-CHANNEL credentials
// sends mail at all, i.e. the default configuration, and it reaches a different
// settings-resolution path from every other test in this file.
//
// The channels API creates a credential atomically with the channel, so the
// only way to reach the branch is to delete the credential row directly. That
// is the same hazard TestUpdateChannel_MissingCredentialWhenNoneExists already
// documents, not a contrivance: the resulting row shape is exactly what a
// credential-less channel looks like.
func TestOutboxWorker_ChannelWithoutCredentialFallsBackToHostSmtpConfig(t *testing.T) {
	t.Parallel()
	f := &fakeSMTP{}
	h := newHarness(t,
		modtest.WithSMTPSend(f.send),
		modtest.WithEnv("MAIL_DRIVER", "smtp"),
		modtest.WithEnv("SMTP_HOST", "host-smtp.example.test"),
		modtest.WithEnv("SMTP_PORT", "2525"),
		modtest.WithEnv("SMTP_FROM", "host-noreply@example.test"),
		modtest.WithEnv("SMTP_USERNAME", "host-user"),
		modtest.WithEnv("SMTP_PASSWORD", "host-password"),
	)
	fx := seedOutboxJob(t, h)
	h.Exec(t, `DELETE FROM communications.channel_credentials WHERE channel_id = $1`, fx.channelID)

	w := communications.NewOutboxWorker(h.Deps())
	processed, err := w.ProcessOne(context.Background())
	if err != nil {
		t.Fatalf("ProcessOne: %v", err)
	}
	if !processed {
		t.Fatal("ProcessOne = false, want true")
	}

	cfgs := f.configs()
	if len(cfgs) != 1 {
		t.Fatalf("sends = %d, want exactly 1: a credential-less channel still sends, on the host settings", len(cfgs))
	}
	got := cfgs[0]
	if got.Driver != "smtp" {
		t.Errorf("Driver = %q, want smtp", got.Driver)
	}
	if got.Host != "host-smtp.example.test" || got.Port != 2525 {
		t.Errorf("host/port = %s:%d, want the process SMTP_HOST/SMTP_PORT", got.Host, got.Port)
	}
	if got.Username != "host-user" || got.Password != "host-password" {
		t.Errorf("credentials = %q/%q, want the process SMTP_USERNAME/SMTP_PASSWORD", got.Username, got.Password)
	}
	if got.TLS != "starttls" {
		t.Errorf("TLS = %q, want starttls (SMTP_TLS's default)", got.TLS)
	}
	// The envelope's From is the CHANNEL's address, not the host SMTP_FROM:
	// the fallback supplies the server, never the sending identity.
	if got.From != fx.channelAddress {
		t.Errorf("From = %q, want the channel address %q, not the host SMTP_FROM", got.From, fx.channelAddress)
	}
	if job := readOutboxJob(t, h, fx.messageID); job.status != "completed" {
		t.Errorf("job status = %q, want completed", job.status)
	}
}

// ---- the worker port ----

// TestOutboxWorker_RunDrainsTheQueueAndStopsWithTheContext proves the Run loop
// the platform runner starts: it drains what is queued and returns when its
// context is done, rather than running forever or returning early.
func TestOutboxWorker_RunDrainsTheQueueAndStopsWithTheContext(t *testing.T) {
	t.Parallel()
	f := &fakeSMTP{}
	h := newOutboxHarness(t, f)
	fx := seedOutboxJob(t, h)
	w := communications.NewOutboxWorker(h.Deps())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()

	deadline := time.Now().Add(10 * time.Second)
	for readOutboxJob(t, h, fx.messageID).status != "completed" {
		if time.Now().After(deadline) {
			t.Fatal("the worker never completed the queued job")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned %v, want nil on cancellation", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after its context was cancelled")
	}
}

// TestCommunicationsModule_ContributesTheOutboxWorker proves the worker is
// reachable the way production starts it — through Module().Workers, which
// module.Workers collects for cmd/vantigo's runner — and not only through the
// constructor these tests call directly.
func TestCommunicationsModule_ContributesTheOutboxWorker(t *testing.T) {
	t.Parallel()
	f := &fakeSMTP{}
	h := newOutboxHarness(t, f)

	workers := module.Workers(h.Deps(), communications.Module())
	var names []string
	for _, w := range workers {
		names = append(names, w.Name())
		if w.Interval() <= 0 {
			t.Errorf("worker %s has interval %v, want a positive poll interval", w.Name(), w.Interval())
		}
	}
	found := false
	for _, n := range names {
		if n == "communications-outbox" {
			found = true
		}
	}
	if !found {
		t.Errorf("workers = %v, want one named communications-outbox", names)
	}
}
