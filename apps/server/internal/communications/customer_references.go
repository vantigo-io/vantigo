package communications

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/communications/store"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// The three kinds of customer reference this module holds: a conversation's
// customer, the customer suggested for it, and its candidate list.
const (
	customerReferenceKindConversations = "communications.conversations"
	customerReferenceKindSuggestions   = "communications.conversationSuggestions"
	customerReferenceKindCandidates    = "communications.conversationCandidates"
)

// customerReferenceHolder is this module's contracts.CustomerReferenceHolder
// (customers merge design D1): when two customers are merged, every
// conversation about, suggested for, or listing the absorbed one is about,
// suggested for, or lists the survivor.
type customerReferenceHolder struct{}

var _ contracts.CustomerReferenceHolder = customerReferenceHolder{}

// newCustomerReferenceHolder is Module's CustomerReferences.
func newCustomerReferenceHolder(module.Deps) contracts.CustomerReferenceHolder {
	return customerReferenceHolder{}
}

// RepointCustomer runs the three re-points inside the caller's transaction, in
// the order the kinds are reported.
func (customerReferenceHolder) RepointCustomer(ctx context.Context, tx pgx.Tx, from, into int32) ([]contracts.RepointedReferences, error) {
	q := store.New(tx)
	conversations, err := q.RepointConversationsCustomer(ctx, store.RepointConversationsCustomerParams{FromCustomerID: from, IntoCustomerID: into})
	if err != nil {
		return nil, fmt.Errorf("communications: re-point customer %d's conversations to %d: %w", from, into, err)
	}
	suggestions, err := q.RepointConversationsSuggestedCustomer(ctx, store.RepointConversationsSuggestedCustomerParams{FromCustomerID: from, IntoCustomerID: into})
	if err != nil {
		return nil, fmt.Errorf("communications: re-point customer %d's suggestions to %d: %w", from, into, err)
	}
	candidates, err := q.RepointConversationCustomerCandidates(ctx, store.RepointConversationCustomerCandidatesParams{FromCustomerID: from, IntoCustomerID: into})
	if err != nil {
		return nil, fmt.Errorf("communications: re-point customer %d's candidates to %d: %w", from, into, err)
	}
	return []contracts.RepointedReferences{
		{Kind: customerReferenceKindConversations, Count: conversations},
		{Kind: customerReferenceKindSuggestions, Count: suggestions},
		{Kind: customerReferenceKindCandidates, Count: candidates.Moved},
	}, nil
}
