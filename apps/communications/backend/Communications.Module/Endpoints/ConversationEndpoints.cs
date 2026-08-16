using System.Security.Cryptography;
using System.Text.Json;

using Microsoft.AspNetCore.Antiforgery;
using Microsoft.AspNetCore.Http.Features;
using Microsoft.AspNetCore.Mvc;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.Options;

using Vantigo.Communications.Authorization;
using Vantigo.Communications.Database.Communications;
using Vantigo.Communications.Infrastructure.Storage;
using Vantigo.Communications.Services;
using Vantigo.Contracts;
using Vantigo.Contracts.AspNetCore.Authorization;
using Vantigo.Storage.Abstractions;

using static Vantigo.Communications.Endpoints.CommunicationEndpointHelpers;

namespace Vantigo.Communications.Endpoints;

internal static class ConversationEndpoints
{
    internal static void MapConversationEndpoints(this IEndpointRouteBuilder api)
    {
        api.MapGet("/conversations", ListConversations).RequirePermission(CommunicationsPermissions.ConversationsView);
        api.MapGet("/conversations/{id:guid}", GetConversation).RequirePermission(CommunicationsPermissions.ConversationsView);
        api.MapPost("/conversations/{id:guid}/read", MarkRead).RequirePermission(CommunicationsPermissions.ConversationsView);
        api.MapPost("/conversations/{id:guid}/reply", Reply).RequirePermission(CommunicationsPermissions.ConversationsReply).RequirePermission(CommunicationsPermissions.ConversationsView);
        api.MapPost("/conversations/{id:guid}/attachments", StageAttachment).RequirePermission(CommunicationsPermissions.ConversationsReply).RequirePermission(CommunicationsPermissions.ConversationsView);
        api.MapGet("/conversations/{conversationId:guid}/attachments/{attachmentId:guid}", GetAttachmentUploadStatus).RequirePermission(CommunicationsPermissions.ConversationsView);
        api.MapGet("/attachments/{id:guid}/download", DownloadAttachment).RequirePermission(CommunicationsPermissions.ConversationsView);
    }

    internal static void MapConversationMutationEndpoints(this IEndpointRouteBuilder api)
    {
        api.MapPost("/conversations", CreateConversation).RequirePermission(CommunicationsPermissions.ConversationsReply);
        api.MapPost("/conversations/{id:guid}/notes", AddNote).RequirePermission(CommunicationsPermissions.ConversationsManage).RequirePermission(CommunicationsPermissions.ConversationsView);
        api.MapPatch("/conversations/{id:guid}", UpdateConversation).RequirePermission(CommunicationsPermissions.ConversationsManage).RequirePermission(CommunicationsPermissions.ConversationsView);
    }

    private static async Task<IResult> ListConversations(int? page, int? pageSize, string? status, Guid? assignedUserId, Guid? tagId,
        int? customerId, bool? unreadOnly, HttpContext http, CommunicationsDbContext db, CancellationToken ct)
    {
        var (currentPage, size) = PageValues(page, pageSize);
        var userId = CurrentUserId(http);
        var query = db.Conversations.AsNoTracking()
            .Include(item => item.Tags).ThenInclude(item => item.Tag)
            .Include(item => item.CustomerCandidates)
            .Include(item => item.Participants).ThenInclude(item => item.Participant).AsQueryable();
        if (!string.IsNullOrWhiteSpace(status)) query = query.Where(item => item.Status == status.Trim().ToLowerInvariant());
        if (assignedUserId.HasValue) query = query.Where(item => item.AssignedUserId == assignedUserId);
        if (customerId.HasValue) query = query.Where(item => item.CustomerId == customerId);
        if (tagId.HasValue) query = query.Where(item => item.Tags.Any(tag => tag.TagId == tagId));
        if (unreadOnly == true && userId.HasValue) query = query.Where(item => !item.ReadStates.Any(state => state.UserId == userId && state.LastReadAt >= item.LastActivityAt));
        var total = await query.CountAsync(ct);
        var conversations = await query.OrderByDescending(item => item.LastActivityAt).Skip((currentPage - 1) * size).Take(size).ToListAsync(ct);
        var readAt = userId.HasValue ? await db.ConversationReadStates.AsNoTracking().Where(item => conversations.Select(conversation => conversation.Id).Contains(item.ConversationId) && item.UserId == userId).ToDictionaryAsync(item => item.ConversationId, item => item.LastReadAt, ct) : [];
        var result = conversations.Select(item => new ConversationListItem(item.Id, item.ChannelId, item.Subject, item.Status, item.AssignedUserId, item.CustomerId,
            item.CustomerAssociationSource, item.SuggestedCustomerId, item.CustomerCandidates.OrderBy(candidate => candidate.CustomerId).Select(candidate => candidate.CustomerId).ToArray(),
            item.LastActivityAt, item.PreviewText, Participants(item.Participants), !readAt.TryGetValue(item.Id, out var lastRead) || lastRead < item.LastActivityAt, Tags(item.Tags))).ToArray();
        return TypedResults.Ok(PaginatedResponse<ConversationListItem>.Create(result, currentPage, size, total));
    }

