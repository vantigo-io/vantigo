package communications_test

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/communications"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// This file is the outbox's messenger branch (Messenger design N6, N7, N12,
// delivery A): a message sent as ordered parts through the fake provider, the
// progress of each part in messenger_parts, a job claimed again skipping the
// parts already sent, a part attempted and never sent counted as a possible
// duplicate, the lease renewed between parts, the window checked again at send
// time and the provider's errors mapped onto the outbox's outcomes.

// outbox is an outbox worker, as one replica.
func (f chatFixture) outbox() *communications.OutboxWorker {
	return communications.NewOutboxWorker(f.h.Deps())
}

// drainOutbox runs the worker until no job is due.
func (f chatFixture) drainOutbox(t *testing.T, w *communications.OutboxWorker) {
	t.Helper()
	for range 20 {
		processed, err := w.ProcessOne(context.Background())
		if err != nil {
			t.Fatalf("outbox ProcessOne: %v", err)
		}
		if !processed {
			return
		}
	}
	t.Fatal("the outbox kept finding work")
}

// stage stages a file in conversation and answers its id.
func (f chatFixture) stage(t *testing.T, conversation, name string, data []byte) string {
	t.Helper()
	r := stageAttachment(t, f.c, conversation, uuid.NewString(), name, "image/png", data)
	if r.Status != http.StatusCreated {
		t.Fatalf("stage: status %d body %s, want 201", r.Status, r.Body)
	}
	var up attachmentUploadJSON
	r.JSON(&up)
	return up.Id
}

type partRow struct {
	index     int32
	attempted bool
	platform  string
	sent      bool
}

