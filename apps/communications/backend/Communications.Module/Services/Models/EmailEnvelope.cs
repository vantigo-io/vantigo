namespace Vantigo.Communications.Services;

public sealed record EmailEnvelope(
    Guid MessageId,
    string FromAddress,
    string? FromDisplayName,
    string Subject,
    string? TextBody,
    string? HtmlBody,
    IReadOnlyList<string> To,
    IReadOnlyList<string> Cc,
    IReadOnlyList<string> Bcc,
    string? InReplyTo = null,
    IReadOnlyList<string>? References = null,
    IReadOnlyList<EmailAttachment>? Attachments = null);