    private static async Task<IResult> GetConversation(Guid id, HttpContext http, CommunicationsDbContext db, CancellationToken ct)
    {
        var item = await db.Conversations.AsNoTracking().Include(conversation => conversation.Channel).Include(conversation => conversation.Messages).ThenInclude(message => message.Participant)
            .Include(conversation => conversation.Messages).ThenInclude(message => message.Attachments)
            .Include(conversation => conversation.Messages).ThenInclude(message => message.Deliveries)
            .Include(conversation => conversation.Participants).ThenInclude(link => link.Participant)
            .Include(conversation => conversation.CustomerCandidates)
            .Include(conversation => conversation.Tags).ThenInclude(tag => tag.Tag)
            .SingleOrDefaultAsync(conversation => conversation.Id == id, ct);
        if (item is null) return TypedResults.NotFound();
        var userId = CurrentUserId(http);
        var lastRead = userId.HasValue ? await db.ConversationReadStates.AsNoTracking().Where(state => state.ConversationId == id && state.UserId == userId).Select(state => (DateTimeOffset?)state.LastReadAt).SingleOrDefaultAsync(ct) : null;
        return TypedResults.Ok(new ConversationDetailResponse(item.Id, item.ChannelId, item.Subject, item.Status, item.AssignedUserId, item.CustomerId,
            item.CustomerAssociationSource, item.SuggestedCustomerId, item.SuggestedCustomerConfidence, item.SuggestedCustomerReasoning,
            item.CustomerCandidates.OrderBy(candidate => candidate.CustomerId).Select(candidate => candidate.CustomerId).ToArray(), item.LastActivityAt, item.PreviewText, item.CreatedAt,
            item.Messages.OrderBy(message => message.OccurredAt).Select(message => ToMessage(message, http)).ToArray(), Participants(item.Participants), Tags(item.Tags), lastRead,
            ReplyRecipients(item)));
    }

    private static async Task<IResult> MarkRead(Guid id, HttpContext http, CommunicationsDbContext db, CancellationToken ct)
    {
        var userId = CurrentUserId(http);
        if (!userId.HasValue) return TypedResults.Unauthorized();
        if (!await db.Conversations.AnyAsync(item => item.Id == id, ct)) return TypedResults.NotFound();
        var state = await db.ConversationReadStates.FindAsync([id, userId.Value], ct);
        if (state is null) db.ConversationReadStates.Add(new ConversationReadState { ConversationId = id, UserId = userId.Value, LastReadAt = DateTimeOffset.UtcNow });
        else state.LastReadAt = DateTimeOffset.UtcNow;
        await db.SaveChangesAsync(ct);
        return TypedResults.Ok();
    }

    private static async Task<IResult> Reply(Guid id, ReplyRequest? request, HttpContext http, IAntiforgery antiforgery, CommunicationsDbContext db, CancellationToken ct)
    {
        var csrf = await ValidateAntiforgery(http, antiforgery); if (csrf is not null) return csrf;
        var validation = CommunicationValidation.ValidateReply(request); if (validation.Count > 0) return ValidationError(validation);
        return await QueueOutboundAsync(id, request!, http, db, ct, true);
    }