// parts is a message's messenger_parts rows, by index.
func (f chatFixture) parts(t *testing.T, message uuid.UUID) []partRow {
	t.Helper()
	rows, err := f.h.Pool().Query(context.Background(), `
		SELECT part_index, attempted_at IS NOT NULL, coalesce(platform_message_id, ''), sent_at IS NOT NULL
		FROM communications.messenger_parts WHERE message_id = $1 ORDER BY part_index`, message)
	if err != nil {
		t.Fatalf("read parts: %v", err)
	}
	defer rows.Close()
	var out []partRow
	for rows.Next() {
		var p partRow
		if err := rows.Scan(&p.index, &p.attempted, &p.platform, &p.sent); err != nil {
			t.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}

// failureCode is the code of a message's latest failure event.
func (f chatFixture) failureCode(t *testing.T, message uuid.UUID) string {
	t.Helper()
	return modtestOne(t, f, `
		SELECT coalesce((SELECT data_json::jsonb->>'code' FROM communications.message_events
		                 WHERE message_id = $1 AND event_type IN ('submission_failed', 'retrying')
		                 ORDER BY occurred_at DESC, id DESC LIMIT 1), '')`, message)
}

func modtestOne(t *testing.T, f chatFixture, sql string, args ...any) string {
	t.Helper()
	var out string
	if err := f.h.Pool().QueryRow(context.Background(), sql, args...).Scan(&out); err != nil {
		t.Fatalf("query: %v", err)
	}
	return out
}

// TestOutboxMessenger_SendsInOrderedPartsWithTheOptionsOnTheLast: the text, then
// each file, one part each, the answer options and the e-mail request on the last
// part only; each part is recorded with the platform's id, the job completes and
// the delivery is accepted.
func TestOutboxMessenger_SendsInOrderedPartsWithTheOptionsOnTheLast(t *testing.T) {
	t.Parallel()
	f := newChat(t)
	conv := f.chat(t)
	first, second := f.stage(t, conv, "one.png", []byte("ONE")), f.stage(t, conv, "two.png", []byte("TWO"))
	message := f.mustReply(t, conv, map[string]any{
		"textBody": "Here you are", "attachmentIds": []string{first, second},
		"components": map[string]any{"answerOptions": yesNo()["answerOptions"], "shareEmail": true},
	})

	f.drainOutbox(t, f.outbox())
	sent := f.fake.Sent()
	if len(sent) != 3 {
		t.Fatalf("parts sent = %d, want the text and the two files", len(sent))
	}
	if sent[0].Part.Text != "Here you are" || sent[0].Part.Attachment != nil || sent[1].Part.Attachment == nil || sent[2].Part.Attachment == nil {
		t.Errorf("parts = %+v, want the text, then the two files in order", sent)
	}
	// The files go in a stable order — by when they were attached, then by id — so
	// a job claimed again counts its parts the same way.
	if got := string(sent[1].Part.Attachment.Content) + string(sent[2].Part.Attachment.Content); got != "ONETWO" && got != "TWOONE" {
		t.Errorf("files = %q, want the two staged files", got)
	}
	for i, s := range sent {
		if s.Address != f.channel.Address || s.Part.Participant != "4242" || s.Part.MessageID != message.String() || s.Part.HumanAgent {
			t.Errorf("part %d = %+v, want the Page, the participant, the message and no tag", i, s)
		}
		last := i == len(sent)-1
		if last != (len(s.Part.AnswerOptions) == 2) || last != s.Part.ShareEmail {
			t.Errorf("part %d carries options %v and e-mail %v, want them on the last only", i, s.Part.AnswerOptions, s.Part.ShareEmail)
		}
	}
	parts := f.parts(t, message)
	if len(parts) != 3 {
		t.Fatalf("part rows = %+v, want 3", parts)
	}
	for i, p := range parts {
		if !p.attempted || !p.sent || p.platform != sent[i].Sent.PlatformMessageID {
			t.Errorf("part row %d = %+v, want attempted, sent and the platform's id %q", i, p, sent[i].Sent.PlatformMessageID)
		}
	}
	job := readOutboxJob(t, f.h, message)
	if job.status != "completed" || job.deliveryAttemptedAt != nil {
		t.Errorf("job = %+v, want completed with no job-level marker: each part has its own", job)
	}
	if d := readDeliveries(t, f.h, message); len(d) != 1 || d[0].status != "relay_accepted" || d[0].acceptedAt == nil {
		t.Errorf("deliveries = %+v, want one accepted", d)
	}
	if n, _ := countEvents(t, f.h, message, "relay_accepted"); n != 1 {
		t.Errorf("relay_accepted events = %d, want 1", n)
	}
}

// TestOutboxMessenger_ACarouselIsTheLastPartAndItsImagesAreNeverParts: the cards
// of a message are one carousel part after the files, built from the card
// attachments; the answer options ride on it, and no card image is a part of its
// own.
func TestOutboxMessenger_ACarouselIsTheLastPartAndItsImagesAreNeverParts(t *testing.T) {
	t.Parallel()
	f := newChat(t)
	conv := f.chat(t)
	file := f.stage(t, conv, "file.png", []byte("FILE"))
	message := f.mustReply(t, conv, map[string]any{"textBody": "Look", "attachmentIds": []string{file}, "components": yesNo()})

	// What the delivery that attaches products writes: the card's image, an
	// attachment of role card, and the cards on the message.
	image := f.stage(t, conv, "card.png", []byte("CARD"))
	card := uuid.New()
	f.h.Exec(t, `
		INSERT INTO communications.message_attachments
		 (id, message_id, file_name, content_type, size_bytes, content_hash, storage_key, role, is_inline, created_at)
		SELECT $1, $2, file_name, content_type, size_bytes, content_hash, storage_key, 'card', false, now()
		FROM communications.attachment_uploads WHERE id = $3`, card, message, image)
	f.h.Exec(t, `
		UPDATE communications.conversation_messages
		SET components = jsonb_build_object('answerOptions', components->'answerOptions',
		    'cards', jsonb_build_array(jsonb_build_object('title', 'Chair', 'subtitle', '100 kr', 'attachmentId', $2::text,
		        'choices', jsonb_build_array(jsonb_build_object('id', 'buy', 'title', 'Buy')))))
		WHERE id = $1`, message, card.String())

	f.drainOutbox(t, f.outbox())
	sent := f.fake.Sent()
	if len(sent) != 3 {
		t.Fatalf("parts sent = %d, want the text, the file and the carousel", len(sent))
	}
	carousel := sent[2].Part
	if len(carousel.Cards) != 1 || carousel.Cards[0].Title != "Chair" || carousel.Cards[0].Subtitle != "100 kr" ||
		carousel.Cards[0].Image == nil || string(carousel.Cards[0].Image.Content) != "CARD" ||
		len(carousel.Cards[0].Choices) != 1 || carousel.Cards[0].Choices[0].ID != "buy" {
		t.Errorf("carousel = %+v, want the card with its image and its choice", carousel)
	}
	if len(carousel.AnswerOptions) != 2 {
		t.Errorf("carousel options = %v, want the answer options on the last part", carousel.AnswerOptions)
	}
	for i, s := range sent[:2] {
		if len(s.Part.Cards) != 0 || len(s.Part.AnswerOptions) != 0 {
			t.Errorf("part %d carries %+v, want plain text and a plain file", i, s.Part)
		}
	}
	if sent[1].Part.Attachment == nil || string(sent[1].Part.Attachment.Content) != "FILE" {
		t.Error("the second part is not the file: a card image is never a part of its own")
	}
	if job := readOutboxJob(t, f.h, message); job.status != "completed" {
		t.Errorf("job = %q, want completed", job.status)
	}
}

// TestOutboxMessenger_AReclaimAfterPart2Of3SkipsTheSentParts: the third of three
// parts fails and the job is retried: the first two, recorded as sent, are not
// sent again, and the third — attempted and never sent — is counted as a
// possible duplicate and sent once more.
func TestOutboxMessenger_AReclaimAfterPart2Of3SkipsTheSentParts(t *testing.T) {
	t.Parallel()
	f := newChat(t)
	conv := f.chat(t)
	first, second := f.stage(t, conv, "one.png", []byte("ONE")), f.stage(t, conv, "two.png", []byte("TWO"))
	message := f.mustReply(t, conv, map[string]any{"textBody": "Three parts", "attachmentIds": []string{first, second}})
	f.fake.FailSend(3, contracts.MessengerTemporary(errors.New("throttled")))

	meter := newMeterHarness(t)
	w := communications.NewOutboxWorkerWithMeterForTest(f.h.Deps(), meter.provider)
	f.drainOutbox(t, w)
	if got := len(f.fake.Sent()); got != 2 {
		t.Fatalf("parts sent = %d, want the first two", got)
	}
	job := readOutboxJob(t, f.h, message)
	if job.status != "retry" || job.attempts != 1 {
		t.Fatalf("job = %+v, want a retry after one attempt", job)
	}
	parts := f.parts(t, message)
	if len(parts) != 3 || !parts[0].sent || !parts[1].sent || parts[2].sent || !parts[2].attempted {
		t.Fatalf("parts = %+v, want two sent and the third attempted only", parts)
	}
	if got := meter.count(t, possibleDuplicateSendsMetric); got != 0 {
		t.Errorf("possible duplicates = %d, want none yet", got)
	}

	f.h.Advance(time.Hour)
	f.drainOutbox(t, w)
	sent := f.fake.Sent()
	if len(sent) != 3 || sent[2].Part.Attachment == nil {
		t.Fatalf("parts sent = %d, want exactly one more, the third", len(sent))
	}
	if sent[2].Part.Attachment.FileName == sent[1].Part.Attachment.FileName {
		t.Error("a part recorded as sent was sent again: the third part is the second's file")
	}
	if got := f.fake.CallCount("Send"); got != 4 {
		t.Errorf("Send calls = %d, want 4: two, the failed one, and the third again", got)
	}
	if got := meter.count(t, possibleDuplicateSendsMetric); got != 1 {
		t.Errorf("possible duplicates = %d, want the third part's one", got)
	}
	if job := readOutboxJob(t, f.h, message); job.status != "completed" || job.attempts != 2 {
		t.Errorf("job = %+v, want completed on the second attempt", job)
	}
	for i, p := range f.parts(t, message) {
		if !p.sent || p.platform == "" {
			t.Errorf("part %d = %+v, want sent with its id", i, p)
		}
	}
}

// TestOutboxMessenger_APartAttemptedAndNeverSentIsAPossibleDuplicate: a job
// reclaimed after its worker died inside the third part's Send has a part with an
// attempt and no sent time: logged, counted in the existing metric, and sent
// again, the sent ones skipped.
func TestOutboxMessenger_APartAttemptedAndNeverSentIsAPossibleDuplicate(t *testing.T) {
	t.Parallel()
	f := newChat(t)
	conv := f.chat(t)
	file := f.stage(t, conv, "one.png", []byte("ONE"))
	message := f.mustReply(t, conv, map[string]any{"textBody": "Two parts", "attachmentIds": []string{file}})
	// A first holder that sent the text, then died inside the file's Send.
	f.h.Exec(t, `
		INSERT INTO communications.messenger_parts (message_id, part_index, attempted_at, platform_message_id, sent_at)
		VALUES ($1, 0, now(), 'm_before', now()), ($1, 1, now(), NULL, NULL)`, message)
	f.h.Exec(t, `UPDATE communications.outbox_jobs SET status = 'processing', lease_id = 'dead', lease_until = $2, attempts = 1 WHERE message_id = $1`,
		message, f.h.Now().Add(-time.Minute))

	meter := newMeterHarness(t)
	f.drainOutbox(t, communications.NewOutboxWorkerWithMeterForTest(f.h.Deps(), meter.provider))
	sent := f.fake.Sent()
	if len(sent) != 1 || sent[0].Part.Attachment == nil {
		t.Fatalf("parts sent = %+v, want the file alone: the text was recorded as sent", sent)
	}
	if got := meter.count(t, possibleDuplicateSendsMetric); got != 1 {
		t.Errorf("possible duplicates = %d, want the file part's one", got)
	}
	if job := readOutboxJob(t, f.h, message); job.status != "completed" {
		t.Errorf("job = %q, want completed", job.status)
	}
}

// TestOutboxMessenger_TheLeaseIsRenewedBetweenPartsAndALostOneStopsTheJob: a
// renewal before each part after the first moves the lease on, and one that finds
// the lease taken stops the job without sending the rest or writing a failure.
func TestOutboxMessenger_TheLeaseIsRenewedBetweenPartsAndALostOneStopsTheJob(t *testing.T) {
	t.Parallel()
	f := newChat(t)
	conv := f.chat(t)
	first, second := f.stage(t, conv, "one.png", []byte("ONE")), f.stage(t, conv, "two.png", []byte("TWO"))
	message := f.mustReply(t, conv, map[string]any{"textBody": "Three parts", "attachmentIds": []string{first, second}})

	// Renewed: the lease at the first part's Send is what the claim took, and at
	// the second's it has moved on.
	var mu sync.Mutex
	var leases []time.Time
	f.fake.OnSend(func(context.Context, string, contracts.MessengerPart) error {
		var until time.Time
		_ = f.h.Pool().QueryRow(context.Background(), `SELECT lease_until FROM communications.outbox_jobs WHERE message_id = $1`, message).Scan(&until)
		mu.Lock()
		leases = append(leases, until)
		first := len(leases) == 1
		mu.Unlock()
		if first {
			f.h.Advance(30 * time.Second) // time passes while the first part is sent
		}
		return nil
	})
	f.drainOutbox(t, f.outbox())
	if len(leases) != 3 {
		t.Fatalf("Send calls = %d, want 3", len(leases))
	}
	if !leases[1].After(leases[0]) || leases[2].Before(leases[1]) {
		t.Errorf("leases = %v, want the lease moved on before the second part", leases)
	}

	// Lost: a second message whose lease is taken during the first part's Send.
	message = f.mustReply(t, conv, map[string]any{"textBody": "Again three", "attachmentIds": []string{f.stage(t, conv, "three.png", []byte("3")), f.stage(t, conv, "four.png", []byte("4"))}})
	sendsBefore := f.fake.CallCount("Send")
	f.fake.OnSend(func(context.Context, string, contracts.MessengerPart) error {
		f.h.Exec(t, `UPDATE communications.outbox_jobs SET lease_id = 'another0another0another0another0' WHERE message_id = $1`, message)
		return nil
	})
	processed, err := f.outbox().ProcessOne(context.Background())
	if err != nil || !processed {
		t.Fatalf("ProcessOne = %v, %v", processed, err)
	}
	if got := f.fake.CallCount("Send") - sendsBefore; got != 1 {
		t.Errorf("Send calls = %d, want only the first part: the renewal found the lease taken", got)
	}
	job := readOutboxJob(t, f.h, message)
	if job.status != "processing" || job.leaseID == nil || *job.leaseID != "another0another0another0another0" || job.lastError != nil {
		t.Errorf("job = %+v, want the other holder's row untouched, no failure written", job)
	}
	if d := readDeliveries(t, f.h, message); d[0].status == "submission_failed" || d[0].status == "retrying" {
		t.Errorf("delivery = %+v, want no failure recorded by a holder that lost its lease", d[0])
	}
}

// TestOutboxMessenger_TheWindowIsCheckedAgainAtSendTime: a job whose window
// closed while it waited fails terminally with the reason and sends nothing; one
// past the first 24 hours with the Human Agent permission is sent tagged.
func TestOutboxMessenger_TheWindowIsCheckedAgainAtSendTime(t *testing.T) {
	t.Parallel()
	f := newChat(t)
	conv := f.chat(t)
	message := f.mustReply(t, conv, map[string]any{"textBody": "Too slow"})
	f.h.Advance(25 * time.Hour)

	f.drainOutbox(t, f.outbox())
	if got := f.fake.CallCount("Send"); got != 0 {
		t.Errorf("Send calls = %d, want none: the window closed", got)
	}
	job := readOutboxJob(t, f.h, message)
	if job.status != "failed" || job.attempts != 1 {
		t.Errorf("job = %+v, want failed at once: it does not retry", job)
	}
	if got := f.failureCode(t, message); got != "reply_window_closed" {
		t.Errorf("failure code = %q, want reply_window_closed", got)
	}
	if d := readDeliveries(t, f.h, message); d[0].status != "submission_failed" {
		t.Errorf("delivery = %+v, want submission_failed", d[0])
	}

	// With the permission, after the first 24 hours: tagged.
	f.fake.SetCapabilities(contracts.MessengerCapabilities{
		MaxText: 2000, MaxAnswerOptions: 13, MaxAnswerTitle: 20, ShareEmail: true,
		Window: 24 * time.Hour, HumanAgentWindow: 7 * 24 * time.Hour,
	})
	f.resign(t)
	tagged := f.mustReply(t, conv, map[string]any{"textBody": "Late but allowed"})
	f.drainOutbox(t, f.outbox())
	sent := f.fake.Sent()
	if len(sent) != 1 || !sent[0].Part.HumanAgent {
		t.Errorf("parts sent = %+v, want one, tagged for the Human Agent permission", sent)
	}
	if job := readOutboxJob(t, f.h, tagged); job.status != "completed" {
		t.Errorf("job = %q, want completed", job.status)
	}
}

// TestOutboxMessenger_ProviderErrorsMapOntoTheOutcomes: the provider's named
// errors are terminal with their code, an unavailable provider holds the job
// without spending an attempt, and a temporary or an unnamed failure is retried
// on the backoff.
func TestOutboxMessenger_ProviderErrorsMapOntoTheOutcomes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		err       error
		status    string
		attempts  int32
		code      string
		heldEvent bool
	}{
		{"the person cannot be reached", contracts.ErrRecipientUnreachable, "failed", 1, "recipient_unreachable", false},
		{"the platform closed the window", contracts.ErrReplyWindowClosed, "failed", 1, "reply_window_closed", false},
		{"the Human Agent permission is missing", contracts.ErrHumanAgentNotPermitted, "failed", 1, "human_agent_not_permitted", false},
		{"the platform rejected the part", contracts.ErrMessengerRejected, "failed", 1, "rejected", false},
		{"the provider is unavailable", contracts.ErrMessengerUnavailable, "retry", 0, "provider_unavailable", true},
		{"a temporary failure", contracts.MessengerTemporary(errors.New("5xx")), "retry", 1, "", false},
		{"a failure the contract does not name", errors.New("network down"), "retry", 1, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newChat(t)
			conv := f.chat(t)
			message := f.mustReply(t, conv, map[string]any{"textBody": "Hello"})
			f.fake.FailSend(1, tc.err)

			f.drainOutbox(t, f.outbox())
			job := readOutboxJob(t, f.h, message)
			if job.status != tc.status || job.attempts != tc.attempts {
				t.Errorf("job = %+v, want %s after %d attempts", job, tc.status, tc.attempts)
			}
			if got := f.failureCode(t, message); got != tc.code {
				t.Errorf("failure code = %q, want %q", got, tc.code)
			}
			if job.lastError == nil || *job.lastError != "Outbound delivery failed." {
				t.Errorf("last_error = %v, want the constant: the platform's words are never stored", job.lastError)
			}
		})
	}
}

