using Vantigo.Tenancy.Abstractions;

namespace Vantigo.Communications.Database.Communications;

internal sealed class Channel : ITenantOwned
{
    public Guid TenantId { get; set; }
    public Guid Id { get; set; }
    public required string Type { get; set; }
    public required string Address { get; set; }
    public string? DisplayName { get; set; }
    public string Provider { get; set; } = "smtp";
    public bool IsDefault { get; set; }
    public bool IsActive { get; set; } = true;
    public DateTimeOffset CreatedAt { get; set; }
    public ChannelCredential? Credential { get; set; }
    public ICollection<Conversation> Conversations { get; set; } = [];
    public ICollection<Participant> Participants { get; set; } = [];
}

internal sealed class ChannelCredential : ITenantOwned
{
    public Guid TenantId { get; set; }
    public Guid Id { get; set; }
    public Guid ChannelId { get; set; }
    public required string SettingsJson { get; set; }
    public required string SecretCiphertext { get; set; }
    public DateTimeOffset CreatedAt { get; set; }
    public Channel? Channel { get; set; }
}

internal sealed class Participant : ITenantOwned
{
    public Guid TenantId { get; set; }
    public Guid Id { get; set; }
    public Guid ChannelId { get; set; }
    public required string Address { get; set; }
    public string? DisplayName { get; set; }
    public int? ContactId { get; set; }
    public DateTimeOffset CreatedAt { get; set; }
    public Channel? Channel { get; set; }
    public ICollection<ConversationMessage> Messages { get; set; } = [];
    public ICollection<ConversationParticipant> Conversations { get; set; } = [];
}

internal sealed class Conversation : ITenantOwned
{
    public Guid TenantId { get; set; }
    public Guid Id { get; set; }
    public Guid ChannelId { get; set; }
    public string? Subject { get; set; }
    public string Status { get; set; } = "open";
    public Guid? AssignedUserId { get; set; }
    public int? CustomerId { get; set; }
    public string? CustomerAssociationSource { get; set; }
    public int? SuggestedCustomerId { get; set; }
    public double? SuggestedCustomerConfidence { get; set; }
    public string? SuggestedCustomerReasoning { get; set; }
    public DateTimeOffset LastActivityAt { get; set; }
    public string? PreviewText { get; set; }
    public DateTimeOffset CreatedAt { get; set; }

    public Channel? Channel { get; set; }
    public ICollection<ConversationMessage> Messages { get; set; } = [];
    public ICollection<ConversationParticipant> Participants { get; set; } = [];
    public ICollection<ConversationCustomerCandidate> CustomerCandidates { get; set; } = [];
    public ICollection<ConversationTag> Tags { get; set; } = [];
    public ICollection<ConversationReadState> ReadStates { get; set; } = [];
}

internal sealed class ConversationCustomerCandidate : ITenantOwned
{
    public Guid TenantId { get; set; }
    public Guid ConversationId { get; set; }
    public int CustomerId { get; set; }
    public DateTimeOffset CreatedAt { get; set; }
    public Conversation? Conversation { get; set; }
}

internal sealed class ConversationMessage : ITenantOwned
{
    public Guid TenantId { get; set; }
    public Guid Id { get; set; }
    public Guid ConversationId { get; set; }
    public required string Direction { get; set; }
    public Guid? ParticipantId { get; set; }
    public Guid? AuthorUserId { get; set; }
    public string? Subject { get; set; }
    public string? TextBody { get; set; }
    public string? HtmlBody { get; set; }
    public string? ChannelMetadataJson { get; set; }
    public string? RawPayloadStorageKey { get; set; }
    public DateTimeOffset OccurredAt { get; set; }
    public DateTimeOffset CreatedAt { get; set; }
    public string? RfcMessageId { get; set; }

    public Conversation? Conversation { get; set; }
    public Participant? Participant { get; set; }
    public ICollection<MessageDelivery> Deliveries { get; set; } = [];
    public ICollection<MessageEvent> Events { get; set; } = [];
    public ICollection<MessageAttachment> Attachments { get; set; } = [];
}

internal sealed class ConversationParticipant : ITenantOwned
{
    public Guid TenantId { get; set; }
    public Guid ConversationId { get; set; }
    public Guid ParticipantId { get; set; }
    public string Role { get; set; } = "participant";
    public Conversation? Conversation { get; set; }
    public Participant? Participant { get; set; }
}