    private static async Task<IResult> StageAttachment(Guid id, HttpContext http, IAntiforgery antiforgery,
        CommunicationsDbContext db, IObjectStore<CommunicationsStorageScope> objectStore, IOptions<ClamAvOptions> scannerOptions, CancellationToken ct)
    {
        var csrf = await ValidateAntiforgery(http, antiforgery); if (csrf is not null) return csrf;
        var userId = CurrentUserId(http);
        if (!userId.HasValue) return TypedResults.Unauthorized();
        var key = http.Request.Headers["Idempotency-Key"].FirstOrDefault();
        if (string.IsNullOrWhiteSpace(key) || key.Length > 200 || key != key.Trim() || key.Any(char.IsControl))
            return Error(StatusCodes.Status400BadRequest, "idempotency_key_required", "A valid Idempotency-Key header is required.");
        var existing = await db.AttachmentUploads.AsNoTracking().SingleOrDefaultAsync(item => item.UploadedByUserId == userId.Value && item.IdempotencyKey == key, ct);
        if (existing is not null) return TypedResults.Ok(ToUploadResponse(existing));
        var conversation = await db.Conversations.AsNoTracking().SingleOrDefaultAsync(item => item.Id == id, ct);
        if (conversation is null) return TypedResults.NotFound();
        var options = scannerOptions.Value;
        var maxBytes = Math.Clamp(options.MaxBytes, 1, 50 * 1024 * 1024);
        IFormCollection form;
        try { form = await http.Request.ReadFormAsync(new FormOptions { MultipartBodyLengthLimit = maxBytes, ValueCountLimit = 20 }, ct); }
        catch (InvalidDataException) { return Error(StatusCodes.Status413PayloadTooLarge, "attachment_too_large", "The attachment exceeds the configured limit."); }
        if (form.Files.Count != 1) return Error(StatusCodes.Status400BadRequest, "file_required", "Exactly one file is required.");
        var file = form.Files[0];
        if (file.Length <= 0 || file.Length > maxBytes) return Error(StatusCodes.Status413PayloadTooLarge, "attachment_too_large", "The attachment exceeds the configured limit.");
        var fileName = AttachmentSafety.SafeFileName(file.FileName);
        var contentType = AttachmentSafety.ContentType(file.ContentType);
        var contentId = AttachmentSafety.ContentId(form["contentId"].FirstOrDefault());
        var isInline = string.Equals(form["isInline"].FirstOrDefault(), "true", StringComparison.OrdinalIgnoreCase) && contentId is not null;
        if (await db.AttachmentUploads.CountAsync(item => item.ConversationId == id && item.UploadedByUserId == userId && item.ScanStatus != "expired" && item.ScanStatus != "quarantined", ct) >= 20)
            return Error(StatusCodes.Status409Conflict, "attachment_limit", "The attachment limit for this conversation has been reached.");
        // The idempotency key is also the crash-retry identity. If the process
        // dies after PutAsync, the next request reuses this exact object key.
        var uploadId = ObjectOwnershipLifecycle.DeterministicGuid(userId.Value, $"staged-upload:{key}");
        var storageKey = $"staged-attachments/{id:N}/{userId.Value:N}/{uploadId:N}";
        var hash = IncrementalHash.CreateHash(HashAlgorithmName.SHA256);
        var now = DateTimeOffset.UtcNow;
        try
        {
            // A reservation is durable before the object-store write.
            await ObjectOwnershipLifecycle.ReserveAsync(db, storageKey, now, ct);
            await db.SaveChangesAsync(ct);
            await using var source = file.OpenReadStream();
            await using var content = new MemoryStream();
            await source.CopyToAsync(content, ct);
            if (content.Length > maxBytes) throw new InvalidDataException();
            content.Position = 0;
            await objectStore.PutAsync(storageKey, content, contentType, ct);
            content.Position = 0;
            var buffer = new byte[64 * 1024];
            while (await content.ReadAsync(buffer, ct) is > 0) { }
            content.Position = 0;
            hash.AppendData(content.ToArray());
        }
        catch (InvalidDataException) { return Error(StatusCodes.Status413PayloadTooLarge, "attachment_too_large", "The attachment exceeds the configured limit."); }
        catch (Exception) when (!ct.IsCancellationRequested)
        {
            return Error(StatusCodes.Status503ServiceUnavailable, "attachment_storage_unavailable", "Attachment storage is unavailable.");
        }
        var upload = new AttachmentUpload
        {
            Id = uploadId,
            ConversationId = id,
            UploadedByUserId = userId.Value,
            IdempotencyKey = key,
            FileName = fileName,
            ContentType = contentType,
            SizeBytes = file.Length,
            ContentHash = Convert.ToHexString(hash.GetHashAndReset()).ToLowerInvariant(),
            ContentId = contentId,
            StorageKey = storageKey,
            ScanStatus = "pending",
            NextScanAt = now,
            IsInline = isInline,
            ExpiresAt = now.AddHours(24),
            CreatedAt = now
        };
        db.AttachmentUploads.Add(upload);
        try
        {
            // The upload row and reservation ownership transition are one DB commit.
            await ObjectOwnershipLifecycle.MarkOwnedAsync(db, [storageKey], ct);
            await db.SaveChangesAsync(ct);
        }
        catch (DbUpdateException exception) when (IsUniqueViolation(exception))
        {
            var duplicate = await db.AttachmentUploads.AsNoTracking().SingleAsync(item => item.UploadedByUserId == userId.Value && item.IdempotencyKey == key, ct);
            return TypedResults.Ok(ToUploadResponse(duplicate));
        }
        return TypedResults.Created(CommunicationPath(http, $"/attachments/{upload.Id}"), ToUploadResponse(upload));
    }

    private static async Task<IResult> GetAttachmentUploadStatus(Guid conversationId, Guid attachmentId,
        HttpContext http, CommunicationsDbContext db, CancellationToken ct)
    {
        http.Response.Headers.CacheControl = "no-store, private";
        http.Response.Headers.Pragma = "no-cache";
        http.Response.Headers["X-Content-Type-Options"] = "nosniff";
        var userId = CurrentUserId(http);
        if (!userId.HasValue) return TypedResults.Unauthorized();

        // Staged uploads are private to their uploader until they are transactionally
        // bound to an outbound message. The conversation id is part of the lookup so
        // a valid upload id cannot be used to probe another conversation.
        var upload = await db.AttachmentUploads.AsNoTracking()
            .SingleOrDefaultAsync(item => item.Id == attachmentId && item.ConversationId == conversationId &&
                item.UploadedByUserId == userId.Value && item.ScanStatus != "expired", ct);
        if (upload is null) return TypedResults.NotFound();

        return TypedResults.Ok(ToUploadResponse(upload));
    }

