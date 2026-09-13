package communications

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/communications/store"
	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/mail"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/storage"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

// This file is the outbox delivery worker: SV/OutboxJobProcessor.cs (the
// processor, `:11-282`) plus CommunicationsOutboxWorker (the BackgroundService,
// `:284-304`), ported per communications inventory §13 and design doc §4.
//
// The three things about it that most look like bugs and are all deliberate
// .NET behaviour, each pinned by a test in outbox_test.go:
//
//  1. delivery_attempted_at is cleared on every claim, stamped by a bare
//     auto-committing UPDATE *before* the send, never checked for
//     rows-affected, and never cleared on completion (inventory §11 D5,
//     §13.3). It is a crash marker, not a retry marker.
//  2. The completion is a silent no-op when the lease changed underneath it
//     (step 10, `:1315`): the mail has gone out, nothing is committed, and the
//     next holder sends it again. This is the module's principal
//     duplicate-delivery source and is at-least-once delivery working as
//     designed, not a race to close.
//  3. There is no advisory lock anywhere in this worker. The advisory lease
//     guards retention only (design D8, inventory §14); here the conditional
//     UPDATE in queries/outbox.sql *is* the lock.

const (
	// outboxWorkerName is what the runner logs this worker as.
	outboxWorkerName = "communications-outbox"

	// outboxPollInterval is `max(1, Outbox:PollSeconds)`, default 5 s
	// (CFG/CommunicationsOptions.cs:55, inventory §13.1).
	//
	// This and the three constants below are .NET's configured values, carried
	// as constants rather than as new environment variables: design doc §7
	// enumerates this module's configuration and none of the Outbox:* keys is
	// on it. A deployment that needs to tune them gets a config key then,
	// deliberately, rather than four un-exercised knobs now.
	outboxPollInterval = 5 * time.Second
	// outboxLeaseDuration is `Outbox:LeaseSeconds`, default 60 s. It bounds how
	// long a claimed job stays invisible to other workers; nothing renews it
	// (there is no heartbeat), so a send that outlives it is redone by whoever
	// claims next — see outboxSendTimeout.
	outboxLeaseDuration = 60 * time.Second
	// outboxClaimAttempts is `max(3, Outbox:ClaimAttempts)`, default 10
	// (inventory §13.2 step 3): how many times one ProcessOne call will lose a
	// claim race and try the next candidate before giving up for this poll.
	outboxClaimAttempts = 10
	// outboxMaxAttempts is `max(1, Outbox:MaxAttempts)`, default 8. Terminality
	// is `attempts >= outboxMaxAttempts` on the POST-increment value, so this
	// is 8 total send attempts (inventory §13.4).
	outboxMaxAttempts = 8
	// outboxSendTimeout is .NET's `max(1, leaseSeconds / 2)` fallback for
	// Smtp:TimeoutSeconds (inventory §15.2 step 2), which enforces the hard
	// invariant `0 < timeout < lease`. It is what stops a send outliving the
	// lease protecting it — the only thing that does (inventory §13.6).
	outboxSendTimeout = outboxLeaseDuration / 2

	// outboxFailureMessage is the CONSTANT written to outbox_jobs.last_error
	// and message_deliveries.last_error on every failure (`:1337`). The real
	// exception text is logged and never stored: this is the module's only
	// outbound error redaction, so it is not "improved" into the real message.
	outboxFailureMessage = "Outbound delivery failed."
	// outboxFailureEventData is the data_json of the retrying /
	// submission_failed events (`:275`), carrying the same constant.
	outboxFailureEventData = `{"error":"Outbound delivery failed."}`
)

// errChannelGone and errAttachmentsUnavailable are the two
// InvalidOperationExceptions the send phase throws at steps 2 and 4
// (`:136`, `:145-146`). Both reach the ordinary failure path, where their text
// is logged and discarded in favour of outboxFailureMessage.
var (
	errChannelGone            = errors.New("communications: the message channel no longer exists")
	errAttachmentsUnavailable = errors.New("communications: outbound attachments are not available")
)

