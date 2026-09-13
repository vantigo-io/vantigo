package communications

import (
	"context"

	"github.com/vantigo-io/vantigo/server/internal/communications/gen"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// Every operation of communications.yaml, each answering
// module.ErrNotImplemented, which the strict server's response-error
// handler turns into a 501. Mounting them all is what satisfies
// module.Router's "never registered" check, so the contract is fully
// routed from the first commit and each later task replaces the stubs of
// the area it implements.

// PostCommunicationsConversationsByIdAiDraft Generate an AI reply draft
// (POST /api/v1/communications/conversations/{id}/ai/draft)
func (s *server) PostCommunicationsConversationsByIdAiDraft(context.Context, gen.PostCommunicationsConversationsByIdAiDraftRequestObject) (gen.PostCommunicationsConversationsByIdAiDraftResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// PostCommunicationsConversationsByIdAiCustomerSuggestion Suggest a customer for a conversation
// (POST /api/v1/communications/conversations/{id}/ai/customer-suggestion)
func (s *server) PostCommunicationsConversationsByIdAiCustomerSuggestion(context.Context, gen.PostCommunicationsConversationsByIdAiCustomerSuggestionRequestObject) (gen.PostCommunicationsConversationsByIdAiCustomerSuggestionResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// GetCommunicationsStatsSummary, GetCommunicationsStatsTimeseries and
// GetCommunicationsStatsAttention moved to stats.go in task 9.