    private static async Task<IResult> DownloadAttachment(Guid id, HttpContext http, IObjectStore<CommunicationsStorageScope> objectStore, CommunicationsDbContext db, CancellationToken ct)
    {
        var attachment = await db.MessageAttachments.AsNoTracking().Where(item => item.Id == id && item.ScanStatus == "clean")
            .Where(item => item.Message!.Conversation != null).Select(item => new { item.StorageKey, item.FileName, item.ContentType, item.SizeBytes }).SingleOrDefaultAsync(ct);
        if (attachment is null) return TypedResults.NotFound();
        try
        {
            var content = await objectStore.GetAsync(attachment.StorageKey, ct);
            if (content is null) return TypedResults.NotFound();
            var contentType = AttachmentSafety.ContentType(attachment.ContentType);
            var fileName = AttachmentSafety.SafeFileName(attachment.FileName);
            if (attachment.SizeBytes >= 0) http.Response.ContentLength = attachment.SizeBytes;
            return TypedResults.Stream(content, contentType, fileName, enableRangeProcessing: false);
        }
        catch (Exception) when (!ct.IsCancellationRequested) { return TypedResults.NotFound(); }
    }

    private static async Task<IResult> CreateConversation(CreateConversationRequest? request, HttpContext http, IAntiforgery antiforgery, ICustomerDirectory customerDirectory, CommunicationsDbContext db, CancellationToken ct)
    {
        var csrf = await ValidateAntiforgery(http, antiforgery); if (csrf is not null) return csrf;
        var validation = CommunicationValidation.ValidateConversation(request); if (validation.Count > 0) return ValidationError(validation);
        var key = http.Request.Headers["Idempotency-Key"].FirstOrDefault();
        if (string.IsNullOrWhiteSpace(key) || key.Length > 200 || key != key.Trim() || key.Any(char.IsControl)) return Error(StatusCodes.Status400BadRequest, "idempotency_key_required", "A valid Idempotency-Key header is required.");
        var fingerprint = EmailPayloadFingerprint.Create(request!);
        var existing = await db.IdempotencyRecords.SingleOrDefaultAsync(item => item.Key == key, ct);
        if (existing is not null) return existing.PayloadFingerprint == fingerprint ? TypedResults.Ok(new ConversationMutationResponse(existing.ConversationId, existing.MessageId, "queued", key)) : Error(StatusCodes.Status409Conflict, "idempotency_key_reused", "The Idempotency-Key was already used with a different payload.");
        var channel = request!.ChannelId.HasValue ? await db.Channels.SingleOrDefaultAsync(item => item.Id == request.ChannelId && item.IsActive, ct)
            : await db.Channels.Where(item => item.Type == "email" && item.IsActive).OrderByDescending(item => item.IsDefault).ThenBy(item => item.CreatedAt).FirstOrDefaultAsync(ct);
        if (channel is null) return Error(StatusCodes.Status422UnprocessableEntity, "channel_invalid", "The selected channel does not exist or is inactive.");
        var now = DateTimeOffset.UtcNow;
        if (request.CustomerId is { } customerId && await customerDirectory.FindCustomerAsync(customerId, ct) is null) return Error(StatusCodes.Status422UnprocessableEntity, "customer_invalid", "The selected customer does not exist.");
        var conversation = new Conversation { Id = Guid.NewGuid(), ChannelId = channel.Id, CustomerId = request.CustomerId, CustomerAssociationSource = request.CustomerId.HasValue ? CustomerAssociationSources.Manual : null, Subject = request.Subject, Status = "open", LastActivityAt = now, PreviewText = Preview(request.TextBody ?? request.HtmlBody), CreatedAt = now };
        db.Conversations.Add(conversation);
        var message = BuildOutboundMessage(conversation, request.Subject, request.TextBody, request.HtmlBody, http, now);
        await AddGenericDeliveriesAsync(message, conversation, request, db, customerDirectory, now, ct);
        AddQueuedEvents(message, now);
        db.ConversationMessages.Add(message);
        db.OutboxJobs.Add(new OutboxJob { Id = Guid.NewGuid(), MessageId = message.Id, NextAttemptAt = now, CreatedAt = now });
        db.IdempotencyRecords.Add(new IdempotencyRecord { Id = Guid.NewGuid(), Key = key, PayloadFingerprint = fingerprint, ConversationId = conversation.Id, MessageId = message.Id, CreatedAt = now });
        await db.SaveChangesAsync(ct);
        return TypedResults.Created(CommunicationPath(http, $"/conversations/{conversation.Id}"), new ConversationMutationResponse(conversation.Id, message.Id, "queued", key));
    }