// OutboxWorker drains communications.outbox_jobs: claim a job by conditional
// update, send its message over the channel's own SMTP settings, and record the
// outcome. It implements worker.Worker, so module.Workers hands it to
// cmd/vantigo's runner in worker mode and in api mode when
// WORKERS_IN_PROCESS=1 (design §2).
type OutboxWorker struct {
	deps module.Deps
	// store is this module's scoped object store, used only to read attachment
	// bytes for an outbound message.
	store storage.ObjectStore
	// storeErr is deferred rather than returned by the constructor: a worker
	// whose object store cannot be built must still be startable, failing only
	// the jobs that actually carry attachments, exactly as an unconfigured
	// store fails closed per operation rather than at boot (docs/storage.md).
	storeErr error
	// duplicates counts the possible-duplicate sends this worker detected —
	// .NET's communications.outbox.possible_duplicate_sends counter
	// (inventory §18). There is no metrics facility in this port yet (task
	// 14's scope), so the counter lives here, is logged at warn level where
	// .NET logs it, and is readable for tests and for whatever task 14 wires
	// it into.
	duplicates atomic.Int64
}

var _ worker.Worker = (*OutboxWorker)(nil)

// NewOutboxWorker builds the worker over d. It never fails: an object store
// that cannot be constructed is remembered and reported when a job actually
// needs one (see OutboxWorker.storeErr).
func NewOutboxWorker(d module.Deps) *OutboxWorker {
	store, err := moduleObjectStore(d)
	return &OutboxWorker{deps: d, store: store, storeErr: err}
}

// Name identifies this worker in the runner's logs.
func (w *OutboxWorker) Name() string { return outboxWorkerName }

// Interval is the poll cadence between drains.
func (w *OutboxWorker) Interval() time.Duration { return outboxPollInterval }

// PossibleDuplicateSends is how many times this worker has claimed a job that
// was already marked as attempted — a crash between a previous worker's send
// and its completion commit, and therefore a probable duplicate mail.
func (w *OutboxWorker) PossibleDuplicateSends() int64 { return w.duplicates.Load() }