// TestOutboxMessenger_ConcurrentWorkersSendEachPartOnce: two outbox workers, one
// job, gated by pg_stat_activity as the e-mail claim's race is: exactly one claims
// it, so each part is sent once.
func TestOutboxMessenger_ConcurrentWorkersSendEachPartOnce(t *testing.T) {
	t.Parallel()
	f := newChat(t)
	conv := f.chat(t)
	file := f.stage(t, conv, "one.png", []byte("ONE"))
	message := f.mustReply(t, conv, map[string]any{"textBody": "Two parts", "attachmentIds": []string{file}})

	ctx := context.Background()
	gate, err := f.h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("gate: begin: %v", err)
	}
	t.Cleanup(func() { _ = gate.Rollback(ctx) })
	if _, err := gate.Exec(ctx, `LOCK TABLE communications.outbox_jobs IN EXCLUSIVE MODE`); err != nil {
		t.Fatalf("gate: lock outbox_jobs: %v", err)
	}
	const n = 2
	processed := make([]bool, n)
	errs := make([]error, n)
	var ready, done sync.WaitGroup
	begin := make(chan struct{})
	for i := 0; i < n; i++ {
		ready.Add(1)
		done.Add(1)
		go func() {
			defer done.Done()
			w := f.outbox()
			ready.Done()
			<-begin
			processed[i], errs[i] = w.ProcessOne(ctx)
		}()
	}
	ready.Wait()
	close(begin)
	finished := make(chan struct{})
	go func() {
		done.Wait()
		close(finished)
	}()
	awaitLockWaiters(t, f.h, n, finished)
	if err := gate.Commit(ctx); err != nil {
		t.Fatalf("gate: release: %v", err)
	}
	<-finished

	claims := 0
	for i, err := range errs {
		if err != nil {
			t.Fatalf("worker %d: ProcessOne: %v", i, err)
		}
		if processed[i] {
			claims++
		}
	}
	if claims != 1 {
		t.Errorf("claims = %d, want exactly 1", claims)
	}
	if got := f.fake.CallCount("Send"); got != 2 {
		t.Errorf("Send calls = %d, want 2: each of the two parts once", got)
	}
	if job := readOutboxJob(t, f.h, message); job.status != "completed" || job.attempts != 1 {
		t.Errorf("job = %+v, want completed on the one attempt", job)
	}
}

