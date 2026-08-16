namespace Vantigo.Communications.Services;

public sealed record NormalizedInboundMessage(
    string FromAddress,
    string? FromDisplayName,
    IReadOnlyList<string> To,
    IReadOnlyList<string> Cc,
    string? Subject,
    string? Text,
    string? Html,
    string? RfcMessageId,
    string? InReplyTo,
    IReadOnlyList<string> References,
    IReadOnlyList<NormalizedInboundAttachment> Attachments,
    DateTimeOffset OccurredAt,
    string? ProviderMessageId,
    byte[]? RawPayload);