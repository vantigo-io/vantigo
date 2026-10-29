package communications_test

import (
	"bytes"
	"context"
	"errors"
	netmail "net/mail"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/communications"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is the outbox's Microsoft 365 path (Outlook connection design
// M13, delivery C's task 6): a job on an 'entra' channel is rendered as MIME
// from the same Outbound the SMTP path sends and handed to ChannelMailbox.Send
// with the blind copies beside it. modtest.FakeChannelMailbox stands in for
// Entra's provider; every harness's own clean-up fails the test if the slot
// was called from inside one of the module's transactions (harness_test.go).

// stampingMailbox is the fake with a hook run inside Send, where a test can
// read the database as the worker sees it mid-flight.
type stampingMailbox struct {
	*modtest.FakeChannelMailbox
	during func()
}

func (m *stampingMailbox) Send(ctx context.Context, mailbox string, mime []byte, bcc []string) error {
	if m.during != nil {
		m.during()
	}
	return m.FakeChannelMailbox.Send(ctx, mailbox, mime, bcc)
}

// mailboxJob is one queued message on a Microsoft 365 channel.
type mailboxJob struct {
	mailbox   contracts.ChannelMailboxInfo
	messageID uuid.UUID
	to, cc    string
	bcc       []string
}

// seedMailboxJob creates a Microsoft 365 channel on fake's shared mailbox —
// the harness's default channel — and a conversation on it to one To, one
// Cc and the bcc given, through the real API.
func seedMailboxJob(t *testing.T, h *modtest.Harness, fake *modtest.FakeChannelMailbox, bcc ...string) mailboxJob {
	t.Helper()
	admin := h.SignIn(t, "communications:channels-manage")
	address := channelAddress(t)
	info := sharedMailbox(fake, address, "")
	createChannel(t, admin, entraChannelBody(address))

	job := mailboxJob{
		mailbox: info,
		to:      "to-" + uuid.NewString() + "@example.test",
		cc:      "cc-" + uuid.NewString() + "@example.test",
		bcc:     bcc,
	}
	recipients := []map[string]any{
		{"address": job.to, "type": "to"},
		{"address": job.cc, "type": "cc"},
	}
	for _, a := range bcc {
		recipients = append(recipients, map[string]any{"address": a, "type": "bcc"})
	}
	c := h.SignIn(t, "communications:conversations-reply")
	created := createConversation(t, c, newConversationBodyWithRecipients(recipients))
	if created.MessageId == nil {
		t.Fatal("create conversation returned no messageId")
	}
	job.messageID = uuid.MustParse(*created.MessageId)
	return job
}

// sameAddresses reports whether got and want name the same addresses, in
// order, ignoring case (deliveries store the address as the participant's).
func sameAddresses(got, want []string) bool {
	return slices.EqualFunc(got, want, strings.EqualFold)
}

// headerLine reports whether raw carries the header line name: value
// exactly, byte for byte (the name in go-mail's own spelling).
func headerLine(raw []byte, name, value string) bool {
	return bytes.Contains(raw, []byte("\r\n"+name+": "+value+"\r\n")) ||
		bytes.HasPrefix(raw, []byte(name+": "+value+"\r\n"))
}

// TestOutboxWorker_MailboxChannelSendsThroughTheMailbox: the job goes out
// once through Send to the channel's mailbox, as MIME carrying our
// Message-Id, In-Reply-To and References byte for byte, To and Cc, and the
// Bcc both in the MIME and beside it; the deliveries are relay_accepted, the
// job completed, and nothing goes over SMTP.
func TestOutboxWorker_MailboxChannelSendsThroughTheMailbox(t *testing.T) {
	t.Parallel()
	smtp := &fakeSMTP{}
	fake := modtest.NewFakeChannelMailbox()
	h := newHarness(t, modtest.WithChannelMailbox(fake), modtest.WithSMTPSend(smtp.send))
	bcc := "bcc-" + uuid.NewString() + "@example.test"
	fx := seedMailboxJob(t, h, fake, bcc)
	h.Exec(t, `UPDATE communications.conversation_messages SET channel_metadata_json = $2::jsonb WHERE id = $1`,
		fx.messageID, `{"inReplyTo":"<parent@example.test>","references":["<a@example.test>","<b@example.test>"]}`)

	w := communications.NewOutboxWorker(h.Deps())
	if processed, err := w.ProcessOne(context.Background()); err != nil || !processed {
		t.Fatalf("ProcessOne = %v, %v; want true, nil", processed, err)
	}

	sent := fake.Sent()
	if len(sent) != 1 {
		t.Fatalf("Send calls = %d, want exactly 1", len(sent))
	}
	if sent[0].Mailbox != fx.mailbox.Mailbox {
		t.Errorf("Send mailbox = %q, want the channel's %q", sent[0].Mailbox, fx.mailbox.Mailbox)
	}
	if !sameAddresses(sent[0].Bcc, []string{bcc}) {
		t.Errorf("Send bcc = %v, want [%s] beside the MIME", sent[0].Bcc, bcc)
	}
	raw := sent[0].MIME
	for _, line := range [][2]string{
		{"Message-ID", "<" + expectedMessageID(fx.messageID) + ">"},
		{"In-Reply-To", "<parent@example.test>"},
		{"References", "<a@example.test> <b@example.test>"},
	} {
		if !headerLine(raw, line[0], line[1]) {
			t.Errorf("the MIME has no line %q, want it byte for byte:\n%s", line[0]+": "+line[1], raw)
		}
	}
	msg, err := netmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("the MIME does not parse: %v", err)
	}
	for header, want := range map[string]string{"To": fx.to, "Cc": fx.cc, "Bcc": bcc} {
		list, err := msg.Header.AddressList(header)
		if err != nil || len(list) != 1 || !strings.EqualFold(list[0].Address, want) {
			t.Errorf("%s = %v (%v), want [%s]", header, list, err, want)
		}
	}
	if got := msg.Header.Get("Subject"); !strings.HasPrefix(got, "Subject ") {
		t.Errorf("Subject = %q, want the message's", got)
	}

	job := readOutboxJob(t, h, fx.messageID)
	if job.status != "completed" || job.attempts != 1 {
		t.Errorf("job = %s after %d attempts, want completed after 1", job.status, job.attempts)
	}
	deliveries := readDeliveries(t, h, fx.messageID)
	if len(deliveries) != 3 {
		t.Fatalf("deliveries = %d, want 3", len(deliveries))
	}
	for _, d := range deliveries {
		if d.status != "relay_accepted" || d.acceptedAt == nil {
			t.Errorf("delivery %s (%s) = %s, want relay_accepted with its stamp", d.address, d.kind, d.status)
		}
	}
	if n, _ := countEvents(t, h, fx.messageID, "relay_accepted"); n != 3 {
		t.Errorf("relay_accepted events = %d, want 3", n)
	}
	if got := len(smtp.sends()); got != 0 {
		t.Errorf("SMTP sends = %d, want none for a Microsoft 365 channel", got)
	}
}