    private static async Task<IResult> QueueOutboundAsync(Guid conversationId, ReplyRequest request, HttpContext http, CommunicationsDbContext db, CancellationToken ct, bool requireIdempotency)
    {
        var key = http.Request.Headers["Idempotency-Key"].FirstOrDefault();
        if (requireIdempotency && (string.IsNullOrWhiteSpace(key) || key.Length > 200 || key != key.Trim() || key.Any(char.IsControl))) return Error(StatusCodes.Status400BadRequest, "idempotency_key_required", "A valid Idempotency-Key header is required.");
        var conversation = await db.Conversations.Include(item => item.Channel).Include(item => item.Messages).ThenInclude(item => item.Participant).SingleOrDefaultAsync(item => item.Id == conversationId, ct);
        if (conversation is null) return TypedResults.NotFound();
        if (!conversation.Channel!.IsActive) return Error(StatusCodes.Status422UnprocessableEntity, "channel_inactive", "The conversation channel is inactive.");
        var requestFingerprint = EmailPayloadFingerprint.Create(new { conversationId, request.TextBody, request.HtmlBody, request.Subject, request.ReplyMode, request.AttachmentIds });
        if (key is not null)
        {
            var existing = await db.IdempotencyRecords.SingleOrDefaultAsync(item => item.Key == key, ct);
            if (existing is not null) return existing.PayloadFingerprint == requestFingerprint ? TypedResults.Ok(new ConversationMutationResponse(conversationId, existing.MessageId, "queued", key)) : Error(StatusCodes.Status409Conflict, "idempotency_key_reused", "The Idempotency-Key was already used with a different payload.");
        }
        var lastInbound = conversation.Messages.Where(item => item.Direction == "inbound").OrderByDescending(item => item.OccurredAt).FirstOrDefault();
        var replyMode = request.ReplyMode?.Trim().ToLowerInvariant() ?? "reply";
        var metadata = EmailEnvelopeFactory.ParseMetadata(lastInbound?.ChannelMetadataJson);
        var references = (metadata.References ?? []).Concat(lastInbound?.RfcMessageId is null ? [] : [lastInbound.RfcMessageId]).Distinct(StringComparer.OrdinalIgnoreCase).Take(20).ToArray();
        var now = DateTimeOffset.UtcNow;
        var message = BuildOutboundMessage(conversation, request.Subject ?? conversation.Subject, request.TextBody, request.HtmlBody, http, now, new EmailThreadMetadata(InReplyTo: lastInbound?.RfcMessageId, References: references));
        var recipients = lastInbound?.Participant is null ? Array.Empty<EmailRecipientRequest?>() : [new EmailRecipientRequest(lastInbound.Participant.Address)];
        if (recipients.Length == 0) return Error(StatusCodes.Status422UnprocessableEntity, "recipients_missing", "The conversation has no inbound participant to reply to.");
        var replyAllCc = replyMode == "reply_all" ? ReplyAllAddresses(lastInbound, metadata, conversation.Channel.Address) : [];
        var primaryAddress = EmailSuppression.Normalize(recipients[0]!.Email!);
        var ccAddresses = replyAllCc
            .Where(address => !string.Equals(EmailSuppression.Normalize(address), primaryAddress, StringComparison.Ordinal))
            .Distinct(StringComparer.OrdinalIgnoreCase)
            .ToArray();
        var staged = request.AttachmentIds ?? [];
        var stagedUploads = staged.Count == 0 ? [] : await db.AttachmentUploads.AsNoTracking()
            .Where(item => staged.Contains(item.Id) && item.ConversationId == conversationId &&
                item.UploadedByUserId == CurrentUserId(http) && item.ScanStatus == "clean" && item.ExpiresAt > now)
            .ToListAsync(ct);
        if (stagedUploads.Count != staged.Count) return Error(StatusCodes.Status409Conflict, "attachments_not_ready", "One or more attachments are still being scanned or are unavailable.");
        var actualDestinations = recipients.Select(recipient => EmailSuppression.Normalize(recipient!.Email!))
            .Concat(ccAddresses.Select(EmailSuppression.Normalize))
            .Distinct(StringComparer.Ordinal)
            .ToArray();
        var suppressed = conversation.Channel.Type == "email"
            ? await db.Suppressions.AsNoTracking().Where(item => actualDestinations.Contains(item.NormalizedEmailAddress)).Select(item => item.NormalizedEmailAddress).ToListAsync(ct)
            : [];
        if (suppressed.Count > 0) return Error(StatusCodes.Status422UnprocessableEntity, "recipient_suppressed", "One or more recipients are suppressed.", new Dictionary<string, string[]> { ["recipients"] = suppressed.ToArray() });
        await using var transaction = await db.Database.BeginTransactionAsync(ct);
        if (stagedUploads.Count > 0)
        {
            // Expiry and consumption race on this conditional transition. The
            // winner owns the row until the message attachment and outbox commit.
            var claimed = await db.AttachmentUploads
                .Where(item => staged.Contains(item.Id) && item.ConversationId == conversationId &&
                    item.UploadedByUserId == CurrentUserId(http) && item.ScanStatus == "clean" && item.ExpiresAt > now)
                .ExecuteUpdateAsync(setters => setters.SetProperty(item => item.ScanStatus, "claimed")
                    .SetProperty(item => item.ScanLeaseId, (string?)null)
                    .SetProperty(item => item.ScanLeaseUntil, (DateTimeOffset?)null), ct);
            if (claimed != stagedUploads.Count)
            {
                await transaction.RollbackAsync(ct);
                return Error(StatusCodes.Status409Conflict, "attachments_not_ready", "One or more attachments are still being scanned or are unavailable.");
            }
        }

        AddDeliveries(message, recipients, ccAddresses.Select(address => new EmailRecipientRequest(address)), [], now);
        foreach (var upload in stagedUploads)
            message.Attachments.Add(new MessageAttachment { Id = Guid.NewGuid(), MessageId = message.Id, FileName = upload.FileName, ContentType = upload.ContentType, SizeBytes = upload.SizeBytes, ContentHash = upload.ContentHash, ContentId = upload.ContentId, StorageKey = upload.StorageKey, ScanStatus = "clean", IsInline = upload.IsInline, CreatedAt = now });
        AddQueuedEvents(message, now);
        db.ConversationMessages.Add(message);
        if (stagedUploads.Count > 0)
        {
            await ObjectOwnershipLifecycle.MarkOwnedAsync(db, stagedUploads.Select(item => item.StorageKey), ct);
            await db.AttachmentUploads.Where(item => staged.Contains(item.Id) && item.ScanStatus == "claimed").ExecuteDeleteAsync(ct);
        }
        db.OutboxJobs.Add(new OutboxJob { Id = Guid.NewGuid(), MessageId = message.Id, NextAttemptAt = now, CreatedAt = now });
        db.IdempotencyRecords.Add(new IdempotencyRecord { Id = Guid.NewGuid(), Key = key!, PayloadFingerprint = requestFingerprint, ConversationId = conversationId, MessageId = message.Id, CreatedAt = now });
        await db.SaveChangesAsync(ct);
        await transaction.CommitAsync(ct);
        return TypedResults.Created(CommunicationPath(http, $"/conversations/{conversationId}"), new ConversationMutationResponse(conversationId, message.Id, "queued", key));
    }

