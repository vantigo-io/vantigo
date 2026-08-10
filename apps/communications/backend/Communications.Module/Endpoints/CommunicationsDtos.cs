using System.Text;

using MimeKit;

using Vantigo.Communications.Database.Communications;
using Vantigo.Communications.Services;

namespace Vantigo.Communications.Endpoints;

public sealed record EmailRecipientRequest(string? Email);

public sealed record ExternalEntityLinkRequest(
    string? SourceSystem,
    string? SourceInstance,
    string? EntityType,
    string? ExternalEntityId,
    string? DisplayLabel);

public sealed record CreateEmailRequest(
    string? Subject,
    string? TextBody,
    string? HtmlBody,
    IReadOnlyList<EmailRecipientRequest?>? To,
    IReadOnlyList<EmailRecipientRequest?>? Cc,
    IReadOnlyList<EmailRecipientRequest?>? Bcc,
    IReadOnlyList<ExternalEntityLinkRequest?>? ExternalLinks,
    string? Source,
    Guid? MailboxId = null);

public sealed record SmtpMailboxCredentialRequest(string? Host, int? Port, bool? UseSsl, string? Username, string? Password);
public sealed record MailgunMailboxCredentialRequest(string? Domain, string? Region, string? ApiKey);
public sealed record CreateMailboxRequest(
    string? FromAddress,
    string? DisplayName,
    string? Provider = "smtp",
    bool? IsDefault = null,
    SmtpMailboxCredentialRequest? Smtp = null,
    MailgunMailboxCredentialRequest? Mailgun = null);
public sealed record UpdateMailboxRequest(
    string? DisplayName,
    bool? IsActive,
    bool? IsDefault = null,
    string? Provider = null,
    SmtpMailboxCredentialRequest? Smtp = null,
    MailgunMailboxCredentialRequest? Mailgun = null);
public sealed record CreateSuppressionRequest(string? EmailAddress, string? Reason);

public sealed record MailboxSettingsSummary(string? Host, int? Port, bool? UseSsl, string? Username, string? Domain, string? Region);
public sealed record MailboxResponse(Guid Id, string FromAddress, string? DisplayName, DateTimeOffset CreatedAt, bool IsActive,
    string Provider, bool IsDefault, bool HasCredentials, MailboxSettingsSummary? Settings);
public sealed record MailboxSummaryResponse(Guid Id, string FromAddress, string? DisplayName);

public sealed record MessageListItem(
    Guid Id,
    string Subject,
    DateTimeOffset CreatedAt,
    int RecipientCount,
    string Status,
    string? Source,
    DateTimeOffset? ArchivedAt,
    MailboxSummaryResponse Mailbox);

public sealed record DeliveryResponse(
    Guid Id,
    string EmailAddress,
    string RecipientType,
    string Status,
    int Attempts,
    string? LastError,
    DateTimeOffset? AcceptedAt);

public sealed record ExternalEntityLinkResponse(
    Guid Id,
    string SourceSystem,
    string SourceInstance,
    string EntityType,
    string ExternalEntityId,
    string? DisplayLabel);

public sealed record MessageDetailResponse(
    Guid Id,
    Guid MailboxId,
    string Subject,
    string? TextBody,
    string? HtmlBody,
    DateTimeOffset CreatedAt,
    string? Source,
    DateTimeOffset? ArchivedAt,
    IReadOnlyList<DeliveryResponse> Deliveries,
    IReadOnlyList<ExternalEntityLinkResponse> ExternalLinks,
    MailboxSummaryResponse Mailbox);

public sealed record MessageEventResponse(
    Guid Id,
    Guid? DeliveryId,
    string EventType,
    DateTimeOffset OccurredAt,
    string? DataJson);

public sealed record SuppressionResponse(Guid Id, string EmailAddress, string? Reason, DateTimeOffset CreatedAt);

public sealed record EmailCreateResponse(Guid MessageId, string Status, string IdempotencyKey);

public sealed record ResendMessageRequest(string? Scope);

