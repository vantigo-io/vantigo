namespace Vantigo.Communications.Services;

internal sealed record MailgunInboundForm(
    string Timestamp,
    string Token,
    string Signature,
    string? Sender,
    string? From,
    string? Recipient,
    string? Cc,
    string? Subject,
    string? Text,
    string? Html,
    string? Mime,
    string? Headers,
    IReadOnlyList<MailgunFormAttachment> Attachments);