// partTimes sets the platform's time for each of a message's parts, in index
// order, and the time its Send began to the same.
func (f chatFixture) partTimes(t *testing.T, message uuid.UUID) []time.Time {
	t.Helper()
	rows, err := f.h.Pool().Query(context.Background(), `
		SELECT platform_at FROM communications.messenger_parts WHERE message_id = $1 ORDER BY part_index`, message)
	if err != nil {
		t.Fatalf("read part times: %v", err)
	}
	defer rows.Close()
	var out []time.Time
	for rows.Next() {
		var at *time.Time
		if err := rows.Scan(&at); err != nil || at == nil {
			t.Fatalf("a part has no platform time: %v", err)
		}
		out = append(out, *at)
	}
	return out
}

func (f chatFixture) pushWatermark(kind string, at time.Time) {
	f.fake.Push(f.channel.Address, contracts.MessengerEvent{Kind: kind, Participant: "4242", At: at, Watermark: at})
}

// TestOutboxMessenger_AWatermarkAtTheMomentOfTheSendCoversTheMessage: the part's
// platform time is what a watermark is compared with — not the time the outbox
// finished — so one equal to the moment of the send marks the newest message, and
// one a moment before it does not.
func TestOutboxMessenger_AWatermarkAtTheMomentOfTheSendCoversTheMessage(t *testing.T) {
	t.Parallel()
	f := newChat(t)
	conv := f.chat(t)
	message := f.mustReply(t, conv, map[string]any{"textBody": "Hello"})
	f.h.Advance(10 * time.Second)
	f.drainOutbox(t, f.outbox())
	f.h.Advance(30 * time.Second) // the completion is later than the platform's stamp
	moment := f.partTimes(t, message)[0]
	_, delivery := readDeliveryID(t, f, message)

	f.pushWatermark(contracts.MessengerEventDelivered, moment.Add(-time.Millisecond))
	f.sync(t)
	if got := f.deliveryStatus(t, delivery); got != "relay_accepted" {
		t.Fatalf("a watermark before the send moved the delivery to %s", got)
	}
	f.h.Advance(2 * time.Minute)
	f.pushWatermark(contracts.MessengerEventDelivered, moment)
	f.sync(t)
	if got := f.deliveryStatus(t, delivery); got != "delivered" {
		t.Errorf("a watermark at the moment of the send: %s, want delivered", got)
	}
	f.h.Advance(2 * time.Minute)
	f.pushWatermark(contracts.MessengerEventRead, moment)
	f.sync(t)
	if got := f.deliveryStatus(t, delivery); got != "read" {
		t.Errorf("a read watermark at the moment of the send: %s, want read", got)
	}
}