public sealed record ResendMessageResponse(Guid MessageId, string Status, string Scope, int RequeuedRecipientCount);
public sealed record CommunicationErrorResponse(CommunicationError Error);
public sealed record CommunicationError(string Code, string Message, IReadOnlyDictionary<string, string[]>? Fields = null);

internal static class CommunicationValidation
{
    internal static Dictionary<string, string[]> Validate(CreateEmailRequest? request)
    {
        var errors = new Dictionary<string, string[]>();
        if (string.IsNullOrWhiteSpace(request?.Subject) || request.Subject.Length > 998 ||
            HasEdgeWhitespace(request.Subject) || request.Subject.Any(char.IsControl))
            errors["subject"] = ["Subject is required, must be at most 998 characters, and cannot contain surrounding whitespace or control characters."];
        if (string.IsNullOrWhiteSpace(request?.TextBody) && string.IsNullOrWhiteSpace(request?.HtmlBody))
            errors["body"] = ["TextBody or HtmlBody is required."];
        if (request?.TextBody is not null && Encoding.UTF8.GetByteCount(request.TextBody) > 1024 * 1024)
            errors["textBody"] = ["TextBody must be at most 1 MiB."];
        if (request?.HtmlBody is not null && Encoding.UTF8.GetByteCount(request.HtmlBody) > 1024 * 1024)
            errors["htmlBody"] = ["HtmlBody must be at most 1 MiB."];
        ValidateRecipients(errors, "to", request?.To, true);
        ValidateRecipients(errors, "cc", request?.Cc, false);
        ValidateRecipients(errors, "bcc", request?.Bcc, false);
        var allRecipients = (request?.To ?? []).Concat(request?.Cc ?? []).Concat(request?.Bcc ?? [])
            .Where(recipient => recipient?.Email is not null).Select(recipient => EmailSuppression.Normalize(recipient!.Email!)).ToArray();
        if (allRecipients.Length != allRecipients.Distinct(StringComparer.Ordinal).Count())
            errors["recipients"] = ["A recipient may appear only once."];
        if (request?.ExternalLinks is { Count: > 100 }) errors["externalLinks"] = ["At most 100 external links are allowed."];
        if (request?.ExternalLinks is not null)
        {
            for (var index = 0; index < request.ExternalLinks.Count; index++)
            {
                var link = request.ExternalLinks[index];
                if (link is null || !ValidBoundValue(link.SourceSystem, 100) || !ValidBoundValue(link.SourceInstance, 200) ||
                    !ValidBoundValue(link.EntityType, 100) || !ValidBoundValue(link.ExternalEntityId, 500) ||
                    (link.DisplayLabel is not null && !ValidOptionalBoundValue(link.DisplayLabel, 500)))
                    errors[$"externalLinks[{index}]"] = ["SourceSystem, SourceInstance, EntityType, and ExternalEntityId are required."];
            }
        }
        if (request?.Source is not null && !ValidOptionalBoundValue(request.Source, 100))
            errors["source"] = ["Source must be at most 100 characters and cannot contain surrounding whitespace or control characters."];
        return errors;
    }

    internal static Dictionary<string, string[]> ValidateMailbox(CreateMailboxRequest? request)
    {
        var errors = new Dictionary<string, string[]>();
        if (!IsEmail(request?.FromAddress)) errors["fromAddress"] = ["A valid FromAddress is required."];
        if (request?.DisplayName is not null && !ValidOptionalBoundValue(request.DisplayName, 200)) errors["displayName"] = ["DisplayName must be at most 200 characters and cannot contain surrounding whitespace or control characters."];
        ValidateProvider(errors, request?.Provider, request?.Smtp, request?.Mailgun, true);
        return errors;
    }

    internal static Dictionary<string, string[]> ValidateMailboxUpdate(UpdateMailboxRequest? request)
    {
        var errors = new Dictionary<string, string[]>();
        if (request is null)
        {
            errors["request"] = ["A request body is required."];
            return errors;
        }
        if (request.DisplayName is not null && !ValidOptionalBoundValue(request.DisplayName, 200)) errors["displayName"] = ["DisplayName must be at most 200 characters and cannot contain surrounding whitespace or control characters."];
        ValidateProvider(errors, request.Provider, request.Smtp, request.Mailgun, false);
        return errors;
    }

