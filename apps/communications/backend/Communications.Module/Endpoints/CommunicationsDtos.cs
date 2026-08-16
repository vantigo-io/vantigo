using System.Text;
using System.Text.Json;

using MimeKit;

using Vantigo.Communications.Services;

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
internal sealed record CreateTagRequest(string? Name, string? Color);
internal sealed record CreateChannelRequest(
    string? Type,
    string? Address,
    string? DisplayName,
    string? Provider = "smtp",
    bool? IsDefault = null,
    SmtpChannelCredentialRequest? Smtp = null,
    MailgunChannelCredentialRequest? Mailgun = null);
internal sealed record UpdateChannelRequest(
    string? DisplayName,
    bool? IsActive,
    bool? IsDefault = null,
    string? Provider = null,
    SmtpChannelCredentialRequest? Smtp = null,
    MailgunChannelCredentialRequest? Mailgun = null);
internal sealed record SmtpChannelCredentialRequest(string? Host, int? Port, bool? UseSsl, string? Username, string? Password);
internal sealed record MailgunChannelCredentialRequest(string? Domain, string? Region, string? ApiKey, string? InboundSigningKey = null);
internal sealed record CreateSuppressionRequest(string? EmailAddress, string? Reason);
internal sealed record AiDraftRequest(string? Tone, string? Instruction);
internal sealed record AiDraftResponse(Guid InteractionId, string? Subject, string Text, bool ProductDataUsed, string Notice);
internal sealed record AiCustomerSuggestionResponse(Guid InteractionId, int? CustomerId, double? Confidence, string? Rationale, string Outcome);

internal sealed record ChannelSettingsSummary(string? Host, int? Port, bool? UseSsl, string? Username, string? Domain, string? Region);
internal sealed record ChannelResponse(Guid Id, string Type, string Address, string? DisplayName, DateTimeOffset CreatedAt,
    bool IsActive, string Provider, bool IsDefault, bool HasCredentials, ChannelSettingsSummary? Settings);
internal sealed record ParticipantResponse(Guid Id, Guid ChannelId, string Address, string? DisplayName, int? ContactId);
internal sealed record TagResponse(Guid Id, string Name, string? Color);
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
internal sealed record MessageEventResponse(Guid Id, Guid? DeliveryId, string EventType, DateTimeOffset OccurredAt, string? DataJson);
internal sealed record SuppressionResponse(Guid Id, string EmailAddress, string? Reason, DateTimeOffset CreatedAt);
internal sealed record CommunicationErrorResponse(CommunicationError Error);
internal sealed record CommunicationError(string Code, string Message, IReadOnlyDictionary<string, string[]>? Fields = null);

internal static class CommunicationValidation
{
    internal static Dictionary<string, string[]> ValidateConversation(CreateConversationRequest? request)
    {
        var errors = ValidateBody(request?.Subject, request?.TextBody, request?.HtmlBody);
        var recipients = request?.Recipients ?? [];
        if (recipients.Count == 0 && (request?.To is null || request.To.Count == 0)) errors["to"] = ["At least one recipient is required."];
        if (recipients.Count > 100) errors["recipients"] = ["At most 100 recipients are allowed."];
        for (var index = 0; index < recipients.Count; index++)
        {
            var recipient = recipients[index];
            if (recipient is null || (!recipient.ParticipantId.HasValue && !IsEmail(recipient.Address))) errors[$"recipients[{index}]"] = ["A participant id or valid channel address is required."];
        }
        ValidateRecipients(errors, "to", request?.To, request?.To is not null);
        ValidateRecipients(errors, "cc", request?.Cc, false);
        AddDuplicateRecipientError(errors, request?.To, request?.Cc);
        return errors;
    }

    internal static Dictionary<string, string[]> ValidateReply(ReplyRequest? request)
    {
        var errors = ValidateBody(request?.Subject, request?.TextBody, request?.HtmlBody, subjectRequired: false);
        if (request?.ReplyMode?.Trim().ToLowerInvariant() is not ("reply" or "reply_all"))
            errors["replyMode"] = ["ReplyMode must be reply or reply_all."];
        if (request?.AttachmentIds is { Count: > 20 }) errors["attachmentIds"] = ["At most 20 attachments are allowed."];
        if (request?.AttachmentIds is not null && request.AttachmentIds.Distinct().Count() != request.AttachmentIds.Count)
            errors["attachmentIds"] = ["An attachment may appear only once."];
        return errors;
    }

    internal static Dictionary<string, string[]> ValidateNote(NoteRequest? request)
    {
        var errors = new Dictionary<string, string[]>();
        if (string.IsNullOrWhiteSpace(request?.TextBody)) errors["textBody"] = ["TextBody is required."];
        return errors;
    }

    internal static Dictionary<string, string[]> ValidateChannel(CreateChannelRequest? request)
    {
        var errors = new Dictionary<string, string[]>();
        if (request?.Type?.Trim().ToLowerInvariant() is not "email") errors["type"] = ["Only the email channel is currently supported."];
        if (!IsEmail(request?.Address)) errors["address"] = ["A valid channel email address is required."];
        ValidateProvider(errors, request?.Provider, request?.Smtp, request?.Mailgun, true);
        return errors;
    }

