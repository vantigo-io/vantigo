package agents

// Erasure and export (virtual employees design V26): what the module holds about
// a customer or a contact as a person. A run's transcript copies the customer's
// messages, and a review item holds the reply the agent wrote to them, so the
// erasure of a customer deletes the runs, transcripts and review items by
// customer_id and by every conversation the module saw linked to that customer,
// which covers a conversation linked after its run began. Communications is not
// asked: the module keeps the links it saw (agents.conversation_links). The
// action log never holds a body, so the customer's ids in it are blanked. The
// erasure of a person (a reviewer) keeps the ids of their decisions as opaque
// ids and needs nothing here.

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/agents/store"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// The kinds an erasure reports, "<module>.<what>" in the API's camelCase.
const (
	erasedRuns        = "agents.runs"
	erasedTranscripts = "agents.runMessages"
	erasedItems       = "agents.reviewItems"
	erasedActions     = "agents.actions"
	erasedMemory      = "agents.memory"
	erasedSuggestions = "agents.instructionSuggestions"
)

// personalData is this module's contracts.CustomerPersonalData and
// contracts.ContactDataEraser. It needs the pool alone, which a disabled module
// has too.
type personalData struct{ pool *pgxpool.Pool }

var (
	_ contracts.CustomerPersonalData = personalData{}
	_ contracts.ContactDataEraser    = personalData{}
)

func newCustomerPersonalData(d module.Deps) contracts.CustomerPersonalData {
	return personalData{pool: d.Pool}
}

type exportedRun struct {
	ID             uuid.UUID `json:"id"`
	ConversationID uuid.UUID `json:"conversationId"`
	State          string    `json:"state"`
	Outcome        *string   `json:"outcome,omitempty"`
	Summary        *string   `json:"summary,omitempty"`
	StartedAt      string    `json:"startedAt"`
}

type exportedItem struct {
	ID             uuid.UUID `json:"id"`
	ConversationID uuid.UUID `json:"conversationId"`
	Kind           string    `json:"kind"`
	State          string    `json:"state"`
	Item           any       `json:"item"`
	DiscardReason  *string   `json:"discardReason,omitempty"`
	CreatedAt      string    `json:"createdAt"`
}

type exportedNote struct {
	ID        uuid.UUID `json:"id"`
	Agent     string    `json:"agent"`
	Text      string    `json:"text"`
	CreatedAt string    `json:"createdAt"`
}

type exportSection struct {
	Runs        []exportedRun  `json:"runs"`
	ReviewItems []exportedItem `json:"reviewItems"`
	Memory      []exportedNote `json:"memory"`
}

// conversationsOfCustomer are the conversations the module knows to be the
// customer's.
func conversationsOfCustomer(ctx context.Context, q *store.Queries, customerID int32) ([]uuid.UUID, error) {
	ids, err := q.ConversationsOfCustomer(ctx, customerID)
	if err != nil {
		return nil, fmt.Errorf("agents: find customer %d's conversations: %w", customerID, err)
	}
	if ids == nil {
		ids = []uuid.UUID{}
	}
	return ids, nil
}

// ExportCustomerData answers the runs and the replies held for the customer's
// conversations, nil when the module holds none.
func (p personalData) ExportCustomerData(ctx context.Context, customerID int32) (any, error) {
	q := store.New(p.pool)
	convs, err := conversationsOfCustomer(ctx, q, customerID)
	if err != nil {
		return nil, err
	}
	runs, err := q.ExportRunsOfConversations(ctx, store.ExportRunsOfConversationsParams{ConversationIds: convs, CustomerID: &customerID})
	if err != nil {
		return nil, fmt.Errorf("agents: read customer %d's runs: %w", customerID, err)
	}
	items, err := q.ExportItemsOfConversations(ctx, store.ExportItemsOfConversationsParams{ConversationIds: convs, CustomerID: &customerID})
	if err != nil {
		return nil, fmt.Errorf("agents: read customer %d's review items: %w", customerID, err)
	}
	notes, err := q.ExportMemoryOfCustomer(ctx, fmt.Sprint(customerID))
	if err != nil {
		return nil, fmt.Errorf("agents: read the notes about customer %d: %w", customerID, err)
	}
	if len(runs) == 0 && len(items) == 0 && len(notes) == 0 {
		return nil, nil
	}
	out := exportSection{Runs: make([]exportedRun, 0, len(runs)), ReviewItems: make([]exportedItem, 0, len(items)), Memory: make([]exportedNote, 0, len(notes))}
	for _, n := range notes {
		out.Memory = append(out.Memory, exportedNote{ID: n.ID, Agent: n.AgentName, Text: n.Text, CreatedAt: n.CreatedAt.UTC().Format("2006-01-02T15:04:05Z")})
	}
	for _, r := range runs {
		out.Runs = append(out.Runs, exportedRun{ID: r.ID, ConversationID: r.ConversationID, State: r.State, Outcome: r.Outcome, Summary: r.Summary, StartedAt: r.StartedAt.UTC().Format("2006-01-02T15:04:05Z")})
	}
	for _, i := range items {
		var item any
		_ = json.Unmarshal(i.Item, &item)
		out.ReviewItems = append(out.ReviewItems, exportedItem{
			ID: i.ID, ConversationID: i.ConversationID, Kind: i.Kind, State: i.State, Item: item, DiscardReason: i.DiscardReason,
			CreatedAt: i.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		})
	}
	return out, nil
}