// Run is the worker loop (`:288-303`): drain the queue, sleep the poll
// interval, repeat until ctx is done. A failing iteration is logged and the
// loop continues — the loop never dies, which is the property the runner
// depends on since it never restarts a worker.
func (w *OutboxWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(outboxPollInterval)
	defer ticker.Stop()
	for {
		w.drain(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// drain keeps processing while a job was touched, which is exactly .NET's
// `while (await processor.ProcessOneAsync()) { }`: the loop ends on an empty
// queue, not on a success, so a failed job does not stall the drain.
func (w *OutboxWorker) drain(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		processed, err := w.ProcessOne(ctx)
		if err != nil {
			if ctx.Err() == nil {
				w.logger().Error("outbox iteration failed", "worker", outboxWorkerName, "error", err)
			}
			return
		}
		if !processed {
			return
		}
	}
}

// ProcessOne claims at most one job and carries it to an outcome, returning
// whether a job was touched. Like .NET's ProcessOneAsync it returns true for
// *any* outcome including a failure — only an empty queue returns false — so a
// caller can use it to drain.
func (w *OutboxWorker) ProcessOne(ctx context.Context) (bool, error) {
	job, leaseID, err := w.claim(ctx)
	if err != nil {
		return false, err
	}
	if job == nil {
		return false, nil
	}

	if err := w.deliver(ctx, *job, leaseID); err != nil {
		// .NET catches only when the token is not cancelled (`:199-204`); a
		// cancellation propagates rather than burning an attempt.
		if ctx.Err() != nil {
			return true, err
		}
		w.logger().Warn("outbound delivery failed",
			"worker", outboxWorkerName, "job", job.ID, "message", job.MessageID, "error", err)
		if markErr := w.markFailed(ctx, *job, leaseID, err); markErr != nil {
			return true, markErr
		}
	}
	return true, nil
}

// claim is ProcessOneForTenantAsync's claim phase (`:61-126`), all of it inside
// one transaction: pick a candidate, count it as a possible duplicate when it
// carries a stale attempt marker, take it with a conditional UPDATE, and move
// its sendable deliveries to "sending". A lost race (0 rows affected) simply
// tries the next candidate.
//
// now and leaseUntil are computed ONCE before the loop and reused for every
// attempt, exactly as .NET freezes them (`:68-69`): a slow claim loop must not
// silently extend the lease it is about to take.
func (w *OutboxWorker) claim(ctx context.Context) (*store.CommunicationsOutboxJob, string, error) {
	// Guid.NewGuid().ToString("N"): 32 lowercase hex characters, no dashes.
	leaseID := hexN(uuid.New())
	now := w.now()
	leaseUntil := now.Add(outboxLeaseDuration)

	var claimed *store.CommunicationsOutboxJob
	err := db.WithTx(ctx, w.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := store.New(tx)
		for attempt := 0; attempt < outboxClaimAttempts; attempt++ {
			candidate, err := q.SelectClaimableOutboxJob(ctx, now)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			if err != nil {
				return fmt.Errorf("communications: select claimable outbox job: %w", err)
			}

			// The possible-duplicate signal (`:88-96`), checked on the
			// CANDIDATE — so a lost claim race still counts, and it can only
			// ever fire on the lease-expired 'processing' branch.
			if candidate.Status == "processing" && candidate.DeliveryAttemptedAt != nil {
				w.duplicates.Add(1)
				w.logger().Warn("possible duplicate outbound send",
					"worker", outboxWorkerName, "job", candidate.ID, "message", candidate.MessageID,
					"deliveryAttemptedAt", *candidate.DeliveryAttemptedAt)
			}

			rows, err := q.ClaimOutboxJob(ctx, store.ClaimOutboxJobParams{
				ID: candidate.ID, LeaseID: &leaseID, LeaseUntil: &leaseUntil, Now: now,
			})
			if err != nil {
				return fmt.Errorf("communications: claim outbox job: %w", err)
			}
			if rows == 0 {
				// Someone else won it between the select and the update.
				continue
			}

			job, err := q.GetOutboxJob(ctx, candidate.ID)
			if err != nil {
				return fmt.Errorf("communications: re-read claimed outbox job: %w", err)
			}
			deliveries, err := q.ListMessageDeliveriesForSend(ctx, job.MessageID)
			if err != nil {
				return fmt.Errorf("communications: list deliveries for claim: %w", err)
			}
			for _, d := range deliveries {
				if !outboxDeliveryIsSendable(d.Status) {
					continue
				}
				if err := q.MarkMessageDeliverySending(ctx, d.ID); err != nil {
					return fmt.Errorf("communications: mark delivery sending: %w", err)
				}
			}
			claimed = &job
			return nil
		}
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	return claimed, leaseID, nil
}

// deliver is the send phase (`:128-204`) in .NET's exact order. Every step that
// can refuse the message returns an error, which ProcessOne turns into the
// failure path.
func (w *OutboxWorker) deliver(ctx context.Context, job store.CommunicationsOutboxJob, leaseID string) error {
	q := store.New(w.deps.Pool)

	// Steps 1-2: the message with its conversation, channel and credential.
	//
	// The no-rows branch below is UNREACHABLE, here and in .NET, and is
	// deliberately left without a test rather than left looking like missing
	// coverage: GetOutboundMessageForSend inner-joins the conversation and the
	// channel, conversations.channel_id and participants.channel_id are both
	// ON DELETE RESTRICT (so a channel with any history cannot be deleted —
	// channels are deactivated with is_active = false instead, inventory §10
	// item 9), and outbox_jobs.message_id is ON DELETE CASCADE (so a deleted
	// message takes its job with it). A job therefore always has a message,
	// a conversation and a channel, and no fixture can construct the state
	// this branch answers. It is implemented anyway because .NET implements
	// it (`:136`) and because a future schema change that relaxes either FK
	// should find a handled path here, not a nil dereference.
	message, err := q.GetOutboundMessageForSend(ctx, job.MessageID)
	if errors.Is(err, pgx.ErrNoRows) {
		return errChannelGone
	}
	if err != nil {
		return fmt.Errorf("communications: load outbound message: %w", err)
	}

	deliveries, err := q.ListMessageDeliveriesForSend(ctx, job.MessageID)
	if err != nil {
		return fmt.Errorf("communications: list deliveries for send: %w", err)
	}
	sendable := make([]store.ListMessageDeliveriesForSendRow, 0, len(deliveries))
	for _, d := range deliveries {
		if outboxDeliveryIsSendable(d.Status) {
			sendable = append(sendable, d)
		}
	}

	// Step 3: nothing to send is a COMPLETION, not a failure — the job is done
	// even though no mail leaves (`:138-143`, `:207-218`).
	if len(sendable) == 0 {
		return w.finishJob(ctx, w.deps.Pool, job.ID, "completed")
	}

	// Step 4: the fourth load-bearing "clean" site (inventory §5.5). One
	// non-clean attachment refuses the whole message.
	attachments, err := q.ListMessageAttachmentsForSend(ctx, job.MessageID)
	if err != nil {
		return fmt.Errorf("communications: list attachments for send: %w", err)
	}
	for _, a := range attachments {
		if a.ScanStatus != "clean" {
			return errAttachmentsUnavailable
		}
	}

	// Steps 5-6: the worker's own suppression re-check, for email channels
	// only. Post-port this is the ONLY place suppression is enforced on an
	// outbound message, because reply's own check is unreachable (design §1.1).
	if strings.EqualFold(message.ChannelType, "email") {
		suppressed, err := w.anySuppressed(ctx, q, sendable)
		if err != nil {
			return err
		}
		if suppressed {
			return w.cancelSuppressed(ctx, job, sendable)
		}
	}

	// Step 7: the marker, committed on its own before the send. Its
	// rows-affected is deliberately NOT checked — on an already-stolen lease
	// this write no-ops and the send still happens (inventory `:1320-1323`).
	stamp := w.now()
	if _, err := q.StampOutboxDeliveryAttempted(ctx, store.StampOutboxDeliveryAttemptedParams{
		DeliveryAttemptedAt: &stamp, ID: job.ID, LeaseID: &leaseID,
	}); err != nil {
		return fmt.Errorf("communications: stamp delivery attempt: %w", err)
	}

	// Step 8: the external send.
	envelope, err := w.envelope(ctx, message, sendable, attachments)
	if err != nil {
		return err
	}
	smtp, err := w.smtpConfig(message)
	if err != nil {
		return err
	}
	sendCtx, cancel := context.WithTimeout(ctx, outboxSendTimeout)
	defer cancel()
	if err := w.sender()(sendCtx, smtp, false, envelope); err != nil {
		return fmt.Errorf("communications: send outbound message: %w", err)
	}

	// Steps 9-13.
	return w.complete(ctx, job, leaseID, sendable)
}

// complete is steps 9-13. Step 10's lease check is the whole reason this is a
// re-read rather than a blind update: if the lease changed while the send was
// in flight, NOTHING is written — the mail is already out, the job stays
// processing, and the new holder will send it again.
func (w *OutboxWorker) complete(ctx context.Context, job store.CommunicationsOutboxJob, leaseID string,
	sendable []store.ListMessageDeliveriesForSendRow) error {
	now := w.now()
	return db.WithTx(ctx, w.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := store.New(tx)
		current, err := q.GetOutboxJob(ctx, job.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("communications: re-read job before completion: %w", err)
		}
		if current.LeaseID == nil || *current.LeaseID != leaseID {
			w.logger().Warn("outbox completion skipped: the lease was taken over mid-send",
				"worker", outboxWorkerName, "job", job.ID, "message", job.MessageID)
			return nil
		}
		if err := q.FinishOutboxJob(ctx, store.FinishOutboxJobParams{
			Status: "completed", CompletedAt: &now, ID: job.ID,
		}); err != nil {
			return fmt.Errorf("communications: complete outbox job: %w", err)
		}
		for _, d := range sendable {
			deliveryID := d.ID
			if err := q.MarkMessageDeliveryAccepted(ctx, store.MarkMessageDeliveryAcceptedParams{
				AcceptedAt: &now, ID: deliveryID,
			}); err != nil {
				return fmt.Errorf("communications: accept delivery: %w", err)
			}
			if err := q.InsertMessageEventWithData(ctx, store.InsertMessageEventWithDataParams{
				ID: uuid.New(), MessageID: job.MessageID, DeliveryID: &deliveryID,
				EventType: "relay_accepted", OccurredAt: now,
			}); err != nil {
				return fmt.Errorf("communications: write relay_accepted event: %w", err)
			}
		}
		return nil
	})
}

// cancelSuppressed is step 6 (`:220-247`): one suppressed recipient cancels the
// whole job, all-or-nothing, and every sendable delivery becomes 'suppressed'
// with its last_error explicitly cleared.
func (w *OutboxWorker) cancelSuppressed(ctx context.Context, job store.CommunicationsOutboxJob,
	sendable []store.ListMessageDeliveriesForSendRow) error {
	now := w.now()
	return db.WithTx(ctx, w.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := store.New(tx)
		if err := q.FinishOutboxJob(ctx, store.FinishOutboxJobParams{
			Status: "cancelled", CompletedAt: &now, ID: job.ID,
		}); err != nil {
			return fmt.Errorf("communications: cancel outbox job: %w", err)
		}
		for _, d := range sendable {
			deliveryID := d.ID
			if err := q.MarkMessageDeliverySuppressed(ctx, deliveryID); err != nil {
				return fmt.Errorf("communications: suppress delivery: %w", err)
			}
			if err := q.InsertMessageEventWithData(ctx, store.InsertMessageEventWithDataParams{
				ID: uuid.New(), MessageID: job.MessageID, DeliveryID: &deliveryID,
				EventType: "suppressed", OccurredAt: now,
			}); err != nil {
				return fmt.Errorf("communications: write suppressed event: %w", err)
			}
		}
		return nil
	})
}

// markFailed is MarkFailedAsync (`:249-281`). It re-reads the job and does
// nothing at all if the row is gone or the lease was taken over — a worker that
// lost its lease does not get to record a failure either.
func (w *OutboxWorker) markFailed(ctx context.Context, job store.CommunicationsOutboxJob, leaseID string, cause error) error {
	now := w.now()
	return db.WithTx(ctx, w.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := store.New(tx)
		current, err := q.GetOutboxJob(ctx, job.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("communications: re-read job before failing it: %w", err)
		}
		if current.LeaseID == nil || *current.LeaseID != leaseID {
			return nil
		}

		// Terminality on the POST-increment attempts value: max_attempts = 8
		// means 8 total attempts, and the backoff for attempts = 7 (128 s) is
		// therefore the largest one a default deployment ever waits.
		terminal := current.Attempts >= outboxMaxAttempts
		jobStatus, deliveryStatus := "retry", "retrying"
		if terminal {
			jobStatus, deliveryStatus = "failed", "submission_failed"
		}
		failure := outboxFailureMessage
		data := outboxFailureEventData

		if err := q.FailOutboxJob(ctx, store.FailOutboxJobParams{
			Status:        jobStatus,
			LastError:     &failure,
			NextAttemptAt: now.Add(retryBackoff(current.Attempts)),
			ID:            current.ID,
		}); err != nil {
			return fmt.Errorf("communications: fail outbox job: %w", err)
		}

		deliveries, err := q.ListMessageDeliveriesForSend(ctx, current.MessageID)
		if err != nil {
			return fmt.Errorf("communications: list deliveries to fail: %w", err)
		}
		for _, d := range deliveries {
			if !outboxDeliveryIsSendable(d.Status) {
				continue
			}
			deliveryID := d.ID
			if err := q.MarkMessageDeliveryFailed(ctx, store.MarkMessageDeliveryFailedParams{
				Status: deliveryStatus, LastError: &failure, ID: deliveryID,
			}); err != nil {
				return fmt.Errorf("communications: fail delivery: %w", err)
			}
			if err := q.InsertMessageEventWithData(ctx, store.InsertMessageEventWithDataParams{
				ID: uuid.New(), MessageID: current.MessageID, DeliveryID: &deliveryID,
				EventType: deliveryStatus, OccurredAt: now, DataJson: &data,
			}); err != nil {
				return fmt.Errorf("communications: write failure event: %w", err)
			}
		}
		return nil
	})
}

