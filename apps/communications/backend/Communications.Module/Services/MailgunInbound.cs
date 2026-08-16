using System.Security.Cryptography;
using System.Text;
using System.Text.Json;

using Ganss.Xss;

using MailKit;

using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.Options;

using MimeKit;

using Vantigo.Communications.Database.Communications;
using Vantigo.Communications.Infrastructure.Storage;
using Vantigo.Storage.Abstractions;
using Vantigo.Tenancy;
using Vantigo.Tenancy.Abstractions;

namespace Vantigo.Communications.Services;

public sealed class MailgunInboundOptions
{
    public int TimestampToleranceSeconds { get; set; } = 300;
    public int MaxFutureSkewSeconds { get; set; } = 300;
    public int MaxPastSkewSeconds { get; set; } = 300;
    public int MaxRequestBytes { get; set; } = 25 * 1024 * 1024;
    public int MaxMimeBytes { get; set; } = 25 * 1024 * 1024;
    public int MaxAttachmentBytes { get; set; } = 10 * 1024 * 1024;
    public int MaxAggregateAttachmentBytes { get; set; } = 10 * 1024 * 1024;
    public int MaxAttachments { get; set; } = 20;
    public int MaxBodyBytes { get; set; } = 8 * 1024 * 1024;
    public int MaxFormKeys { get; set; } = 100;
    public int MaxHeaderBytes { get; set; } = 512 * 1024;
    public int MaxAttachedMessageParts { get; set; } = 0;
    public int LeaseSeconds { get; set; } = 120;
    public int ClaimAttempts { get; set; } = 10;
    public int MaxAttempts { get; set; } = 8;
    public int PollSeconds { get; set; } = 5;
}

internal static class MailgunSignatureVerifier
{
    internal static bool Verify(string? timestamp, string? token, string? signature, string signingKey,
        DateTimeOffset now, TimeSpan pastSkew, TimeSpan futureSkew)
    {
        if (string.IsNullOrWhiteSpace(timestamp) || timestamp.Length > 32 ||
            string.IsNullOrWhiteSpace(token) || token.Length > 256 || token.Any(char.IsControl) ||
            string.IsNullOrWhiteSpace(signature) || signature.Length != 64 || string.IsNullOrWhiteSpace(signingKey))
            return false;
        if (!long.TryParse(timestamp, System.Globalization.NumberStyles.None, System.Globalization.CultureInfo.InvariantCulture, out var seconds))
            return false;

        DateTimeOffset issuedAt;
        try { issuedAt = DateTimeOffset.FromUnixTimeSeconds(seconds); }
        catch (ArgumentOutOfRangeException) { return false; }
        if (issuedAt < now.Subtract(pastSkew) || issuedAt > now.Add(futureSkew)) return false;

        byte[] supplied;
        try { supplied = Convert.FromHexString(signature); }
        catch (FormatException) { return false; }
        using var hmac = new HMACSHA256(Encoding.UTF8.GetBytes(signingKey));
        var expected = hmac.ComputeHash(Encoding.ASCII.GetBytes(timestamp + token));
        return CryptographicOperations.FixedTimeEquals(expected, supplied);
    }
}

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

internal sealed record MailgunFormAttachment(string FileName, string ContentType, byte[] Bytes);

internal static class MailgunInboundMimeBuilder
{
    internal static byte[] CreateSynthetic(MailgunInboundForm form, DateTimeOffset now)
    {
        var message = new MimeMessage();
        AddHeaders(message, form.Headers);
        AddMailboxes(message.Cc, form.Cc);
        if (ParseHeaders(form.Headers).TryGetValue("Cc", out var headerCc)) AddMailboxes(message.Cc, headerCc);
        if (message.From.Count == 0 && TryMailbox(form.From ?? form.Sender, out var from)) message.From.Add(from!);
        if (message.To.Count == 0 && TryMailbox(form.Recipient, out var to)) message.To.Add(to!);
        if (message.Subject is null)
        {
            var subject = Clean(form.Subject, 998);
            if (subject is not null) message.Subject = subject;
        }
        if (message.Date == DateTimeOffset.MinValue) message.Date = now;

        var builder = new BodyBuilder
        {
            TextBody = form.Text,
            HtmlBody = form.Html,
        };
        foreach (var attachment in form.Attachments)
        {
            var item = builder.Attachments.Add(attachment.FileName, attachment.Bytes, ParseContentType(attachment.ContentType));
            item.ContentDisposition ??= new ContentDisposition(ContentDisposition.Attachment);
        }
        message.Body = builder.ToMessageBody();
        using var stream = new MemoryStream();
        message.WriteTo(stream);
        return stream.ToArray();
    }