    internal static string ProviderName(string? provider) => string.IsNullOrWhiteSpace(provider) ? "smtp" : provider.Trim().ToLowerInvariant();

    private static void ValidateProvider(Dictionary<string, string[]> errors, string? provider, SmtpMailboxCredentialRequest? smtp,
        MailgunMailboxCredentialRequest? mailgun, bool create)
    {
        var name = ProviderName(provider ?? (!create && mailgun is not null ? "mailgun" : "smtp"));
        if (name is not ("smtp" or "mailgun"))
        {
            errors["provider"] = ["Provider must be smtp or mailgun."];
            return;
        }
        if (smtp is not null && mailgun is not null) errors["credentials"] = ["Only the selected provider credential may be supplied."];
        if (name == "smtp")
        {
            if (mailgun is not null) errors["mailgun"] = ["Mailgun credentials require the mailgun provider."];
            if (smtp is not null && (string.IsNullOrWhiteSpace(smtp.Host) || smtp.Port is null or < 1 or > 65535))
                errors["smtp"] = ["SMTP credentials require a host and a port from 1 through 65535."];
            return;
        }
        if (smtp is not null) errors["smtp"] = ["SMTP credentials require the smtp provider."];
        if (mailgun is null || string.IsNullOrWhiteSpace(mailgun.Domain) || mailgun.Region?.Trim().ToLowerInvariant() is not ("us" or "eu") || string.IsNullOrWhiteSpace(mailgun.ApiKey))
            errors["mailgun"] = ["Mailgun credentials require a domain, region (us or eu), and API key."];
    }

    internal static Dictionary<string, string[]> ValidateSuppression(CreateSuppressionRequest? request)
    {
        var errors = new Dictionary<string, string[]>();
        if (!IsEmail(request?.EmailAddress)) errors["emailAddress"] = ["A valid email address is required."];
        if (request?.Reason is not null && !ValidOptionalBoundValue(request.Reason, 500)) errors["reason"] = ["Reason must be at most 500 characters and cannot contain surrounding whitespace or control characters."];
        return errors;
    }

    internal static bool IsEmail(string? value)
    {
        if (string.IsNullOrWhiteSpace(value) || value.Length > 320 || HasEdgeWhitespace(value) || value.Any(char.IsControl) || value.Any(char.IsWhiteSpace) ||
            value.Contains('<') || value.Contains('>') || value.Contains('"')) return false;
        return MailboxAddress.TryParse(value, out var mailbox) && mailbox is not null &&
            string.Equals(mailbox.Address, value, StringComparison.Ordinal);
    }

    private static void ValidateRecipients(Dictionary<string, string[]> errors, string name, IReadOnlyList<EmailRecipientRequest?>? recipients, bool required)
    {
        if (required && (recipients is null || recipients.Count == 0))
        {
            errors[name] = ["At least one recipient is required."];
            return;
        }
        if (recipients is { Count: > 100 }) errors[name] = ["At most 100 recipients are allowed."];
        if (recipients is null) return;
        for (var index = 0; index < recipients.Count; index++)
            if (recipients[index] is null || !IsEmail(recipients[index]!.Email)) errors[$"{name}[{index}]"] = ["A valid email address is required."];
    }

    private static bool HasEdgeWhitespace(string value) => !string.Equals(value, value.Trim(), StringComparison.Ordinal);

    private static bool ValidBoundValue(string? value, int maxLength) =>
        !string.IsNullOrEmpty(value) && value.Length <= maxLength && !HasEdgeWhitespace(value) && !value.Any(char.IsControl);

    private static bool ValidOptionalBoundValue(string value, int maxLength) =>
        value.Length <= maxLength && !HasEdgeWhitespace(value) && !value.Any(char.IsControl);
}