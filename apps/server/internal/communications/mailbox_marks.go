package communications

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/communications/store"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// mailboxMarkMaxAttempts bounds retries of one conversation's on-close
// Outlook mark job before it is marked failed (M17).
const mailboxMarkMaxAttempts = 8

// ProcessOneMark claims at most one due communications.mailbox_marks row and
// applies its read or move operation to the conversation's imported inbound
// messages in Outlook (M17).
func (w *MailboxSyncWorker) ProcessOneMark(ctx context.Context) (bool, error) {
	conversationID, leaseID, ok, err := w.claimMark(ctx)
	if err != nil || !ok {
		return false, err
	}
	attempts, err := w.markCycle(ctx, conversationID, leaseID)
	switch {
	case err == nil:
		return true, nil
	case ctx.Err() != nil:
		return true, err
	case errors.Is(err, errSyncLeaseLost):
		w.logger().Info("mailbox mark stopped: another worker took the job's lease",
			"worker", mailboxSyncWorkerName, "conversation", conversationID)
		return true, nil
	}
	return true, w.recordMarkFailure(ctx, conversationID, leaseID, attempts, err)
}

func (w *MailboxSyncWorker) claimMark(ctx context.Context) (uuid.UUID, string, bool, error) {
	leaseID := hexN(uuid.New())
	now := w.now()
	leaseUntil := now.Add(mailboxSyncLease)
	q := store.New(w.deps.Pool)
	for range mailboxSyncClaimAttempts {
		conversationID, err := q.SelectDueMailboxMark(ctx, now)
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, "", false, nil
		}
		if err != nil {
			return uuid.Nil, "", false, fmt.Errorf("communications: select a mailbox mark due: %w", err)
		}
		rows, err := q.ClaimMailboxMark(ctx, store.ClaimMailboxMarkParams{
			LeaseID: &leaseID, LeaseUntil: &leaseUntil, Now: now, ConversationID: conversationID,
		})
		if err != nil {
			return uuid.Nil, "", false, fmt.Errorf("communications: claim a mailbox mark: %w", err)
		}
		if rows == 1 {
			return conversationID, leaseID, true, nil
		}
	}
	return uuid.Nil, "", false, nil
}

func (w *MailboxSyncWorker) markCycle(ctx context.Context, conversationID uuid.UUID, leaseID string) (int32, error) {
	q := store.New(w.deps.Pool)
	row, err := q.GetMailboxMarkForCycle(ctx, conversationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("communications: read the claimed mailbox mark: %w", err)
	}
	if row.LeaseID == nil || *row.LeaseID != leaseID {
		return row.Attempts, errSyncLeaseLost
	}
	if row.ChannelProvider != providerEntra || !row.ChannelIsActive {
		return row.Attempts, w.guarded(q.CompleteMailboxMark(ctx, store.CompleteMailboxMarkParams{
			Now: ptrTime(w.now()), ConversationID: conversationID, LeaseID: &leaseID,
		}))
	}

	provider, err := w.syncMailbox(ctx)
	if err != nil {
		return row.Attempts, err
	}
	if provider == nil {
		return row.Attempts, errSyncProviderHeld
	}
	status, err := syncMailboxStatus(ctx, provider)
	if err != nil || status.State != contracts.MailboxAvailable {
		return row.Attempts, errSyncProviderHeld
	}
	mailbox, err := channelMailboxOf(row.ChannelSettings)
	if err != nil {
		return row.Attempts, err
	}
	if mailbox == "" {
		return row.Attempts, contracts.ErrMailboxUnknown
	}

	providerIDs, err := q.ListConversationInboundProviderIDs(ctx, store.ListConversationInboundProviderIDsParams{
		ChannelID: row.ChannelID, ConversationID: conversationID,
	})
	if err != nil {
		return row.Attempts, fmt.Errorf("communications: list inbound provider ids for mark: %w", err)
	}

	if len(providerIDs) > 0 {
		op := contracts.MailMark{Kind: contracts.MailMarkRead}
		if row.Operation == onCloseMove {
			folderName := defaultOnCloseFolder
			if row.Folder != nil && strings.TrimSpace(*row.Folder) != "" {
				folderName = strings.TrimSpace(*row.Folder)
			}
			folderID, ferr := mailboxEnsureFolder(ctx, provider, mailbox, folderName)
			if ferr != nil {
				return row.Attempts, ferr
			}
			op = contracts.MailMark{Kind: contracts.MailMarkMove, FolderID: folderID}
		}
		for _, providerID := range providerIDs {
			merr := mailboxMark(ctx, provider, mailbox, providerID, op)
			if errors.Is(merr, contracts.ErrMailItemGone) {
				continue
			}
			if merr != nil {
				return row.Attempts, merr
			}
		}
	}

	return row.Attempts, w.guarded(q.CompleteMailboxMark(ctx, store.CompleteMailboxMarkParams{
		Now: ptrTime(w.now()), ConversationID: conversationID, LeaseID: &leaseID,
	}))
}

func markFailureCode(err error) (code string, refused bool) {
	switch {
	case errors.Is(err, contracts.ErrMailboxDenied):
		return "mailbox_denied", true
	case errors.Is(err, contracts.ErrMailboxGone):
		return "mailbox_gone", true
	case errors.Is(err, contracts.ErrMailboxUnknown):
		return "mailbox_unknown", true
	}
	return "mark_failed", false
}

func (w *MailboxSyncWorker) recordMarkFailure(ctx context.Context, conversationID uuid.UUID, leaseID string, attempts int32, cause error) error {
	q := store.New(w.deps.Pool)
	now := w.now()
	if errors.Is(cause, errSyncProviderHeld) || errors.Is(cause, contracts.ErrMailboxUnavailable) {
		w.logger().Warn("mailbox mark held: the mail channels provider is not available",
			"worker", mailboxSyncWorkerName, "conversation", conversationID, "code", "provider_unavailable")
		err := w.guarded(q.HoldMailboxMark(ctx, store.HoldMailboxMarkParams{
			NextAttemptAt: now.Add(mailboxSyncEvery), ConversationID: conversationID, LeaseID: &leaseID,
		}))
		if errors.Is(err, errSyncLeaseLost) {
			return nil
		}
		return err
	}

	code, refused := markFailureCode(cause)
	status := "retry"
	if refused || attempts >= mailboxMarkMaxAttempts {
		status = "failed"
	}
	w.logger().Warn("mailbox mark failed",
		"worker", mailboxSyncWorkerName, "conversation", conversationID, "code", code, "status", status)
	err := w.guarded(q.FailMailboxMark(ctx, store.FailMailboxMarkParams{
		Status: status, LastError: &code, NextAttemptAt: now.Add(syncBackoff(max(attempts-1, 0))),
		ConversationID: conversationID, LeaseID: &leaseID,
	}))
	if errors.Is(err, errSyncLeaseLost) {
		return nil
	}
	return err
}
