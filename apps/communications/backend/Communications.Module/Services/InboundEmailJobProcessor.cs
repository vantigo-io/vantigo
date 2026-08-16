using System.Security.Cryptography;
using System.Text.Json;

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