// finishJob writes one terminal job status with its completion stamp and the
// lease cleared. Step 3 uses it outside any transaction, exactly as
// CompleteWithoutSendingAsync does (`:207-218`).
func (w *OutboxWorker) finishJob(ctx context.Context, dbtx store.DBTX, jobID uuid.UUID, status string) error {
	now := w.now()
	if err := store.New(dbtx).FinishOutboxJob(ctx, store.FinishOutboxJobParams{
		Status: status, CompletedAt: &now, ID: jobID,
	}); err != nil {
		return fmt.Errorf("communications: finish outbox job as %s: %w", status, err)
	}
	return nil
}

// anySuppressed is step 5: normalise every sendable recipient the way
// suppressions are stored (Trim().ToUpperInvariant(), spec D7) and ask whether
// any of them is suppressed.
func (w *OutboxWorker) anySuppressed(ctx context.Context, q *store.Queries,
	sendable []store.ListMessageDeliveriesForSendRow) (bool, error) {
	seen := make(map[string]bool, len(sendable))
	addresses := make([]string, 0, len(sendable))
	for _, d := range sendable {
		normalized := normalizeEmail(d.RecipientAddress)
		if normalized == "" || seen[normalized] {
			continue
		}
		seen[normalized] = true
		addresses = append(addresses, normalized)
	}
	if len(addresses) == 0 {
		return false, nil
	}
	count, err := q.CountSuppressedRecipients(ctx, addresses)
	if err != nil {
		return false, fmt.Errorf("communications: check suppressions: %w", err)
	}
	return count > 0, nil
}