// TestOutboxWorker_MailboxChannelWithoutBccRendersNoBccHeader: no blind copy,
// no Bcc header, and nothing beside the MIME.
func TestOutboxWorker_MailboxChannelWithoutBccRendersNoBccHeader(t *testing.T) {
	t.Parallel()
	fake := modtest.NewFakeChannelMailbox()
	h := newHarness(t, modtest.WithChannelMailbox(fake))
	fx := seedMailboxJob(t, h, fake)

	if _, err := communications.NewOutboxWorker(h.Deps()).ProcessOne(context.Background()); err != nil {
		t.Fatalf("ProcessOne: %v", err)
	}
	sent := fake.Sent()
	if len(sent) != 1 {
		t.Fatalf("Send calls = %d, want 1", len(sent))
	}
	if len(sent[0].Bcc) != 0 {
		t.Errorf("Send bcc = %v, want none", sent[0].Bcc)
	}
	msg, err := netmail.ReadMessage(bytes.NewReader(sent[0].MIME))
	if err != nil {
		t.Fatalf("the MIME does not parse: %v", err)
	}
	if got := msg.Header["Bcc"]; len(got) != 0 {
		t.Errorf("Bcc = %q, want no header", got)
	}
	if job := readOutboxJob(t, h, fx.messageID); job.status != "completed" {
		t.Errorf("job = %s, want completed", job.status)
	}
}

// TestOutboxWorker_MailboxChannelStampsBeforeTheSend: delivery_attempted_at is
// committed before Send runs, as before an SMTP send — the crash marker the
// possible-duplicate count reads.
func TestOutboxWorker_MailboxChannelStampsBeforeTheSend(t *testing.T) {
	t.Parallel()
	fake := modtest.NewFakeChannelMailbox()
	mailbox := &stampingMailbox{FakeChannelMailbox: fake}
	h := newHarness(t, modtest.WithChannelMailbox(mailbox))
	fx := seedMailboxJob(t, h, fake)

	var stamped bool
	mailbox.during = func() { stamped = readOutboxJob(t, h, fx.messageID).deliveryAttemptedAt != nil }
	if _, err := communications.NewOutboxWorker(h.Deps()).ProcessOne(context.Background()); err != nil {
		t.Fatalf("ProcessOne: %v", err)
	}
	if len(fake.Sent()) != 1 {
		t.Fatalf("Send calls = %d, want 1", len(fake.Sent()))
	}
	if !stamped {
		t.Error("delivery_attempted_at was not committed before Send")
	}
}

