package communications

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/communications/store"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// The five kinds a person's anonymisation reports from this module, in the
// order it reports them.
//
// personalDataKindObjects counts every object key the erase queued for
// deletion on the cleanup ledger — message attachments, raw payloads and staged
// uploads alike — not message_attachments rows. It counts each
// queueObjectForDeletion call, one that wrote nothing because a record for the
// key already existed included.
const (
	personalDataKindConversations = "communications.conversations"
	personalDataKindMessages      = "communications.messages"
	personalDataKindObjects       = "communications.objects"
	personalDataKindSuggestions   = "communications.conversationSuggestions"
	personalDataKindCandidates    = "communications.conversationCandidates"
)

// customerPersonalData is this module's contracts.CustomerPersonalData
// (customers GDPR design D2). A person's correspondence is the most personal
// thing this installation holds about them, so it is handed over whole in the
// export and removed whole by the anonymisation: every conversation about
// them, with every message and attachment under it. A conversation that only
// suggests or lists them keeps its own customer and loses the reference.
type customerPersonalData struct {
	pool  *pgxpool.Pool
	clock func() time.Time
}

var _ contracts.CustomerPersonalData = (*customerPersonalData)(nil)

// newCustomerPersonalData is Module's CustomerPersonalData. The pool is the
// export's and the clock stamps the cleanup records the erase writes — both
// there whether or not the module is enabled.
func newCustomerPersonalData(d module.Deps) contracts.CustomerPersonalData {
	return &customerPersonalData{pool: d.Pool, clock: d.Clock}
}

// personalDataSection is this module's section of a person's export.
type personalDataSection struct {
	Conversations []personalDataConversation `json:"conversations"`
}

type personalDataConversation struct {
	ID             uuid.UUID             `json:"id"`
	Subject        *string               `json:"subject,omitempty"`
	Status         string                `json:"status"`
	CreatedAt      time.Time             `json:"createdAt"`
	LastActivityAt time.Time             `json:"lastActivityAt"`
	Messages       []personalDataMessage `json:"messages"`
}

type personalDataMessage struct {
	Direction   string    `json:"direction"`
	Subject     *string   `json:"subject,omitempty"`
	OccurredAt  time.Time `json:"occurredAt"`
	TextBody    *string   `json:"textBody,omitempty"`
	HTMLBody    *string   `json:"htmlBody,omitempty"`
	Attachments []string  `json:"attachments"`
}

// ExportCustomerData reads the three statements in one read-only snapshot, so
// a message written between them can never show without its conversation, and
// answers nil for a customer with no conversation. Both bodies go in, each when
// present: a message may carry either or both (validateSubjectAndBody asks for
// one of them), and an HTML-only letter's words are only in its HTML.
func (p *customerPersonalData) ExportCustomerData(ctx context.Context, customerID int32) (any, error) {
	var (
		conversations []store.CustomerConversationsForExportRow
		messages      []store.CustomerMessagesForExportRow
		attachments   []store.CustomerAttachmentNamesForExportRow
	)
	err := db.WithTx(ctx, p.pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		q := store.New(tx)
		var err error
		if conversations, err = q.CustomerConversationsForExport(ctx, customerID); err != nil || len(conversations) == 0 {
			return err
		}
		if messages, err = q.CustomerMessagesForExport(ctx, customerID); err != nil {
			return err
		}
		attachments, err = q.CustomerAttachmentNamesForExport(ctx, customerID)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("communications: read customer %d's correspondence: %w", customerID, err)
	}
	if len(conversations) == 0 {
		return nil, nil
	}

	names := make(map[uuid.UUID][]string, len(attachments))
	for _, a := range attachments {
		names[a.MessageID] = append(names[a.MessageID], a.FileName)
	}
	byConversation := make(map[uuid.UUID][]personalDataMessage, len(conversations))
	for _, m := range messages {
		files := names[m.ID]
		if files == nil {
			files = []string{}
		}
		byConversation[m.ConversationID] = append(byConversation[m.ConversationID], personalDataMessage{
			Direction: m.Direction, Subject: m.Subject, OccurredAt: m.OccurredAt, TextBody: m.TextBody, HTMLBody: m.HtmlBody, Attachments: files,
		})
	}
	section := personalDataSection{Conversations: make([]personalDataConversation, 0, len(conversations))}
	for _, c := range conversations {
		msgs := byConversation[c.ID]
		if msgs == nil {
			msgs = []personalDataMessage{}
		}
		section.Conversations = append(section.Conversations, personalDataConversation{
			ID: c.ID, Subject: c.Subject, Status: c.Status, CreatedAt: c.CreatedAt, LastActivityAt: c.LastActivityAt, Messages: msgs,
		})
	}
	return section, nil
}

// EraseCustomerData removes the person's correspondence inside the customers
// module's transaction: every message through retention's own deletes
// (deleteMessages — the delivery events before the deliveries, each object key
// on the cleanup ledger before the rows naming it go), the staged uploads'
// objects queued the same way, then the conversations, whose remaining rows
// cascade, then the participants nobody links to any more (retention's step 7).
// A message still waiting in the outbox is deleted with its job: a mail not yet
// sent to an anonymised person is not sent. The object store is never called
// here — the cleanup worker does that after this commits, and a rollback leaves
// no record behind.
func (p *customerPersonalData) EraseCustomerData(ctx context.Context, tx pgx.Tx, customerID int32) ([]contracts.ErasedData, error) {
	q := store.New(tx)
	now := p.clock()

	messageIDs, err := q.CustomerConversationMessageIDs(ctx, customerID)
	if err != nil {
		return nil, fmt.Errorf("communications: list customer %d's messages: %w", customerID, err)
	}
	var messages int64
	queued := 0
	if len(messageIDs) > 0 {
		if messages, queued, err = deleteMessages(ctx, q, messageIDs, now); err != nil {
			return nil, err
		}
	}
	uploads, err := q.CustomerConversationUploadKeys(ctx, customerID)
	if err != nil {
		return nil, fmt.Errorf("communications: list customer %d's staged uploads: %w", customerID, err)
	}
	for _, key := range uploads {
		if err := queueObjectForDeletion(ctx, q, key, nil, now); err != nil {
			return nil, err
		}
		queued++
	}
	conversations, err := q.DeleteCustomerConversations(ctx, customerID)
	if err != nil {
		return nil, fmt.Errorf("communications: delete customer %d's conversations: %w", customerID, err)
	}
	if conversations > 0 {
		if err := q.DeleteOrphanConversationParticipants(ctx); err != nil {
			return nil, fmt.Errorf("communications: delete orphan conversation participants: %w", err)
		}
		if err := q.DeleteOrphanParticipants(ctx); err != nil {
			return nil, fmt.Errorf("communications: delete orphan participants: %w", err)
		}
	}
	suggestions, err := q.ClearCustomerSuggestions(ctx, customerID)
	if err != nil {
		return nil, fmt.Errorf("communications: clear suggestions of customer %d: %w", customerID, err)
	}
	candidates, err := q.DeleteCustomerCandidates(ctx, customerID)
	if err != nil {
		return nil, fmt.Errorf("communications: delete candidate rows of customer %d: %w", customerID, err)
	}
	return []contracts.ErasedData{
		{Kind: personalDataKindConversations, Count: conversations},
		{Kind: personalDataKindMessages, Count: messages},
		{Kind: personalDataKindObjects, Count: int64(queued)},
		{Kind: personalDataKindSuggestions, Count: suggestions},
		{Kind: personalDataKindCandidates, Count: candidates},
	}, nil
}