func readDeliveryID(t *testing.T, f chatFixture, message uuid.UUID) (uuid.UUID, uuid.UUID) {
	t.Helper()
	d := readDeliveries(t, f.h, message)
	return message, d[0].id
}

// TestOutboxMessenger_AWatermarkBetweenPartsIsNotLost: a watermark that arrives
// while a message is still being sent finds its deliveries 'sending' and moves
// nothing — and is applied when the send completes, to the message it covers and
// not to a part sent after it.
func TestOutboxMessenger_AWatermarkBetweenPartsIsNotLost(t *testing.T) {
	t.Parallel()
	f := newChat(t)
	conv := f.chat(t)
	file := f.stage(t, conv, "one.png", []byte("ONE"))
	covered := f.mustReply(t, conv, map[string]any{"textBody": "One part"})
	f.drainOutbox(t, f.outbox())
	_, firstDelivery := readDeliveryID(t, f, covered)

	// A second message of two parts; the watermark arrives after its first part.
	f.h.Advance(time.Minute)
	second := f.mustReply(t, conv, map[string]any{"textBody": "Two parts", "attachmentIds": []string{file}})
	_, secondDelivery := readDeliveryID(t, f, second)
	var between time.Time
	f.fake.OnSend(func(context.Context, string, contracts.MessengerPart) error {
		if f.fake.CallCount("Send") == 2 { // the second message's second part
			between = f.h.Now()
			f.fake.Push(f.channel.Address, contracts.MessengerEvent{
				Kind: contracts.MessengerEventRead, Participant: "4242", At: between, Watermark: between,
			})
			f.sync(t) // the sync sees the watermark while the message is being sent
		}
		return nil
	})
	f.drainOutbox(t, f.outbox())

	if got := f.deliveryStatus(t, firstDelivery); got != "read" {
		t.Errorf("the first message = %s, want read: the watermark covers it", got)
	}
	// The second message's last part was sent at the moment the watermark was
	// stamped, which arrived before the send was recorded: covered, and applied
	// at the completion.
	if got := f.deliveryStatus(t, secondDelivery); got != "read" {
		t.Errorf("the second message = %s, want read: applied when its send completed", got)
	}
}