// EraseCustomerData deletes the customer's runs, transcripts and review items by
// customer_id and by every conversation of theirs, and blanks the customer's id
// in the action log, inside the customers module's transaction.
func (p personalData) EraseCustomerData(ctx context.Context, tx pgx.Tx, customerID int32) ([]contracts.ErasedData, error) {
	q := store.New(tx)
	convs, err := conversationsOfCustomer(ctx, q, customerID)
	if err != nil {
		return nil, err
	}
	out, err := erase(ctx, q, convs, &customerID)
	if err != nil {
		return nil, err
	}
	blanked, err := q.BlankActionsOfCustomer(ctx, &customerID)
	if err != nil {
		return nil, fmt.Errorf("agents: blank customer %d in the action log: %w", customerID, err)
	}
	return append(out, contracts.ErasedData{Kind: erasedActions, Count: blanked}), nil
}

// EraseContactData deletes the runs, transcripts and review items of the
// conversations the deleted contacts took part in.
func (p personalData) EraseContactData(ctx context.Context, tx pgx.Tx, contactIDs []int32) ([]contracts.ErasedData, error) {
	if len(contactIDs) == 0 {
		return zeroErased(), nil
	}
	q := store.New(tx)
	convs, err := q.ConversationsOfContacts(ctx, contactIDs)
	if err != nil {
		return nil, fmt.Errorf("agents: find the erased contacts' conversations: %w", err)
	}
	if len(convs) == 0 {
		return zeroErased(), nil
	}
	return erase(ctx, q, convs, nil)
}

func zeroErased() []contracts.ErasedData {
	return []contracts.ErasedData{{Kind: erasedRuns}, {Kind: erasedTranscripts}, {Kind: erasedItems}, {Kind: erasedMemory}, {Kind: erasedSuggestions}}
}

// erase deletes everything the module holds for the conversations, and for the
// customer's own runs and items, and says how much.
func erase(ctx context.Context, q *store.Queries, convs []uuid.UUID, customerID *int32) ([]contracts.ErasedData, error) {
	counts, err := q.CountPersonalRows(ctx, store.CountPersonalRowsParams{ConversationIds: convs, CustomerID: customerID})
	if err != nil {
		return nil, fmt.Errorf("agents: count what to erase: %w", err)
	}
	// The notes and the suggestions first: they find their runs and items by them.
	texts := make([]string, len(convs))
	for i, c := range convs {
		texts[i] = c.String()
	}
	notes, err := q.CountMemoryToErase(ctx, store.CountMemoryToEraseParams{ConversationIds: convs, ConversationTexts: texts, CustomerID: customerID})
	if err != nil {
		return nil, fmt.Errorf("agents: count the notes to erase: %w", err)
	}
	if _, err := q.DeleteMemoryToErase(ctx, store.DeleteMemoryToEraseParams{ConversationIds: convs, ConversationTexts: texts, CustomerID: customerID}); err != nil {
		return nil, fmt.Errorf("agents: erase notes: %w", err)
	}
	suggestions, err := q.CountSuggestionsToErase(ctx, store.CountSuggestionsToEraseParams{ConversationIds: convs, CustomerID: customerID})
	if err != nil {
		return nil, fmt.Errorf("agents: count the suggestions to erase: %w", err)
	}
	if _, err := q.DeleteSuggestionsToErase(ctx, store.DeleteSuggestionsToEraseParams{ConversationIds: convs, CustomerID: customerID}); err != nil {
		return nil, fmt.Errorf("agents: erase suggestions: %w", err)
	}
	// Items next, then the runs, whose transcripts and action logs go with them.
	if _, err := q.DeleteItemsOfConversations(ctx, store.DeleteItemsOfConversationsParams{ConversationIds: convs, CustomerID: customerID}); err != nil {
		return nil, fmt.Errorf("agents: erase review items: %w", err)
	}
	if _, err := q.DeleteRunsOfConversations(ctx, store.DeleteRunsOfConversationsParams{ConversationIds: convs, CustomerID: customerID}); err != nil {
		return nil, fmt.Errorf("agents: erase runs: %w", err)
	}
	if _, err := q.DeleteHandedOffOfConversations(ctx, convs); err != nil {
		return nil, fmt.Errorf("agents: erase hand-off markers: %w", err)
	}
	if _, err := q.DeleteRequestsOfConversations(ctx, convs); err != nil {
		return nil, fmt.Errorf("agents: erase run requests: %w", err)
	}
	if _, err := q.DeleteWakeupsOfCustomer(ctx, customerID); err != nil {
		return nil, fmt.Errorf("agents: erase wakeups: %w", err)
	}
	if _, err := q.DeleteRequestsOfCustomer(ctx, customerID); err != nil {
		return nil, fmt.Errorf("agents: erase the customer's run requests: %w", err)
	}
	if _, err := q.DeleteSeenSendersOfConversations(ctx, convs); err != nil {
		return nil, fmt.Errorf("agents: erase senders: %w", err)
	}
	if _, err := q.DeleteConversationLinks(ctx, convs); err != nil {
		return nil, fmt.Errorf("agents: erase conversation links: %w", err)
	}
	return []contracts.ErasedData{
		{Kind: erasedRuns, Count: counts.Runs},
		{Kind: erasedTranscripts, Count: counts.Messages},
		{Kind: erasedItems, Count: counts.Items},
		{Kind: erasedMemory, Count: notes},
		{Kind: erasedSuggestions, Count: suggestions},
	}, nil
}
