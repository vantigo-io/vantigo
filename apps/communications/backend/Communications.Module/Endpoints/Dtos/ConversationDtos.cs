using System.Text.Json;

namespace Vantigo.Communications.Endpoints;

internal sealed record EmailRecipientRequest(string? Email);
internal sealed record CreateConversationRequest(
    Guid? ChannelId,
    IReadOnlyList<Guid?>? ParticipantIds,
    IReadOnlyList<ChannelRecipientRequest?>? Recipients,
    string? Subject,
    string? TextBody,
    string? HtmlBody,
    IReadOnlyList<EmailRecipientRequest?>? To = null,
    IReadOnlyList<EmailRecipientRequest?>? Cc = null,
    int? CustomerId = null);
internal sealed record ChannelRecipientRequest(Guid? ParticipantId, string? Address, string? Type = "to", int? ContactId = null);
internal sealed record ReplyRequest(string? TextBody, string? HtmlBody, string? Subject = null, string? ReplyMode = "reply", IReadOnlyList<Guid>? AttachmentIds = null);
internal sealed record NoteRequest(string? TextBody);
internal sealed record UpdateConversationRequest(string? Status, JsonElement? AssignedUserId, JsonElement CustomerId);

internal sealed record ParticipantResponse(Guid Id, Guid ChannelId, string Address, string? DisplayName, int? ContactId);
internal sealed record AttachmentResponse(Guid Id, string FileName, string ContentType, long SizeBytes, string? ContentId,
    string ScanStatus, bool IsInline, DateTimeOffset CreatedAt, bool DownloadAvailable, string DownloadPath);
internal sealed record AttachmentUploadResponse(Guid Id, string FileName, string ContentType, long SizeBytes,
    string ScanStatus, bool IsInline, DateTimeOffset ExpiresAt, bool Ready);
internal sealed record ReplyRecipientsResponse(bool CanReply, bool CanReplyAll, string? ReplyTo,
    IReadOnlyList<ParticipantResponse> ReplyAllCc);
internal sealed record DeliveryResponse(Guid Id, string Destination, string RecipientType, string Status, int Attempts,
    string? Error, DateTimeOffset? AcceptedAt);
internal sealed record ConversationMessageResponse(Guid Id, string Direction, ParticipantResponse? Participant, Guid? AuthorUserId,
    string? Subject, string? TextBody, string? HtmlBody,
    DateTimeOffset OccurredAt, DateTimeOffset CreatedAt, IReadOnlyList<AttachmentResponse> Attachments,
    IReadOnlyList<DeliveryResponse> Deliveries);
internal sealed record ConversationListItem(Guid Id, Guid ChannelId, string? Subject, string Status, Guid? AssignedUserId,
    int? CustomerId, string? CustomerAssociationSource, int? SuggestedCustomerId, IReadOnlyList<int> CandidateCustomerIds,
    DateTimeOffset LastActivityAt, string? PreviewText, IReadOnlyList<ParticipantResponse> Participants,
    bool Unread, IReadOnlyList<TagResponse> Tags);
internal sealed record ConversationDetailResponse(Guid Id, Guid ChannelId, string? Subject, string Status, Guid? AssignedUserId,
    int? CustomerId, string? CustomerAssociationSource, int? SuggestedCustomerId, double? SuggestedCustomerConfidence, string? SuggestedCustomerReasoning,
    IReadOnlyList<int> CandidateCustomerIds,
    DateTimeOffset LastActivityAt, string? PreviewText, DateTimeOffset CreatedAt, IReadOnlyList<ConversationMessageResponse> Messages,
    IReadOnlyList<ParticipantResponse> Participants, IReadOnlyList<TagResponse> Tags, DateTimeOffset? LastReadAt,
    ReplyRecipientsResponse ReplyRecipients);
internal sealed record ConversationMutationResponse(Guid ConversationId, Guid? MessageId, string Status, string? IdempotencyKey);