    private static async Task<IResult> AddNote(Guid id, NoteRequest? request, HttpContext http, IAntiforgery antiforgery, CommunicationsDbContext db, CancellationToken ct)
    {
        var csrf = await ValidateAntiforgery(http, antiforgery); if (csrf is not null) return csrf;
        var validation = CommunicationValidation.ValidateNote(request); if (validation.Count > 0) return ValidationError(validation);
        var conversation = await db.Conversations.SingleOrDefaultAsync(item => item.Id == id, ct); if (conversation is null) return TypedResults.NotFound();
        var now = DateTimeOffset.UtcNow; var message = new ConversationMessage { Id = Guid.NewGuid(), ConversationId = id, Direction = "internal_note", TextBody = request!.TextBody, AuthorUserId = CurrentUserId(http), OccurredAt = now, CreatedAt = now };
        conversation.LastActivityAt = now; conversation.PreviewText = Preview(request.TextBody); db.ConversationMessages.Add(message); await db.SaveChangesAsync(ct); return TypedResults.Created(CommunicationPath(http, $"/conversations/{id}"), new ConversationMutationResponse(id, message.Id, "created", null));
    }

    private static async Task<IResult> UpdateConversation(Guid id, UpdateConversationRequest? request, ICustomerDirectory customerDirectory, CommunicationsDbContext db, CancellationToken ct)
    {
        if (request is null || (request.Status is not null && request.Status.Trim().ToLowerInvariant() is not ("open" or "closed" or "archived"))) return Error(StatusCodes.Status400BadRequest, "invalid_request", "Status must be open, closed, or archived.");
        var conversation = await db.Conversations.Include(item => item.CustomerCandidates).SingleOrDefaultAsync(item => item.Id == id, ct); if (conversation is null) return TypedResults.NotFound();
        if (request.Status is not null) conversation.Status = request.Status.Trim().ToLowerInvariant();
        if (request.AssignedUserId is { } assigned) conversation.AssignedUserId = assigned.ValueKind == JsonValueKind.Null ? null : assigned.GetGuid();
        if (request.CustomerId.ValueKind != JsonValueKind.Undefined)
        {
            var requestedCustomerId = 0;
            if (request.CustomerId.ValueKind is not (JsonValueKind.Null or JsonValueKind.Number) ||
                (request.CustomerId.ValueKind == JsonValueKind.Number && !request.CustomerId.TryGetInt32(out requestedCustomerId)))
                return Error(StatusCodes.Status400BadRequest, "invalid_request", "CustomerId must be an integer or null.");

            var requestedCustomer = request.CustomerId.ValueKind == JsonValueKind.Null ? (int?)null : requestedCustomerId;
            if (requestedCustomer is { } customerId && await customerDirectory.FindCustomerAsync(customerId, ct) is null)
                return Error(StatusCodes.Status422UnprocessableEntity, "customer_invalid", "The selected customer does not exist.");

            conversation.CustomerId = requestedCustomer;
            conversation.CustomerAssociationSource = requestedCustomer.HasValue ? CustomerAssociationSources.Manual : null;
            conversation.SuggestedCustomerId = null;
            conversation.SuggestedCustomerConfidence = null;
            conversation.SuggestedCustomerReasoning = null;
            conversation.CustomerCandidates.Clear();
        }
        await db.SaveChangesAsync(ct); return TypedResults.Ok(new
        {
            conversation.Id,
            conversation.Status,
            conversation.AssignedUserId,
            conversation.CustomerId,
            conversation.CustomerAssociationSource,
            SuggestedCustomerId = conversation.SuggestedCustomerId,
            CandidateCustomerIds = conversation.CustomerCandidates.OrderBy(candidate => candidate.CustomerId).Select(candidate => candidate.CustomerId).ToArray(),
        });
    }

