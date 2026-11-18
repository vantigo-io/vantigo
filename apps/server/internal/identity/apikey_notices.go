package identity

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
)

// The expiry notices of API keys (API access design K25): the identity.api-keys
// worker mails one 14 days before a key expires, one 3 days before, and one on
// the day, by UTC date. Each goes to the integration's contact, when that
// person is active, and to every active Owner, in the recipient's language,
// through the user directory's Notify, which sends as invitations are sent and
// skips whoever is not active. A key revoked or rolled first gets none.
// identity.api_key_notices records each kind sent, so none goes twice; a key
// that comes due late gets only the most urgent kind it is owed.

// sendAPIKeyNotices sends every notice due at now, a batch at a time, and
// answers how many it sent. It holds no connection while it mails: each
// notice is claimed first, by its row in api_key_notices, which another
// replica's cycle cannot claim again, and then mailed. When no one was mailed
// — every send failed, or every recipient was skipped as not active or
// without an address that mail reaches — the claim is taken back and the
// notice tried again next cycle. When someone was, it is kept, and the
// failures are logged. A process that stops between the claim and the mails
// has sent the notice to no one and never sends it: a notice goes at most
// once.
func (w *apiKeysWorker) sendAPIKeyNotices(ctx context.Context, now time.Time) (int, error) {
	q := store.New(w.deps.Pool)
	sent := 0
	for {
		due, err := q.ListAPIKeyNoticesDue(ctx, store.ListAPIKeyNoticesDueParams{Now: now, BatchSize: apiKeysBatch})
		if err != nil {
			return sent, fmt.Errorf("identity: read the API key notices due: %w", err)
		}
		owners, err := q.ListOwnerIDs(ctx)
		if err != nil {
			return sent, fmt.Errorf("identity: read the Owners: %w", err)
		}
		progressed := 0
		for _, k := range due {
			ok, err := w.sendAPIKeyNotice(ctx, q, k, owners, now)
			if err != nil {
				return sent, err
			}
			if ok {
				sent++
				progressed++
			}
		}
		if len(due) < apiKeysBatch || progressed == 0 || ctx.Err() != nil {
			return sent, nil
		}
	}
}

func (w *apiKeysWorker) sendAPIKeyNotice(ctx context.Context, q *store.Queries, k store.ListAPIKeyNoticesDueRow, owners []uuid.UUID, now time.Time) (bool, error) {
	n, err := q.InsertAPIKeyNotice(ctx, store.InsertAPIKeyNoticeParams{KeyID: k.ID, Kind: k.Kind, Now: now})
	if err != nil {
		return false, fmt.Errorf("identity: record the API key notice: %w", err)
	}
	if n == 0 {
		return false, nil
	}
	recipients := slices.Clone(owners)
	if k.ContactUserID != nil && !slices.Contains(recipients, *k.ContactUserID) {
		recipients = append([]uuid.UUID{*k.ContactUserID}, recipients...)
	}
	notice := apiKeyNotice(k)
	mailed := 0
	for _, id := range recipients {
		sent, err := w.users.notify(ctx, id, notice)
		if err != nil {
			w.deps.Logger.Warn("identity: an API key notice could not be mailed", "key", k.ID, "kind", k.Kind, "error", err)
		}
		if sent {
			mailed++
		}
	}
	if mailed == 0 {
		if err := q.DeleteAPIKeyNotice(context.WithoutCancel(ctx), store.DeleteAPIKeyNoticeParams{KeyID: k.ID, Kind: k.Kind}); err != nil {
			return false, fmt.Errorf("identity: take back an undelivered API key notice: %w", err)
		}
		return false, nil
	}
	return true, nil
}

// apiKeyNotice is the mail of k's notice, in English and Norwegian bokmål.
func apiKeyNotice(k store.ListAPIKeyNoticesDueRow) contracts.Notice {
	at := k.ExpiresAt.UTC().Format("2006-01-02 15:04")
	enSubject, nbSubject := "An API key of "+k.IntegrationName+" expires soon", "En API-nøkkel for "+k.IntegrationName+" utløper snart"
	if k.Kind == "today" {
		enSubject, nbSubject = "An API key of "+k.IntegrationName+" expires today", "En API-nøkkel for "+k.IntegrationName+" utløper i dag"
	}
	en := strings.Join([]string{
		fmt.Sprintf("The API key “%s” of the integration “%s” expires at %s UTC. After that, every request made with it is refused with 401 api_key_expired.", k.Name, k.IntegrationName, at),
		"To keep the integration working, roll the key under Workspace admin › API access before then, and give the new key to the application that uses it.",
	}, "\n\n")
	nb := strings.Join([]string{
		fmt.Sprintf("API-nøkkelen «%s» for integrasjonen «%s» utløper %s UTC. Etter det avvises hver forespørsel med den med 401 api_key_expired.", k.Name, k.IntegrationName, at),
		"For at integrasjonen skal fortsette å virke, roterer du nøkkelen under Administrer arbeidsområde › API-tilgang før den tid, og gir den nye nøkkelen til programmet som bruker den.",
	}, "\n\n")
	return contracts.Notice{Texts: map[string]contracts.NoticeText{
		"en": {Subject: enSubject, Body: en},
		"nb": {Subject: nbSubject, Body: nb},
	}}
}