internal sealed class MessageAttachment : ITenantOwned
{
    public Guid TenantId { get; set; }
    public Guid Id { get; set; }
    public Guid MessageId { get; set; }
    public required string FileName { get; set; }
    public required string ContentType { get; set; }
    public long SizeBytes { get; set; }
    public required string ContentHash { get; set; }
    public string? ContentId { get; set; }
    public required string StorageKey { get; set; }
    public string ScanStatus { get; set; } = "pending";
    public int ScanAttempts { get; set; }
    public DateTimeOffset NextScanAt { get; set; } = DateTimeOffset.UtcNow;
    public string? ScanLeaseId { get; set; }
    public DateTimeOffset? ScanLeaseUntil { get; set; }
    public string? ScanError { get; set; }
    public bool IsInline { get; set; }
    public DateTimeOffset CreatedAt { get; set; }
    public ConversationMessage? Message { get; set; }
}

/// <summary>Durable upload staging. The object is never released to a message until it is clean.</summary>
internal sealed class AttachmentUpload : ITenantOwned
{
    public Guid TenantId { get; set; }
    public Guid Id { get; set; }
    public Guid ConversationId { get; set; }
    public Guid UploadedByUserId { get; set; }
    public required string IdempotencyKey { get; set; }
    public required string FileName { get; set; }
    public required string ContentType { get; set; }
    public long SizeBytes { get; set; }
    public required string ContentHash { get; set; }
    public string? ContentId { get; set; }
    public required string StorageKey { get; set; }
    public string ScanStatus { get; set; } = "pending";
    public int ScanAttempts { get; set; }
    public DateTimeOffset NextScanAt { get; set; } = DateTimeOffset.UtcNow;
    public string? ScanLeaseId { get; set; }
    public DateTimeOffset? ScanLeaseUntil { get; set; }
    public string? ScanError { get; set; }
    public bool IsInline { get; set; }
    public DateTimeOffset ExpiresAt { get; set; }
    public DateTimeOffset CreatedAt { get; set; }
    public Conversation? Conversation { get; set; }
}

internal sealed class MessageDelivery : ITenantOwned
{
    public Guid TenantId { get; set; }
    public Guid Id { get; set; }
    public Guid MessageId { get; set; }
    public required string RecipientAddress { get; set; }
    public required string RecipientType { get; set; }
    public Guid? RecipientParticipantId { get; set; }
    public string Status { get; set; } = "queued";
    public int Attempts { get; set; }
    public string? LastError { get; set; }
    public DateTimeOffset? AcceptedAt { get; set; }
    public DateTimeOffset CreatedAt { get; set; }
    public ConversationMessage? Message { get; set; }
    public Participant? RecipientParticipant { get; set; }
    public ICollection<MessageEvent> Events { get; set; } = [];
}

internal sealed class MessageEvent : ITenantOwned
{
    public Guid TenantId { get; set; }
    public Guid Id { get; set; }
    public Guid MessageId { get; set; }
    public Guid? DeliveryId { get; set; }
    public required string EventType { get; set; }
    public DateTimeOffset OccurredAt { get; set; }
    public string? DataJson { get; set; }
    public ConversationMessage? Message { get; set; }
    public MessageDelivery? Delivery { get; set; }
}

internal sealed class Tag : ITenantOwned
{
    public Guid TenantId { get; set; }
    public Guid Id { get; set; }
    public required string Name { get; set; }
    public string? Color { get; set; }
    public ICollection<ConversationTag> Conversations { get; set; } = [];
}

internal sealed class ConversationTag : ITenantOwned
{
    public Guid TenantId { get; set; }
    public Guid ConversationId { get; set; }
    public Guid TagId { get; set; }
    public Conversation? Conversation { get; set; }
    public Tag? Tag { get; set; }
}

internal sealed class ConversationReadState : ITenantOwned
{
    public Guid TenantId { get; set; }
    public Guid ConversationId { get; set; }
    public Guid UserId { get; set; }
    public DateTimeOffset LastReadAt { get; set; }
    public Conversation? Conversation { get; set; }
}

internal sealed class Suppression : ITenantOwned
{
    public Guid TenantId { get; set; }
    public Guid Id { get; set; }
    public required string NormalizedEmailAddress { get; set; }
    public string? Reason { get; set; }
    public DateTimeOffset CreatedAt { get; set; }
}

