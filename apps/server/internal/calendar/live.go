package calendar

import (
	"context"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/live"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// The live topic of the Outlook sync card (live design K41). Events carry the
// person's id only: the card refetches through its ordinary request, which
// answers nobody's but the caller's.
const (
	// liveMailboxSync is the card's topic, keyed by the person whose card it
	// is: sync.changed with that person's id. It is the first topic keyed by
	// a person rather than by a thing.
	liveMailboxSync = "calendar.mailbox-sync"

	eventSyncChanged = "sync.changed"
)

// liveTopics is the module's LiveTopics. The card is authorised by the rule
// GET /calendar/mailbox-sync applies — calendar:access — and by the key being
// the caller's own id: the card is the caller's, and every other key,
// malformed or somebody else's, is ErrForbidden.
func liveTopics(module.Deps) []live.Topic {
	return []live.Topic{
		{Kind: liveMailboxSync, Authorize: func(ctx context.Context, key string) error {
			id, err := uuid.Parse(key)
			if err != nil || !live.HasPermission(ctx, "calendar:access") {
				return live.ErrForbidden
			}
			// Only the canonical spelling is the caller's key: an uppercase
			// one parses to the same id but is another topic.
			if me, ok := contracts.AsUserFrom(ctx); !ok || me != id || key != id.String() {
				return live.ErrForbidden
			}
			return nil
		}},
	}
}

// notifySync tells the person's card that it changed. It runs on q, the
// transaction that wrote (live design L6), so it is heard only if that
// commits.
func notifySync(ctx context.Context, q live.Execer, userID uuid.UUID) error {
	return live.Notify(ctx, q, liveMailboxSync+":"+userID.String(), eventSyncChanged, userID.String())
}

// notifySyncOnPool is notifySync after a single write made on the pool,
// outside a transaction. A failure to send is logged, not answered: the write
// is made, and the card's fallback poll repairs a lost event.
func (s *server) notifySyncOnPool(ctx context.Context, userID uuid.UUID) {
	if err := notifySync(ctx, s.deps.Pool, userID); err != nil {
		s.deps.Logger.WarnContext(ctx, "calendar: notify a mailbox sync", "error", err)
	}
}
