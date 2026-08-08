namespace Vantigo.Communications.Api.Database.Communications;

public sealed class SharedMailbox
{
    public Guid Id { get; set; }
    public required string FromAddress { get; set; }
    public string? DisplayName { get; set; }
    public string Provider { get; set; } = "smtp";
    public bool IsDefault { get; set; }
    public DateTimeOffset CreatedAt { get; set; }
    public bool IsActive { get; set; } = true;
    public MailboxProviderCredential? Credential { get; set; }
}

public sealed class MailboxProviderCredential
{
    public Guid Id { get; set; }
    public Guid MailboxId { get; set; }
    public required string Provider { get; set; }
    public required string SettingsJson { get; set; }
    public required string SecretCiphertext { get; set; }
    public DateTimeOffset CreatedAt { get; set; }
    public SharedMailbox? Mailbox { get; set; }
}

public sealed class EmailMessage
{
    public Guid Id { get; set; }
    public Guid MailboxId { get; set; }
    public required string Subject { get; set; }
    public string? TextBody { get; set; }
    public string? HtmlBody { get; set; }
    public DateTimeOffset CreatedAt { get; set; }
    public Guid? CreatedByUserId { get; set; }
    public string? Source { get; set; }
    public DateTimeOffset? ArchivedAt { get; set; }

    public SharedMailbox? Mailbox { get; set; }
    public ICollection<RecipientDelivery> Deliveries { get; set; } = [];
    public ICollection<MessageEvent> Events { get; set; } = [];
    public ICollection<ExternalEntityLink> ExternalLinks { get; set; } = [];
}

public sealed class RecipientDelivery
{
    public Guid Id { get; set; }
    public Guid MessageId { get; set; }
    public required string EmailAddress { get; set; }
    public required string RecipientType { get; set; }
    public string Status { get; set; } = "queued";
    public int Attempts { get; set; }
    public string? LastError { get; set; }
    public DateTimeOffset? AcceptedAt { get; set; }
    public DateTimeOffset CreatedAt { get; set; }
    public EmailMessage? Message { get; set; }
    public ICollection<MessageEvent> Events { get; set; } = [];
}

public sealed class MessageEvent
{
    public Guid Id { get; set; }
    public Guid MessageId { get; set; }
    public Guid? DeliveryId { get; set; }
    public required string EventType { get; set; }
    public DateTimeOffset OccurredAt { get; set; }
    public string? DataJson { get; set; }
    public EmailMessage? Message { get; set; }
    public RecipientDelivery? Delivery { get; set; }
}

public sealed class ExternalEntityLink
{
    public Guid Id { get; set; }
    public Guid MessageId { get; set; }
    public required string SourceSystem { get; set; }
    public required string SourceInstance { get; set; }
    public required string EntityType { get; set; }
    public required string ExternalEntityId { get; set; }
    public string? DisplayLabel { get; set; }
    public EmailMessage? Message { get; set; }
}

public sealed class Suppression
{
    public Guid Id { get; set; }
    public required string NormalizedEmailAddress { get; set; }
    public string? Reason { get; set; }
    public DateTimeOffset CreatedAt { get; set; }
}

public sealed class IdempotencyRecord
{
    public Guid Id { get; set; }
    public required string Key { get; set; }
    public required string PayloadFingerprint { get; set; }
    public Guid MessageId { get; set; }
    public DateTimeOffset CreatedAt { get; set; }
}

public sealed class OutboxJob
{
    public Guid Id { get; set; }
    public Guid MessageId { get; set; }
    public string Status { get; set; } = "pending";
    public int Attempts { get; set; }
    public DateTimeOffset NextAttemptAt { get; set; }
    public string? LeaseId { get; set; }
    public DateTimeOffset? LeaseUntil { get; set; }
    public DateTimeOffset? CompletedAt { get; set; }
    public string? LastError { get; set; }
    public DateTimeOffset CreatedAt { get; set; }
    public EmailMessage? Message { get; set; }
}