// assertFailedAtOnce checks the job failed terminally on its first attempt
// with code in the submission_failed events' data, the constant everywhere a
// message is stored, and jobsFailed counted once.
func assertFailedAtOnce(t *testing.T, h *modtest.Harness, m *meterHarness, w *communications.OutboxWorker,
	messageID uuid.UUID, code string) {
	t.Helper()
	job := readOutboxJob(t, h, messageID)
	if job.status != "failed" || job.attempts != 1 {
		t.Errorf("job = %s after %d attempts, want failed after 1", job.status, job.attempts)
	}
	if job.lastError == nil || *job.lastError != "Outbound delivery failed." {
		t.Errorf("job last_error = %v, want the constant", job.lastError)
	}
	deliveries := readDeliveries(t, h, messageID)
	for _, d := range deliveries {
		if d.status != "submission_failed" {
			t.Errorf("delivery %s = %s, want submission_failed", d.address, d.status)
		}
		if d.lastError == nil || *d.lastError != "Outbound delivery failed." {
			t.Errorf("delivery last_error = %v, want the constant", d.lastError)
		}
	}
	n, data := countEvents(t, h, messageID, "submission_failed")
	if n != len(deliveries) {
		t.Errorf("submission_failed events = %d, want %d", n, len(deliveries))
	}
	want := `{"error":"Outbound delivery failed.","code":"` + code + `"}`
	if data == nil || *data != want {
		t.Errorf("event data = %v, want %s", data, want)
	}
	if got := m.count(t, jobsFailedMetric); got != 1 {
		t.Errorf("%s = %d, want 1", jobsFailedMetric, got)
	}
	if got := m.count(t, jobsRetriedMetric); got != 0 {
		t.Errorf("%s = %d, want 0", jobsRetriedMetric, got)
	}
	h.Advance(time.Hour)
	if again, err := w.ProcessOne(context.Background()); err != nil || again {
		t.Errorf("ProcessOne after the failure = %v, %v; want the failed job never claimed again", again, err)
	}
}

// TestOutboxWorker_MailboxRefusalsFailTheJobAtOnce: the mailbox's denial, its
// absence and a message too large are answers, not outages — the job fails
// on its first attempt with the code in the event, never retried.
func TestOutboxWorker_MailboxRefusalsFailTheJobAtOnce(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		err  error
		code string
	}{
		{contracts.ErrMailboxDenied, "mailbox_denied"},
		{contracts.ErrMailboxGone, "mailbox_gone"},
		{contracts.ErrMailboxUnknown, "mailbox_unknown"},
		{contracts.ErrMailTooLarge, "too_large"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			t.Parallel()
			fake := modtest.NewFakeChannelMailbox()
			h := newHarness(t, modtest.WithChannelMailbox(fake))
			fx := seedMailboxJob(t, h, fake)
			fake.FailWith("Send", tc.err)
			m := newMeterHarness(t)
			w := m.worker(t, h)

			if _, err := w.ProcessOne(context.Background()); err != nil {
				t.Fatalf("ProcessOne: %v", err)
			}
			assertFailedAtOnce(t, h, m, w, fx.messageID, tc.code)
		})
	}
}

// TestOutboxWorker_MailboxChannelWithoutAMailboxIsMisconfigured: settings
// naming no mailbox fail the job at once as channel_misconfigured, and
// nothing is handed to Send.
func TestOutboxWorker_MailboxChannelWithoutAMailboxIsMisconfigured(t *testing.T) {
	t.Parallel()
	fake := modtest.NewFakeChannelMailbox()
	h := newHarness(t, modtest.WithChannelMailbox(fake))
	fx := seedMailboxJob(t, h, fake)
	h.Exec(t, `UPDATE communications.channels SET settings = '{}' WHERE provider = 'entra'`)
	m := newMeterHarness(t)
	w := m.worker(t, h)

	if _, err := w.ProcessOne(context.Background()); err != nil {
		t.Fatalf("ProcessOne: %v", err)
	}
	if calls := sendCalls(fake); calls != 0 {
		t.Errorf("Send calls = %d, want none", calls)
	}
	assertFailedAtOnce(t, h, m, w, fx.messageID, "channel_misconfigured")
}