// outboxChannelMetadata is the threading metadata EmailEnvelopeFactory reads
// off conversation_messages.channel_metadata_json (inventory §15.1, §4.4).
//
// Nothing in this port ever writes that column — its only .NET writer was the
// inbound processor, which is out of scope (inventory §4.5) — so both fields
// are permanently absent here. The parse is kept rather than dropped for the
// same reason the column and the reply-all path are kept: an inbound provider
// arriving later should not need this code written from scratch.
type outboxChannelMetadata struct {
	InReplyTo  string   `json:"inReplyTo"`
	References []string `json:"references"`
}

// envelope is EmailEnvelopeFactory.Create (`:300-313`): the From from the
// channel, the subject falling back from the message to the conversation, the
// recipients partitioned on recipient_type, the deterministic Message-Id, and
// the clean attachments' bytes read out of the object store.
func (w *OutboxWorker) envelope(ctx context.Context, message store.GetOutboundMessageForSendRow,
	sendable []store.ListMessageDeliveriesForSendRow, attachments []store.ListMessageAttachmentsForSendRow) (mail.Outbound, error) {
	out := mail.Outbound{
		// EmailMessageId.For(message.Id): 32 lowercase hex, no dashes, at the
		// hardcoded literal vantigo.invalid (inventory §15.4, D3). Bare here —
		// internal/mail adds the angle brackets.
		MessageID: hexN(message.ID) + "@vantigo.invalid",
	}
	if message.ChannelDisplayName != nil {
		out.DisplayName = *message.ChannelDisplayName
	}
	switch {
	case message.Subject != nil:
		out.Subject = *message.Subject
	case message.ConversationSubject != nil:
		out.Subject = *message.ConversationSubject
	}
	if message.TextBody != nil {
		out.TextBody = *message.TextBody
	}
	if message.HtmlBody != nil {
		out.HTMLBody = *message.HtmlBody
	}
	for _, d := range sendable {
		switch strings.ToLower(d.RecipientType) {
		case "to":
			out.To = append(out.To, d.RecipientAddress)
		case "cc":
			out.Cc = append(out.Cc, d.RecipientAddress)
		case "bcc":
			out.Bcc = append(out.Bcc, d.RecipientAddress)
		}
	}
	if len(message.ChannelMetadataJson) > 0 {
		var metadata outboxChannelMetadata
		if err := json.Unmarshal(message.ChannelMetadataJson, &metadata); err == nil {
			out.InReplyTo = metadata.InReplyTo
			out.References = metadata.References
		}
	}

	for _, a := range attachments {
		content, err := w.attachmentContent(ctx, a.StorageKey)
		if err != nil {
			return mail.Outbound{}, err
		}
		contentID := ""
		if a.ContentID != nil {
			contentID = *a.ContentID
		}
		out.Attachments = append(out.Attachments, mail.Attachment{
			FileName: a.FileName, ContentType: a.ContentType, Content: content,
			ContentID: contentID, Inline: a.IsInline,
		})
	}
	return out, nil
}