    private static ConversationMessage BuildOutboundMessage(Conversation conversation, string? subject, string? text, string? html, HttpContext http, DateTimeOffset now, EmailThreadMetadata? metadata = null)
    {
        var message = new ConversationMessage { Id = Guid.NewGuid(), ConversationId = conversation.Id, Direction = "outbound", AuthorUserId = CurrentUserId(http), Subject = subject, TextBody = text, HtmlBody = html, ChannelMetadataJson = metadata is null ? null : JsonSerializer.Serialize(metadata, SmtpDeliveryProvider.JsonOptions), OccurredAt = now, CreatedAt = now };
        message.RfcMessageId = EmailMessageId.For(message.Id);
        conversation.Subject ??= subject; conversation.LastActivityAt = now; conversation.PreviewText = text ?? html; return message;
    }

    private static void AddDeliveries(ConversationMessage message, IEnumerable<EmailRecipientRequest?> to, IEnumerable<EmailRecipientRequest?> cc, IEnumerable<EmailRecipientRequest?> bcc, DateTimeOffset now) { foreach (var (recipient, type) in (to ?? []).Select(item => (item, "to")).Concat((cc ?? []).Select(item => (item, "cc"))).Concat((bcc ?? []).Select(item => (item, "bcc")))) if (recipient?.Email is not null) message.Deliveries.Add(new MessageDelivery { Id = Guid.NewGuid(), MessageId = message.Id, RecipientAddress = recipient.Email, RecipientType = type, CreatedAt = now }); }

    private static async Task AddGenericDeliveriesAsync(ConversationMessage message, Conversation conversation, CreateConversationRequest request, CommunicationsDbContext db, ICustomerDirectory customerDirectory, DateTimeOffset now, CancellationToken ct)
    {
        var channel = await db.Channels.SingleAsync(item => item.Id == conversation.ChannelId, ct);
        var recipients = request.Recipients ?? [];
        if (recipients.Count == 0 && channel.Type == "email")
        {
            AddDeliveries(message, request.To ?? [], request.Cc ?? [], [], now);
            foreach (var delivery in message.Deliveries)
            {
                var participant = await db.Participants.SingleOrDefaultAsync(item => item.ChannelId == channel.Id && item.Address == EmailSuppression.Normalize(delivery.RecipientAddress), ct)
                    ?? new Participant { Id = Guid.NewGuid(), ChannelId = channel.Id, Address = EmailSuppression.Normalize(delivery.RecipientAddress), CreatedAt = now };
                if (participant.Id != Guid.Empty && participant.Channel is null) db.Participants.Add(participant);
                delivery.RecipientParticipantId = participant.Id;
                delivery.RecipientParticipant = participant;
                conversation.Participants.Add(new ConversationParticipant { ConversationId = conversation.Id, ParticipantId = participant.Id, Participant = participant });
            }
            return;
        }

        foreach (var recipient in recipients)
        {
            if (recipient is null) continue;
            Participant? participant = recipient.ParticipantId is { } participantId
                ? await db.Participants.SingleOrDefaultAsync(item => item.Id == participantId && item.ChannelId == channel.Id, ct)
                : null;
            if (participant is null && !string.IsNullOrWhiteSpace(recipient.Address))
            {
                var normalized = channel.Type == "email" ? EmailSuppression.Normalize(recipient.Address) : recipient.Address.Trim();
                participant = await db.Participants.SingleOrDefaultAsync(item => item.ChannelId == channel.Id && item.Address == normalized, ct);
                if (participant is null)
                {
                    participant = new Participant { Id = Guid.NewGuid(), ChannelId = channel.Id, Address = normalized, ContactId = recipient.ContactId, CreatedAt = now };
                    db.Participants.Add(participant);
                }
            }
            if (participant is null) continue;
            if (recipient.ContactId is { } contactId && await customerDirectory.FindContactAsync(contactId, ct) is null)
                throw new InvalidOperationException("The selected contact does not exist.");
            var recipientType = recipient.Type?.Trim().ToLowerInvariant() is "cc" or "bcc" ? recipient.Type.Trim().ToLowerInvariant() : "to";
            message.Deliveries.Add(new MessageDelivery { Id = Guid.NewGuid(), MessageId = message.Id, RecipientParticipantId = participant.Id, RecipientAddress = participant.Address, RecipientType = recipientType, CreatedAt = now });
            if (!conversation.Participants.Any(item => item.ParticipantId == participant.Id)) conversation.Participants.Add(new ConversationParticipant { ConversationId = conversation.Id, ParticipantId = participant.Id, Participant = participant });
        }
    }