// TestOutboxMessenger_AHeldSendClearsThePartsAttempt: a part the provider held —
// it is unavailable — was never handed over, so its marker is cleared and the next
// run counts no possible duplicate.
func TestOutboxMessenger_AHeldSendClearsThePartsAttempt(t *testing.T) {
	t.Parallel()
	f := newChat(t)
	conv := f.chat(t)
	message := f.mustReply(t, conv, map[string]any{"textBody": "Hello"})
	f.fake.FailSend(1, contracts.ErrMessengerUnavailable)
	meter := newMeterHarness(t)
	w := communications.NewOutboxWorkerWithMeterForTest(f.h.Deps(), meter.provider)
	f.drainOutbox(t, w)
	if parts := f.parts(t, message); len(parts) != 1 || parts[0].attempted || parts[0].sent {
		t.Fatalf("parts after a held send = %+v, want the attempt cleared", parts)
	}
	f.h.Advance(time.Hour)
	f.drainOutbox(t, w)
	if got := meter.count(t, possibleDuplicateSendsMetric); got != 0 {
		t.Errorf("possible duplicates = %d, want none: nothing was handed over", got)
	}
	if job := readOutboxJob(t, f.h, message); job.status != "completed" {
		t.Errorf("job = %q, want completed", job.status)
	}
}