// attachmentContent reads one attachment's bytes out of the object store.
// .NET's equivalents are "Attachment storage is required." (no store) and
// "Attachment object is unavailable." (no object) — both ordinary failures
// here, so a storage outage retries the job rather than losing it.
func (w *OutboxWorker) attachmentContent(ctx context.Context, key string) ([]byte, error) {
	if w.storeErr != nil {
		return nil, fmt.Errorf("communications: attachment storage is required: %w", w.storeErr)
	}
	reader, err := w.store.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("communications: attachment object is unavailable: %w", err)
	}
	defer func() { _ = reader.Close() }()
	content, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("communications: read attachment object: %w", err)
	}
	return content, nil
}

// smtpConfig is SmtpDeliveryProvider's settings resolution (inventory §15.2
// step 1): the channel's own stored credential, or the host's SMTP
// configuration when the channel has none. The TLS decision is
// smtpTLSMode's — shared with channel verification, so a channel that verifies
// is a channel that can send.
func (w *OutboxWorker) smtpConfig(message store.GetOutboundMessageForSendRow) (config.MailConfig, error) {
	if message.CredentialSettingsJson == nil {
		// No credential: fall back to the host configuration, as .NET falls
		// back to SmtpOptions. A deployment with neither cannot send, and
		// fails this job rather than silently doing nothing.
		host := config.MailConfig{}
		if w.deps.Config != nil {
			host = w.deps.Config.Mail
		}
		if strings.TrimSpace(host.Host) == "" {
			return config.MailConfig{}, fmt.Errorf("communications: the channel has no credential and no host smtp configuration is set")
		}
		host.Driver = "smtp"
		host.From = message.ChannelAddress
		return host, nil
	}

	var settings smtpProviderSettings
	if err := json.Unmarshal([]byte(*message.CredentialSettingsJson), &settings); err != nil {
		return config.MailConfig{}, fmt.Errorf("communications: decode channel settings: %w", err)
	}
	tlsMode, err := smtpTLSMode(settings.UseSsl, settings.Port)
	if err != nil {
		return config.MailConfig{}, err
	}
	username := ""
	if settings.Username != nil {
		username = *settings.Username
	}
	password := ""
	if message.CredentialSecretCiphertext != nil && w.deps.Secrets != nil {
		// A credential that can no longer be opened becomes an empty password
		// rather than an error, the same swallow the update path already makes
		// (inventory §15.6).
		if sealed, decodeErr := decodeCiphertext(*message.CredentialSecretCiphertext); decodeErr == nil {
			if opened, openErr := w.deps.Secrets.Open(channelCredentialPurpose, sealed); openErr == nil {
				password = string(opened)
			}
		}
	}
	return config.MailConfig{
		Driver: "smtp", Host: settings.Host, Port: int(settings.Port),
		Username: username, Password: password, From: message.ChannelAddress, TLS: tlsMode,
	}, nil
}

// sender is Deps.SMTPSend, or mail.SendOutbound when no harness substituted
// one — the same seam, and the same default, channel verification uses.
func (w *OutboxWorker) sender() func(context.Context, config.MailConfig, bool, mail.Outbound) error {
	if w.deps.SMTPSend != nil {
		return w.deps.SMTPSend
	}
	return mail.SendOutbound
}

// now is the worker's clock, so tests control time exactly as they do for the
// handlers.
func (w *OutboxWorker) now() time.Time {
	if w.deps.Clock != nil {
		return w.deps.Clock().UTC()
	}
	return time.Now().UTC()
}

func (w *OutboxWorker) logger() *slog.Logger {
	if w.deps.Logger != nil {
		return w.deps.Logger
	}
	return slog.Default()
}

// outboxDeliveryIsSendable is IsSendable (`:22-23`): everything except the
// three terminal-or-excluded statuses. Note that submission_failed IS sendable
// — a terminally failed delivery would be retried by a *new* job for the same
// message (inventory §13.5).
func outboxDeliveryIsSendable(status string) bool {
	switch status {
	case "relay_accepted", "cancelled", "suppressed":
		return false
	}
	return true
}