    private static void AddQueuedEvents(ConversationMessage message, DateTimeOffset now) { foreach (var delivery in message.Deliveries) message.Events.Add(new MessageEvent { Id = Guid.NewGuid(), MessageId = message.Id, DeliveryId = delivery.Id, EventType = "queued", OccurredAt = now }); message.Events.Add(new MessageEvent { Id = Guid.NewGuid(), MessageId = message.Id, EventType = "message_queued", OccurredAt = now }); }
    private static ConversationMessageResponse ToMessage(ConversationMessage item, HttpContext http) => new(item.Id, item.Direction, item.Participant is null ? null : new ParticipantResponse(item.Participant.Id, item.Participant.ChannelId, item.Participant.Address, item.Participant.DisplayName, item.Participant.ContactId), item.AuthorUserId, item.Subject, item.TextBody, item.HtmlBody, item.OccurredAt, item.CreatedAt, item.Attachments.Select(attachment => new AttachmentResponse(attachment.Id, attachment.FileName, attachment.ContentType, attachment.SizeBytes, attachment.ContentId, attachment.ScanStatus, attachment.IsInline, attachment.CreatedAt, attachment.ScanStatus == "clean", CommunicationPath(http, $"/attachments/{attachment.Id}/download"))).ToArray(), item.Deliveries.Select(delivery => new DeliveryResponse(delivery.Id, delivery.RecipientAddress, delivery.RecipientType, delivery.Status, delivery.Attempts, null, delivery.AcceptedAt)).ToArray());

    private static IReadOnlyList<ParticipantResponse> Participants(IEnumerable<ConversationParticipant> links) => links.Where(item => item.Participant is not null).Select(item => item.Participant!).DistinctBy(item => item.Id).Select(item => new ParticipantResponse(item.Id, item.ChannelId, item.Address, item.DisplayName, item.ContactId)).ToArray();
    private static IReadOnlyList<TagResponse> Tags(IEnumerable<ConversationTag> tags) => tags.Where(item => item.Tag is not null).Select(item => new TagResponse(item.Tag!.Id, item.Tag.Name, item.Tag.Color)).ToArray();
    private static string? Preview(string? value) => string.IsNullOrWhiteSpace(value) ? null : value.Length <= 500 ? value : value[..500];
    private static AttachmentUploadResponse ToUploadResponse(AttachmentUpload item) => new(item.Id, item.FileName, item.ContentType, item.SizeBytes, item.ScanStatus, item.IsInline, item.ExpiresAt, item.ScanStatus == "clean");

    private static ReplyRecipientsResponse ReplyRecipients(Conversation conversation)
    {
        var inbound = conversation.Messages.Where(item => item.Direction == "inbound").OrderByDescending(item => item.OccurredAt).FirstOrDefault();
        if (inbound?.Participant is null || conversation.Channel is null)
            return new(false, false, null, []);
        var metadata = EmailEnvelopeFactory.ParseMetadata(inbound.ChannelMetadataJson);
        var cc = ReplyAllAddresses(inbound, metadata, conversation.Channel.Address)
            .Select(address => new ParticipantResponse(Guid.Empty, conversation.ChannelId, address, null, null)).ToArray();
        return new(true, cc.Length > 0, inbound.Participant.Address, cc);
    }

    private static IReadOnlyList<string> ReplyAllAddresses(ConversationMessage? inbound, EmailThreadMetadata metadata, string mailbox)
    {
        if (inbound is null) return [];
        var sender = inbound.Participant?.Address;
        var normalizedSender = sender is null ? null : EmailSuppression.Normalize(sender);
        return (metadata.Cc ?? [])
            .Where(CommunicationValidation.IsEmail)
            .Select(EmailSuppression.Normalize)
            .Where(address => !string.Equals(address, EmailSuppression.Normalize(mailbox), StringComparison.Ordinal) &&
                              (normalizedSender is null || !string.Equals(address, normalizedSender, StringComparison.Ordinal)))
            .Distinct(StringComparer.OrdinalIgnoreCase).Take(100).ToArray();
    }

    private static (int Page, int PageSize) PageValues(int? page, int? pageSize) => (Math.Max(page ?? 1, 1), Math.Clamp(pageSize ?? 25, 1, 100));
}