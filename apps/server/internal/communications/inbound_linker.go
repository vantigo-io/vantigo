package communications

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/communications/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// senderLink is what the linker found for a received message's sender (M15;
// delivery D, reading 19): the customers every contact with the address is
// linked to, deduplicated, and the contact when exactly one has it.
type senderLink struct {
	customers []int32
	contact   *int32
}

// lookupSender asks the customer directory for the sender's address, before
// the import's transaction. No directory (Customers off), no address or a
// directory that fails all link nothing: the import goes on unlinked, and a
// failure is logged.
func (w *MailboxSyncWorker) lookupSender(ctx context.Context, channelID uuid.UUID, address string) senderLink {
	return lookupSenderLink(ctx, w.deps, w.logger(), mailboxSyncWorkerName, channelID, address)
}

// lookupSenderLink is lookupSender for any worker that imports what a person
// sent: workerName is how a failure is logged.
func lookupSenderLink(ctx context.Context, deps module.Deps, logger *slog.Logger, workerName string, channelID uuid.UUID, address string) senderLink {
	if deps.Directory == nil || address == "" {
		return senderLink{}
	}
	matches, err := directoryContactsByEmail(ctx, deps.Directory, address)
	if err != nil {
		logger.Warn("the sender could not be looked up in the customer directory; imported unlinked",
			"worker", workerName, "channel", channelID, "error", err)
		return senderLink{}
	}
	var link senderLink
	for _, m := range matches {
		for _, id := range m.CandidateCustomerIDs {
			if !slices.Contains(link.customers, id) {
				link.customers = append(link.customers, id)
			}
		}
	}
	if len(matches) == 1 {
		id := matches[0].ContactID
		link.contact = &id
	}
	return link
}

// write is the linker's writes inside the import's transaction. A
// conversation that has a customer — set by hand or by an earlier message —
// is never linked again; one without gets the one customer, automatically,
// or the several as candidates. The sender's participant gets the one
// contact when it has none.
func (l senderLink) write(ctx context.Context, txq *store.Queries, conversationID uuid.UUID, isNew bool,
	customerID *int32, senderID *uuid.UUID, now time.Time) error {
	if senderID != nil && l.contact != nil {
		if err := txq.SetParticipantContact(ctx, store.SetParticipantContactParams{ContactID: l.contact, ID: *senderID}); err != nil {
			return fmt.Errorf("communications: link the sender's contact: %w", err)
		}
	}
	if customerID != nil {
		return nil
	}
	switch {
	case len(l.customers) == 1 && !isNew:
		if err := txq.SetConversationAutomaticCustomer(ctx, store.SetConversationAutomaticCustomerParams{
			CustomerID: &l.customers[0], ID: conversationID,
		}); err != nil {
			return fmt.Errorf("communications: link the conversation's customer: %w", err)
		}
	case len(l.customers) > 1:
		for _, id := range l.customers {
			if err := txq.InsertConversationCandidate(ctx, store.InsertConversationCandidateParams{
				ConversationID: conversationID, CustomerID: id, CreatedAt: now,
			}); err != nil {
				return fmt.Errorf("communications: add a candidate customer: %w", err)
			}
		}
	}
	return nil
}