// sendCalls is how many times Send was called on fake, refused or not.
func sendCalls(fake *modtest.FakeChannelMailbox) int {
	n := 0
	for _, c := range fake.Calls() {
		if c.Method == "Send" {
			n++
		}
	}
	return n
}

// assertRetried checks the job is waiting for its retry on the backoff, the
// event's data the constant with no code, and jobsRetried counted once.
func assertRetried(t *testing.T, h *modtest.Harness, m *meterHarness, messageID uuid.UUID) {
	t.Helper()
	job := readOutboxJob(t, h, messageID)
	if job.status != "retry" || job.attempts != 1 {
		t.Errorf("job = %s after %d attempts, want retry after 1", job.status, job.attempts)
	}
	if got := job.nextAttemptAt.Sub(h.Now()); got != 2*time.Second {
		t.Errorf("backoff = %v, want 2s", got)
	}
	for _, d := range readDeliveries(t, h, messageID) {
		if d.status != "retrying" {
			t.Errorf("delivery %s = %s, want retrying", d.address, d.status)
		}
	}
	if _, data := countEvents(t, h, messageID, "retrying"); data == nil || *data != `{"error":"Outbound delivery failed."}` {
		t.Errorf("retrying event data = %v, want the constant with no code", data)
	}
	if got := m.count(t, jobsRetriedMetric); got != 1 {
		t.Errorf("%s = %d, want 1", jobsRetriedMetric, got)
	}
	if got := m.count(t, jobsFailedMetric); got != 0 {
		t.Errorf("%s = %d, want 0", jobsFailedMetric, got)
	}
}

// TestOutboxWorker_MailboxOutagesAreRetried: an unavailable provider and an
// error the contract does not name are failures to retry on the backoff.
func TestOutboxWorker_MailboxOutagesAreRetried(t *testing.T) {
	t.Parallel()
	for name, cause := range map[string]error{
		"unavailable": contracts.ErrMailboxUnavailable,
		"unknown":     errors.New("graph answered 503"),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fake := modtest.NewFakeChannelMailbox()
			h := newHarness(t, modtest.WithChannelMailbox(fake))
			fx := seedMailboxJob(t, h, fake)
			fake.FailWith("Send", cause)
			m := newMeterHarness(t)

			if _, err := m.worker(t, h).ProcessOne(context.Background()); err != nil {
				t.Fatalf("ProcessOne: %v", err)
			}
			assertRetried(t, h, m, fx.messageID)
		})
	}
}

// TestOutboxWorker_MailboxChannelWithoutItsProviderIsRetried: a channel is
// never sent through a provider under another key, nor through SMTP — with
// no slot, or a slot under another key, the job waits for its retry and
// nothing is sent.
func TestOutboxWorker_MailboxChannelWithoutItsProviderIsRetried(t *testing.T) {
	t.Parallel()
	t.Run("another key", func(t *testing.T) {
		t.Parallel()
		smtp := &fakeSMTP{}
		fake := modtest.NewFakeChannelMailbox()
		h := newHarness(t, modtest.WithChannelMailbox(fake), modtest.WithSMTPSend(smtp.send))
		fx := seedMailboxJob(t, h, fake)
		fake.WithKey("google")
		m := newMeterHarness(t)

		if _, err := m.worker(t, h).ProcessOne(context.Background()); err != nil {
			t.Fatalf("ProcessOne: %v", err)
		}
		if calls := sendCalls(fake); calls != 0 {
			t.Errorf("Send calls = %d, want none through a provider under another key", calls)
		}
		if got := len(smtp.sends()); got != 0 {
			t.Errorf("SMTP sends = %d, want none", got)
		}
		assertRetried(t, h, m, fx.messageID)
	})
	t.Run("no slot", func(t *testing.T) {
		t.Parallel()
		smtp := &fakeSMTP{}
		fake := modtest.NewFakeChannelMailbox()
		h := newHarness(t, modtest.WithChannelMailbox(fake), modtest.WithSMTPSend(smtp.send))
		fx := seedMailboxJob(t, h, fake)
		m := newMeterHarness(t)
		deps := h.Deps()
		deps.ChannelMailbox = nil

		if _, err := communications.NewOutboxWorkerWithMeterForTest(deps, m.provider).ProcessOne(context.Background()); err != nil {
			t.Fatalf("ProcessOne: %v", err)
		}
		if calls := sendCalls(fake); calls != 0 {
			t.Errorf("Send calls = %d, want none", calls)
		}
		if got := len(smtp.sends()); got != 0 {
			t.Errorf("SMTP sends = %d, want none", got)
		}
		assertRetried(t, h, m, fx.messageID)
	})
}