internal sealed class IdempotencyRecord : ITenantOwned
{
    public Guid TenantId { get; set; }
    public Guid Id { get; set; }
    public required string Key { get; set; }
    public required string PayloadFingerprint { get; set; }
    public Guid ConversationId { get; set; }
    public Guid MessageId { get; set; }
    public DateTimeOffset CreatedAt { get; set; }
}

internal sealed class InboundReceipt : ITenantOwned
{
    public Guid TenantId { get; set; }
    public Guid Id { get; set; }
    public Guid ChannelId { get; set; }
    public required string Provider { get; set; }
    public required string ProviderEventId { get; set; }
    public string Status { get; set; } = "reserving";
    public string? RfcMessageId { get; set; }
    public string? PayloadHash { get; set; }
    public Guid? ConversationMessageId { get; set; }
    public DateTimeOffset ReceivedAt { get; set; }
    public DateTimeOffset? ReservationExpiresAt { get; set; }
    public Channel? Channel { get; set; }
    public ConversationMessage? ConversationMessage { get; set; }
    public InboundEmailJob? Job { get; set; }
}

internal sealed class InboundEmailJob : ITenantOwned
{
    public Guid TenantId { get; set; }
    public Guid Id { get; set; }
    public Guid ChannelId { get; set; }
    public Guid InboundReceiptId { get; set; }
    public required string RawMimeStorageKey { get; set; }
    public required string Status { get; set; }
    public int Attempts { get; set; }
    public DateTimeOffset NextAttemptAt { get; set; }
    public string? LeaseId { get; set; }
    public DateTimeOffset? LeaseUntil { get; set; }
    public DateTimeOffset? CompletedAt { get; set; }
    public string? LastError { get; set; }
    public string? EnvelopeSenderAddress { get; set; }
    public string? EnvelopeRecipientAddress { get; set; }
    public bool IsSynthetic { get; set; }
    public DateTimeOffset ReceivedAt { get; set; }
    public DateTimeOffset CreatedAt { get; set; }
    public Channel? Channel { get; set; }
    public InboundReceipt? InboundReceipt { get; set; }
}

internal sealed class AttachmentCleanupRecord : ITenantOwned
{
    public Guid TenantId { get; set; }
    public Guid Id { get; set; }
    public Guid? MessageId { get; set; }
    public required string StorageKey { get; set; }
    public string Status { get; set; } = "pending";
    public int Attempts { get; set; }
    public DateTimeOffset NextAttemptAt { get; set; }
    public string? LeaseId { get; set; }
    public DateTimeOffset? LeaseUntil { get; set; }
    public DateTimeOffset? ReservationExpiresAt { get; set; }
    public string? LastError { get; set; }
    public DateTimeOffset CreatedAt { get; set; }
}

internal sealed class OutboxJob : ITenantOwned
{
    public Guid TenantId { get; set; }
    public Guid Id { get; set; }
    public Guid MessageId { get; set; }
    public string Status { get; set; } = "pending";
    public int Attempts { get; set; }
    public DateTimeOffset NextAttemptAt { get; set; }
    public string? LeaseId { get; set; }
    public DateTimeOffset? LeaseUntil { get; set; }

    /// <summary>
    /// Stamped immediately before the external send. A job re-claimed with
    /// this set may already have been delivered (the crash happened between
    /// send and completion commit); recovery still resends — delivery is
    /// at-least-once — but flags the possible duplicate instead of staying
    /// silent.
    /// </summary>
    public DateTimeOffset? DeliveryAttemptedAt { get; set; }

    public DateTimeOffset? CompletedAt { get; set; }
    public string? LastError { get; set; }
    public DateTimeOffset CreatedAt { get; set; }
    public ConversationMessage? Message { get; set; }
}

internal sealed class AiInteraction : ITenantOwned
{
    public Guid TenantId { get; set; }
    public Guid Id { get; set; }
    public Guid ConversationId { get; set; }
    public Guid? MessageId { get; set; }
    public required string Operation { get; set; }
    public Guid? RequesterUserId { get; set; }
    public required string Provider { get; set; }
    public required string Model { get; set; }
    public required string ContextDigest { get; set; }
    public required string ContextVersion { get; set; }
    public string? ResultSummary { get; set; }
    public string? ValidationSummary { get; set; }
    public string? ErrorSummary { get; set; }
    public long? DurationMs { get; set; }
    public int? InputTokenCount { get; set; }
    public int? OutputTokenCount { get; set; }
    public DateTimeOffset CreatedAt { get; set; }
    public Conversation? Conversation { get; set; }
    public ConversationMessage? Message { get; set; }
}