    internal static IReadOnlyDictionary<string, string> ParseHeaders(string? encoded)
    {
        var headers = new Dictionary<string, string>(StringComparer.OrdinalIgnoreCase);
        if (string.IsNullOrWhiteSpace(encoded) || encoded.Length > 512 * 1024) return headers;
        try
        {
            using var document = JsonDocument.Parse(encoded);
            if (document.RootElement.ValueKind == JsonValueKind.Object)
            {
                foreach (var property in document.RootElement.EnumerateObject()) headers[property.Name] = property.Value.ToString();
            }
            else if (document.RootElement.ValueKind == JsonValueKind.Array)
            {
                foreach (var item in document.RootElement.EnumerateArray())
                {
                    if (item.ValueKind == JsonValueKind.Array && item.GetArrayLength() >= 2 && item[0].GetString() is { } pairName)
                        headers[pairName] = item[1].GetString() ?? string.Empty;
                    else if (item.ValueKind == JsonValueKind.Object && item.TryGetProperty("name", out var name) && item.TryGetProperty("value", out var value) && name.GetString() is { } objectName)
                        headers[objectName] = value.GetString() ?? string.Empty;
                }
            }
        }
        catch (JsonException) { }
        return headers;
    }

    private static void AddHeaders(MimeMessage message, string? encoded)
    {
        foreach (var header in ParseHeaders(encoded))
        {
            AddHeader(message, header.Key, header.Value);
        }
    }

    private static void AddHeader(MimeMessage message, string? name, string? value)
    {
        if (string.IsNullOrWhiteSpace(name) || string.IsNullOrWhiteSpace(value) || name.Length > 200 || value.Length > 20_000 || name.Any(char.IsControl) || value.Any(char.IsControl)) return;
        try
        {
            if (name.Equals("From", StringComparison.OrdinalIgnoreCase) || name.Equals("To", StringComparison.OrdinalIgnoreCase) ||
                name.Equals("Cc", StringComparison.OrdinalIgnoreCase) || name.Equals("Bcc", StringComparison.OrdinalIgnoreCase) ||
                name.Equals("Subject", StringComparison.OrdinalIgnoreCase) || name.Equals("Date", StringComparison.OrdinalIgnoreCase))
                return;
            message.Headers.Add(name, value);
        }
        catch (ParseException) { }
    }

    private static void AddMailboxes(InternetAddressList target, string? value)
    {
        if (string.IsNullOrWhiteSpace(value) || value.Length > 998) return;
        if (value.Any(char.IsControl)) return;
        try
        {
            if (!InternetAddressList.TryParse(value, out var addresses) || addresses is null) return;
            foreach (var mailbox in addresses.Mailboxes.Take(100))
            {
                if (mailbox.Address.Length <= 320 && mailbox.Address.Contains('@') && !mailbox.Address.Any(char.IsControl) &&
                    MailboxAddress.TryParse(mailbox.Address, out var validMailbox) && validMailbox is not null)
                    target.Add(new MailboxAddress(mailbox.Name, validMailbox.Address));
            }
        }
        catch (ParseException) { }
    }

    private static bool TryMailbox(string? value, out MailboxAddress? mailbox)
    {
        mailbox = null;
        if (string.IsNullOrWhiteSpace(value) || value.Length > 998) return false;
        try { mailbox = MailboxAddress.Parse(value); return true; }
        catch (ParseException) { return false; }
    }

    private static ContentType ParseContentType(string value)
    {
        try { return ContentType.Parse(string.IsNullOrWhiteSpace(value) ? "application/octet-stream" : value); }
        catch (ParseException) { return new ContentType("application", "octet-stream"); }
    }

    private static string? Clean(string? value, int max) => string.IsNullOrWhiteSpace(value) ? null : value.Trim()[..Math.Min(value.Trim().Length, max)];
}