    internal static Dictionary<string, string[]> ValidateChannelUpdate(UpdateChannelRequest? request)
    {
        var errors = new Dictionary<string, string[]>();
        if (request is null) { errors["request"] = ["A request body is required."]; return errors; }
        if (request.DisplayName is not null && !ValidOptional(request.DisplayName, 200)) errors["displayName"] = ["DisplayName is invalid."];
        ValidateProvider(errors, request.Provider, request.Smtp, request.Mailgun, false);
        return errors;
    }

    internal static Dictionary<string, string[]> ValidateSuppression(CreateSuppressionRequest? request)
    {
        var errors = new Dictionary<string, string[]>();
        if (!IsEmail(request?.EmailAddress)) errors["emailAddress"] = ["A valid email address is required."];
        if (request?.Reason is not null && !ValidOptional(request.Reason, 500)) errors["reason"] = ["Reason is invalid."];
        return errors;
    }

    internal static string ProviderName(string? provider) => string.IsNullOrWhiteSpace(provider) ? "smtp" : provider.Trim().ToLowerInvariant();

    internal static bool IsEmail(string? value)
    {
        if (string.IsNullOrWhiteSpace(value) || value.Length > 320 || value != value.Trim() || value.Any(char.IsControl) || value.Any(char.IsWhiteSpace) || value.Contains('<') || value.Contains('>') || value.Contains('"')) return false;
        return MailboxAddress.TryParse(value, out var mailbox) && mailbox is not null && string.Equals(mailbox.Address, value, StringComparison.Ordinal);
    }

    private static Dictionary<string, string[]> ValidateBody(string? subject, string? text, string? html, bool subjectRequired = true)
    {
        var errors = new Dictionary<string, string[]>();
        if (subjectRequired && (string.IsNullOrWhiteSpace(subject) || subject.Length > 998 || subject != subject.Trim() || subject.Any(char.IsControl)))
            errors["subject"] = ["Subject is required, must be at most 998 characters, and cannot contain surrounding whitespace or control characters."];
        if (!string.IsNullOrWhiteSpace(subject) && (subject.Length > 998 || subject != subject.Trim() || subject.Any(char.IsControl))) errors["subject"] = ["Subject is invalid."];
        if (string.IsNullOrWhiteSpace(text) && string.IsNullOrWhiteSpace(html)) errors["body"] = ["TextBody or HtmlBody is required."];
        if (text is not null && Encoding.UTF8.GetByteCount(text) > 1024 * 1024) errors["textBody"] = ["TextBody must be at most 1 MiB."];
        if (html is not null && Encoding.UTF8.GetByteCount(html) > 1024 * 1024) errors["htmlBody"] = ["HtmlBody must be at most 1 MiB."];
        return errors;
    }

    private static void ValidateRecipients(Dictionary<string, string[]> errors, string name, IReadOnlyList<EmailRecipientRequest?>? recipients, bool required)
    {
        if (required && (recipients is null || recipients.Count == 0)) { errors[name] = ["At least one recipient is required."]; return; }
        if (recipients is { Count: > 100 }) errors[name] = ["At most 100 recipients are allowed."];
        if (recipients is null) return;
        for (var index = 0; index < recipients.Count; index++) if (recipients[index] is null || !IsEmail(recipients[index]!.Email)) errors[$"{name}[{index}]"] = ["A valid email address is required."];
    }

    private static void AddDuplicateRecipientError(Dictionary<string, string[]> errors, IReadOnlyList<EmailRecipientRequest?>? to, IReadOnlyList<EmailRecipientRequest?>? cc)
    {
        var values = (to ?? []).Concat(cc ?? []).Where(item => item?.Email is not null).Select(item => EmailSuppression.Normalize(item!.Email!)).ToArray();
        if (values.Length != values.Distinct(StringComparer.Ordinal).Count()) errors["recipients"] = ["A recipient may appear only once."];
    }

    private static void ValidateProvider(Dictionary<string, string[]> errors, string? provider, SmtpChannelCredentialRequest? smtp, MailgunChannelCredentialRequest? mailgun, bool create)
    {
        var name = ProviderName(provider ?? (!create && mailgun is not null ? "mailgun" : "smtp"));
        if (name is not ("smtp" or "mailgun")) { errors["provider"] = ["Provider must be smtp or mailgun."]; return; }
        if (smtp is not null && mailgun is not null) errors["credentials"] = ["Only the selected provider credential may be supplied."];
        if (name == "smtp")
        {
            if (mailgun is not null) errors["mailgun"] = ["Mailgun credentials require the mailgun provider."];
            if (smtp is not null && (string.IsNullOrWhiteSpace(smtp.Host) || smtp.Port is null or < 1 or > 65535)) errors["smtp"] = ["SMTP credentials require a host and valid port."];
        }
        else if (smtp is not null) errors["smtp"] = ["SMTP credentials require the smtp provider."];
        else if (mailgun is null || string.IsNullOrWhiteSpace(mailgun.Domain) || mailgun.Region?.Trim().ToLowerInvariant() is not ("us" or "eu") ||
                 (create && (string.IsNullOrWhiteSpace(mailgun.ApiKey) || string.IsNullOrWhiteSpace(mailgun.InboundSigningKey))))
            errors["mailgun"] = create
                ? ["Mailgun credentials require a domain, region, API key, and inbound signing key."]
                : ["Mailgun credentials require a domain and region; omitted secrets preserve the existing protected credentials."];
    }

    private static bool ValidOptional(string value, int maxLength) => value.Length <= maxLength && value == value.Trim() && !value.Any(char.IsControl);
}