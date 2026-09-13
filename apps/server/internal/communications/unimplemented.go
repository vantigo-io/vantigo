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

// PostCommunicationsConversationsByIdReply Reply to a conversation
// (POST /api/v1/communications/conversations/{id}/reply)
func (s *server) PostCommunicationsConversationsByIdReply(context.Context, gen.PostCommunicationsConversationsByIdReplyRequestObject) (gen.PostCommunicationsConversationsByIdReplyResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// PostCommunicationsConversationsByIdAttachments Stage an attachment
// (POST /api/v1/communications/conversations/{id}/attachments)
func (s *server) PostCommunicationsConversationsByIdAttachments(context.Context, gen.PostCommunicationsConversationsByIdAttachmentsRequestObject) (gen.PostCommunicationsConversationsByIdAttachmentsResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// GetCommunicationsConversationsByConversationIdAttachmentsByAttachmentId
// Get a staged attachment's status
// (GET /api/v1/communications/conversations/{conversationId}/attachments/{attachmentId})
func (s *server) GetCommunicationsConversationsByConversationIdAttachmentsByAttachmentId(context.Context, gen.GetCommunicationsConversationsByConversationIdAttachmentsByAttachmentIdRequestObject) (gen.GetCommunicationsConversationsByConversationIdAttachmentsByAttachmentIdResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// GetCommunicationsAttachmentsByIdDownload Download a message attachment
// (GET /api/v1/communications/attachments/{id}/download)
func (s *server) GetCommunicationsAttachmentsByIdDownload(context.Context, gen.GetCommunicationsAttachmentsByIdDownloadRequestObject) (gen.GetCommunicationsAttachmentsByIdDownloadResponseObject, error) {
	return nil, module.ErrNotImplemented
}

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

// GetCommunicationsSuppressions List suppressed addresses
// (GET /api/v1/communications/suppressions)
func (s *server) GetCommunicationsSuppressions(context.Context, gen.GetCommunicationsSuppressionsRequestObject) (gen.GetCommunicationsSuppressionsResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// PostCommunicationsSuppressions Suppress an address
// (POST /api/v1/communications/suppressions)
func (s *server) PostCommunicationsSuppressions(context.Context, gen.PostCommunicationsSuppressionsRequestObject) (gen.PostCommunicationsSuppressionsResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// GetCommunicationsSuppressionsById Get a suppressed address
// (GET /api/v1/communications/suppressions/{id})
func (s *server) GetCommunicationsSuppressionsById(context.Context, gen.GetCommunicationsSuppressionsByIdRequestObject) (gen.GetCommunicationsSuppressionsByIdResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// DeleteCommunicationsSuppressionsById Remove a suppressed address
// (DELETE /api/v1/communications/suppressions/{id})
func (s *server) DeleteCommunicationsSuppressionsById(context.Context, gen.DeleteCommunicationsSuppressionsByIdRequestObject) (gen.DeleteCommunicationsSuppressionsByIdResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// GetCommunicationsStatsSummary Get communications dashboard summary
// (GET /api/v1/communications/stats/summary)
func (s *server) GetCommunicationsStatsSummary(context.Context, gen.GetCommunicationsStatsSummaryRequestObject) (gen.GetCommunicationsStatsSummaryResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// GetCommunicationsStatsTimeseries Get communications dashboard time series
// (GET /api/v1/communications/stats/timeseries)
func (s *server) GetCommunicationsStatsTimeseries(context.Context, gen.GetCommunicationsStatsTimeseriesRequestObject) (gen.GetCommunicationsStatsTimeseriesResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// GetCommunicationsStatsAttention Get communications dashboard attention items
// (GET /api/v1/communications/stats/attention)
func (s *server) GetCommunicationsStatsAttention(context.Context, gen.GetCommunicationsStatsAttentionRequestObject) (gen.GetCommunicationsStatsAttentionResponseObject, error) {
	return nil, module.ErrNotImplemented
}