internal static class InboundHtmlSanitizer
{
    internal static string? Sanitize(string? html, int maxCharacters)
    {
        if (string.IsNullOrWhiteSpace(html)) return null;
        var sanitizer = new HtmlSanitizer();
        foreach (var tag in new[] { "script", "style", "form", "input", "button", "textarea", "select", "option", "iframe", "frame", "frameset", "object", "embed", "svg", "math", "meta", "base", "link", "img" })
            sanitizer.AllowedTags.Remove(tag);
        sanitizer.AllowedAttributes.Remove("style");
        sanitizer.AllowedAttributes.Remove("src");
        sanitizer.AllowedAttributes.Remove("srcset");
        sanitizer.AllowedSchemes.Clear();
        sanitizer.AllowedSchemes.Add("http");
        sanitizer.AllowedSchemes.Add("https");
        sanitizer.AllowedSchemes.Add("mailto");
        var clean = sanitizer.Sanitize(html);
        return clean.Length <= maxCharacters ? clean : clean[..maxCharacters];
    }
}

internal sealed class InboundEmailJobProcessor(
    CommunicationsDbContext db,
    ITenantDirectory tenantDirectory,
    IObjectStore<CommunicationsStorageScope> objectStore,
    IThreadResolver threadResolver,
    IConversationContactLinker contactLinker,
    IOptions<MailgunInboundOptions> options,
    ILogger<InboundEmailJobProcessor> logger)
{
    private readonly MailgunInboundOptions inbound = options.Value;

    public Task<bool> ProcessOneAsync(CancellationToken cancellationToken) => ProcessOneAsync(null, cancellationToken);

    public async Task<bool> ProcessOneAsync(Guid? onlyJobId, CancellationToken cancellationToken)
    {
        // System-context discovery; tenant scope is entered before processing.
        // The active tenant directory bounds the discovery pass.
        foreach (var tenant in await tenantDirectory.GetActiveTenantsAsync(cancellationToken))
        {
            try
            {
                db.ChangeTracker.Clear();
                using var tenantScope = AmbientTenantContext.Enter(tenant);
                if (await ProcessOneForTenantAsync(tenant, onlyJobId, cancellationToken)) return true;
            }
            catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested) { throw; }
            catch (Exception exception)
            {
                logger.LogError(exception, "Communications inbound email processing failed for tenant {TenantId}; continuing with the next tenant.", tenant.Value);
            }
            finally { db.ChangeTracker.Clear(); }
        }

        return false;
    }

    private async Task<bool> ProcessOneForTenantAsync(TenantId tenant, Guid? onlyJobId, CancellationToken cancellationToken)
    {
        var leaseId = Guid.NewGuid().ToString("N");
        InboundEmailJob? job = null;
        await using (var transaction = await db.Database.BeginTransactionAsync(cancellationToken))
        {
            var now = DateTimeOffset.UtcNow;
            var leaseUntil = now.AddSeconds(Math.Max(10, inbound.LeaseSeconds));
            for (var attempt = 0; attempt < Math.Max(3, inbound.ClaimAttempts) && job is null; attempt++)
            {
                var candidate = await db.InboundEmailJobs.AsNoTracking()
                    .Where(item => (item.Status == "pending" || item.Status == "retry") && item.NextAttemptAt <= now || item.Status == "processing" && item.LeaseUntil < now)
                    .Where(item => item.TenantId == tenant.Value)
                    .Where(item => !onlyJobId.HasValue || item.Id == onlyJobId.Value)
                    .OrderBy(item => item.NextAttemptAt).FirstOrDefaultAsync(cancellationToken);
                if (candidate is null) break;
                var claimed = await db.InboundEmailJobs.Where(item => item.Id == candidate.Id && item.TenantId == tenant.Value &&
                    ((item.Status == "pending" || item.Status == "retry") && item.NextAttemptAt <= now || item.Status == "processing" && item.LeaseUntil < now))
                    .ExecuteUpdateAsync(setters => setters.SetProperty(item => item.Status, "processing").SetProperty(item => item.LeaseId, leaseId)
                        .SetProperty(item => item.LeaseUntil, leaseUntil).SetProperty(item => item.Attempts, item => item.Attempts + 1), cancellationToken);
                if (claimed == 0) continue;
                db.ChangeTracker.Clear();
                job = await db.InboundEmailJobs.Include(item => item.InboundReceipt).SingleAsync(item => item.Id == candidate.Id, cancellationToken);
            }
            if (job is null) return false;
            await transaction.CommitAsync(cancellationToken);
        }

        try
        {
            db.ChangeTracker.Clear();
            var claimedJobId = job.Id;
            job = await db.InboundEmailJobs.Include(item => item.InboundReceipt).SingleAsync(item => item.Id == claimedJobId, cancellationToken);
            var receipt = job.InboundReceipt ?? throw new InvalidOperationException("Inbound receipt is missing.");
            if (receipt.ConversationMessageId.HasValue)
            {
                await RetryContactLinkAsync(receipt.ConversationMessageId.Value, cancellationToken);
                await CompleteAsync(job.Id, leaseId, cancellationToken);
                return true;
            }

            await using var raw = await objectStore.GetAsync(job.RawMimeStorageKey, cancellationToken) ?? throw new InvalidOperationException("Inbound payload is unavailable.");
            if (raw.CanSeek && raw.Length > inbound.MaxMimeBytes) throw new InvalidOperationException("Inbound payload exceeds the configured limit.");
            using var bounded = new MemoryStream();
            await raw.CopyToAsync(bounded, cancellationToken);
            if (bounded.Length > inbound.MaxMimeBytes) throw new InvalidOperationException("Inbound payload exceeds the configured limit.");
            bounded.Position = 0;
            var mime = await MimeMessage.LoadAsync(bounded, cancellationToken);
            if (mime.Body is MessagePart || mime.BodyParts.OfType<MessagePart>().Count() > inbound.MaxAttachedMessageParts)
                throw new InvalidOperationException("Attached messages are not accepted.");
            var normalized = Normalize(mime, job.ReceivedAt, job.EnvelopeSenderAddress, job.EnvelopeRecipientAddress);
            var conversationId = await threadResolver.ResolveAsync(job.ChannelId, normalized, cancellationToken);
            await PersistInboundAsync(job, receipt, mime, normalized, conversationId, leaseId, cancellationToken);
            return true;
        }
        catch (Exception exception) when (!cancellationToken.IsCancellationRequested)
        {
            logger.LogError(exception, "Communications inbound email processing failed.");
            if (job is not null) await MarkFailedAsync(job.Id, leaseId, cancellationToken);
            return true;
        }
    }

    private NormalizedInboundMessage Normalize(MimeMessage message, DateTimeOffset receivedAt, string? envelopeSender, string? envelopeRecipient)
    {
        var from = message.From.Mailboxes.FirstOrDefault();
        var sender = from?.Address;
        if (!IsAddress(sender ?? string.Empty)) sender = envelopeSender;
        if (string.IsNullOrWhiteSpace(sender)) throw new InvalidOperationException("Inbound message has no reliable sender.");
        var text = message.TextBody;
        var html = InboundHtmlSanitizer.Sanitize(message.HtmlBody, inbound.MaxBodyBytes);
        if (string.IsNullOrWhiteSpace(text) && !string.IsNullOrWhiteSpace(html)) text = HtmlToText(html);
        var attachments = message.Attachments.OfType<MimePart>().Select(part => new NormalizedInboundAttachment(
            NormalizeContentId(part.ContentId), SafeFileName(part.FileName), part.ContentType.MimeType,
            ReadPart(part))).ToArray();
        if (attachments.Any(item => item.Bytes.Length > inbound.MaxAttachmentBytes) || attachments.Length > inbound.MaxAttachments)
            throw new InvalidOperationException("Inbound attachment limits exceeded.");
        if (attachments.Sum(item => (long)item.Bytes.Length) > inbound.MaxAggregateAttachmentBytes)
            throw new InvalidOperationException("Inbound aggregate attachment limits exceeded.");
        var occurred = message.Date > DateTimeOffset.UnixEpoch && message.Date < DateTimeOffset.UtcNow.AddDays(1) ? message.Date : receivedAt;
        var to = message.To.Mailboxes.Select(item => item.Address).Where(IsAddress).Select(EmailSuppression.Normalize).Distinct().Take(100).ToArray();
        if (to.Length == 0 && IsAddress(envelopeRecipient ?? string.Empty)) to = [EmailSuppression.Normalize(envelopeRecipient!)];
        return new NormalizedInboundMessage(sender.Trim().ToLowerInvariant(), CleanDisplayName(from?.Name),
            to,
            message.Cc.Mailboxes.Select(item => item.Address).Where(IsAddress).Select(EmailSuppression.Normalize).Distinct().Take(100).ToArray(),
            CleanSubject(message.Subject), text, html, NormalizeHeader(message.MessageId), NormalizeHeader(message.InReplyTo),
            message.References.Select(EmailThreadResolver.NormalizeMessageId).Where(item => item.Length > 0).Distinct().Take(50).ToArray(),
            attachments, occurred, null, null);
    }

    private async Task PersistInboundAsync(InboundEmailJob job, InboundReceipt receipt, MimeMessage mime, NormalizedInboundMessage inboundMessage,
        Guid? conversationId, string leaseId, CancellationToken cancellationToken)
    {
        var attachmentPlans = inboundMessage.Attachments.Select((attachment, index) =>
        {
            var attachmentId = ObjectOwnershipLifecycle.DeterministicGuid(job.Id, $"attachment:{index}:{Convert.ToHexString(SHA256.HashData(attachment.Bytes))}");
            return new InboundAttachmentPlan(attachment, attachmentId, $"attachments/inbound/{job.Id:N}/{index:D4}-{attachmentId:N}");
        }).ToArray();

        // Reservations are committed before any attachment PutAsync. A crash
        // before ownership therefore leaves a cleanup record for the worker.
        var reservationNow = DateTimeOffset.UtcNow;
        foreach (var plan in attachmentPlans)
            await ObjectOwnershipLifecycle.ReserveAsync(db, plan.StorageKey, reservationNow, cancellationToken);
        await db.SaveChangesAsync(cancellationToken);

        await using var transaction = await db.Database.BeginTransactionAsync(cancellationToken);
        var owner = await db.InboundEmailJobs.Where(item => item.Id == job.Id && item.Status == "processing" && item.LeaseId == leaseId)
            .ExecuteUpdateAsync(setters => setters.SetProperty(item => item.LeaseUntil, DateTimeOffset.UtcNow.AddSeconds(Math.Max(10, inbound.LeaseSeconds))), cancellationToken);
        if (owner == 0) return;
        var existingReceipt = await db.InboundReceipts.SingleAsync(item => item.Id == receipt.Id, cancellationToken);
        if (existingReceipt.ConversationMessageId.HasValue)
        {
            await transaction.CommitAsync(cancellationToken);
            await CompleteAsync(job.Id, leaseId, cancellationToken);
            return;
        }
        if (!string.IsNullOrWhiteSpace(inboundMessage.RfcMessageId))
        {
            var duplicate = await db.InboundReceipts.AsNoTracking().Where(item => item.ChannelId == job.ChannelId && item.Provider == "mailgun" && item.RfcMessageId == inboundMessage.RfcMessageId && item.Id != receipt.Id).Select(item => item.ConversationMessageId).FirstOrDefaultAsync(cancellationToken);
            if (duplicate.HasValue)
            {
                existingReceipt.ConversationMessageId = duplicate.Value;
                await db.SaveChangesAsync(cancellationToken);
                await transaction.CommitAsync(cancellationToken);
                await CompleteAsync(job.Id, leaseId, cancellationToken);
                return;
            }
        }

        var now = DateTimeOffset.UtcNow;
        var conversation = conversationId.HasValue
            ? await db.Conversations.Include(item => item.Participants).ThenInclude(item => item.Participant).SingleAsync(item => item.Id == conversationId.Value, cancellationToken)
            : new Conversation { Id = Guid.NewGuid(), ChannelId = job.ChannelId, Subject = inboundMessage.Subject, Status = "open", LastActivityAt = inboundMessage.OccurredAt, PreviewText = Preview(inboundMessage.Text ?? inboundMessage.Html), CreatedAt = now };
        if (conversationId is null) db.Conversations.Add(conversation);
        conversation.Subject ??= inboundMessage.Subject;
        conversation.LastActivityAt = Max(conversation.LastActivityAt, inboundMessage.OccurredAt);
        conversation.PreviewText = Preview(inboundMessage.Text ?? inboundMessage.Html);

        var sender = await GetParticipantAsync(job.ChannelId, inboundMessage.FromAddress, inboundMessage.FromDisplayName, now, cancellationToken);
        var message = new ConversationMessage
        {
            Id = Guid.NewGuid(),
            ConversationId = conversation.Id,
            Direction = "inbound",
            ParticipantId = sender.Id,
            Participant = sender,
            Subject = inboundMessage.Subject,
            TextBody = inboundMessage.Text,
            HtmlBody = inboundMessage.Html,
            RawPayloadStorageKey = job.RawMimeStorageKey,
            ChannelMetadataJson = JsonSerializer.Serialize(new EmailThreadMetadata(inboundMessage.RfcMessageId, inboundMessage.InReplyTo, inboundMessage.References, inboundMessage.To, inboundMessage.Cc), SmtpDeliveryProvider.JsonOptions),
            RfcMessageId = inboundMessage.RfcMessageId,
            OccurredAt = inboundMessage.OccurredAt,
            CreatedAt = now,
        };
        if (!conversation.Participants.Any(item => item.ParticipantId == sender.Id)) conversation.Participants.Add(new ConversationParticipant { ConversationId = conversation.Id, ParticipantId = sender.Id, Participant = sender, Role = "sender" });
        foreach (var address in inboundMessage.To.Concat(inboundMessage.Cc).Distinct(StringComparer.OrdinalIgnoreCase))
        {
            var participant = await GetParticipantAsync(job.ChannelId, address, null, now, cancellationToken);
            if (!conversation.Participants.Any(item => item.ParticipantId == participant.Id)) conversation.Participants.Add(new ConversationParticipant { ConversationId = conversation.Id, ParticipantId = participant.Id, Participant = participant });
        }
        foreach (var plan in attachmentPlans)
        {
            await using var content = new MemoryStream(plan.Attachment.Bytes, writable: false);
            await objectStore.PutAsync(plan.StorageKey, content, plan.Attachment.ContentType, cancellationToken);
            message.Attachments.Add(new MessageAttachment { Id = plan.AttachmentId, MessageId = message.Id, FileName = plan.Attachment.FileName, ContentType = plan.Attachment.ContentType, SizeBytes = plan.Attachment.Bytes.LongLength, ContentHash = Convert.ToHexString(SHA256.HashData(plan.Attachment.Bytes)).ToLowerInvariant(), ContentId = plan.Attachment.ContentId, StorageKey = plan.StorageKey, ScanStatus = "pending", IsInline = plan.Attachment.ContentId is not null, CreatedAt = now });
        }
        db.ConversationMessages.Add(message);
        existingReceipt.ConversationMessageId = message.Id;
        existingReceipt.Status = "completed";
        var fenced = await db.InboundEmailJobs.Where(item => item.Id == job.Id && item.Status == "processing" && item.LeaseId == leaseId)
            .ExecuteUpdateAsync(setters => setters.SetProperty(item => item.Status, "completed")
                .SetProperty(item => item.CompletedAt, now).SetProperty(item => item.LeaseId, (string?)null)
                .SetProperty(item => item.LeaseUntil, (DateTimeOffset?)null), cancellationToken);
        if (fenced == 0) { await transaction.RollbackAsync(cancellationToken); return; }
        await ObjectOwnershipLifecycle.MarkOwnedAsync(db, attachmentPlans.Select(item => item.StorageKey), cancellationToken);
        await db.SaveChangesAsync(cancellationToken);
        await transaction.CommitAsync(cancellationToken);

        try { await contactLinker.LinkAsync(conversation, sender, cancellationToken); }
        catch (Exception exception) when (!cancellationToken.IsCancellationRequested) { logger.LogWarning(exception, "Inbound contact association will be retried for message {MessageId}.", message.Id); throw; }
    }

    private async Task RetryContactLinkAsync(Guid messageId, CancellationToken cancellationToken)
    {
        var message = await db.ConversationMessages.Include(item => item.Conversation).Include(item => item.Participant).SingleAsync(item => item.Id == messageId, cancellationToken);
        if (message.Conversation is not null && message.Participant is not null) await contactLinker.LinkAsync(message.Conversation, message.Participant, cancellationToken);
    }

    private async Task<Participant> GetParticipantAsync(Guid channelId, string address, string? displayName, DateTimeOffset now, CancellationToken cancellationToken)
    {
        var normalized = EmailSuppression.Normalize(address);
        var participant = await db.Participants.SingleOrDefaultAsync(item => item.ChannelId == channelId && item.Address == normalized, cancellationToken);
        if (participant is not null) { participant.DisplayName ??= CleanDisplayName(displayName); return participant; }
        participant = new Participant { Id = Guid.NewGuid(), ChannelId = channelId, Address = normalized, DisplayName = CleanDisplayName(displayName), CreatedAt = now };
        db.Participants.Add(participant);
        return participant;
    }

    private async Task CompleteAsync(Guid jobId, string leaseId, CancellationToken cancellationToken)
    {
        db.ChangeTracker.Clear();
        var job = await db.InboundEmailJobs.SingleOrDefaultAsync(item => item.Id == jobId, cancellationToken);
        if (job is null || job.LeaseId != leaseId) return;
        job.Status = "completed"; job.CompletedAt = DateTimeOffset.UtcNow; job.LeaseId = null; job.LeaseUntil = null; job.LastError = null;
        await db.SaveChangesAsync(cancellationToken);
    }

    private async Task MarkFailedAsync(Guid jobId, string leaseId, CancellationToken cancellationToken)
    {
        db.ChangeTracker.Clear();
        var job = await db.InboundEmailJobs.SingleOrDefaultAsync(item => item.Id == jobId, cancellationToken);
        if (job is null || job.LeaseId != leaseId) return;
        var terminal = job.Attempts >= Math.Max(1, inbound.MaxAttempts);
        job.Status = terminal ? "failed" : "retry";
        job.LastError = "Inbound email processing failed.";
        job.NextAttemptAt = DateTimeOffset.UtcNow.AddSeconds(Math.Min(3600, Math.Pow(2, Math.Min(job.Attempts, 10))));
        job.LeaseId = null; job.LeaseUntil = null;
        await db.SaveChangesAsync(cancellationToken);
    }

    private static byte[] ReadPart(MimePart part)
    {
        using var stream = new MemoryStream();
        if (part.Content is null) throw new InvalidOperationException("MIME part has no content.");
        part.Content.DecodeTo(stream);
        return stream.ToArray();
    }
    private static string? NormalizeHeader(string? value) => string.IsNullOrWhiteSpace(value) ? null : EmailThreadResolver.NormalizeMessageId(value);
    private static string? NormalizeContentId(string? value) => string.IsNullOrWhiteSpace(value) ? null : value.Trim().Trim('<', '>').ToLowerInvariant();
    private static string? CleanSubject(string? value) => string.IsNullOrWhiteSpace(value) ? null : value.Trim()[..Math.Min(value.Trim().Length, 998)];
    private static string? CleanDisplayName(string? value) => string.IsNullOrWhiteSpace(value) ? null : value.Trim()[..Math.Min(value.Trim().Length, 200)];
    private static string SafeFileName(string? value) { var name = Path.GetFileName(value ?? "attachment").Trim(); return string.IsNullOrWhiteSpace(name) ? "attachment" : name.Length <= 500 ? name : name[..500]; }
    private static bool IsAddress(string value) => !string.IsNullOrWhiteSpace(value) && value.Length <= 320 && MailboxAddress.TryParse(value, out _);
    private static string? HtmlToText(string html) => System.Text.RegularExpressions.Regex.Replace(html, "<[^>]+>", " ").Trim();
    private static string? Preview(string? value) => string.IsNullOrWhiteSpace(value) ? null : value.Length <= 500 ? value : value[..500];
    private static DateTimeOffset Max(DateTimeOffset left, DateTimeOffset right) => left >= right ? left : right;

    private sealed record InboundAttachmentPlan(NormalizedInboundAttachment Attachment, Guid AttachmentId, string StorageKey);
}

public sealed class CommunicationsInboundWorker(IServiceScopeFactory scopeFactory, ILogger<CommunicationsInboundWorker> logger, IOptions<MailgunInboundOptions> options) : BackgroundService
{
    protected override async Task ExecuteAsync(CancellationToken stoppingToken)
    {
        var delay = TimeSpan.FromSeconds(Math.Max(1, options.Value.PollSeconds));
        while (!stoppingToken.IsCancellationRequested)
        {
            try
            {
                await using var scope = scopeFactory.CreateAsyncScope();
                var processor = scope.ServiceProvider.GetRequiredService<InboundEmailJobProcessor>();
                while (await processor.ProcessOneAsync(stoppingToken)) { }
            }
            catch (OperationCanceledException) when (stoppingToken.IsCancellationRequested) { }
            catch (Exception exception) { logger.LogError(exception, "Communications inbound email processing failed."); }
            await Task.Delay(delay, stoppingToken);
        }
    }
}