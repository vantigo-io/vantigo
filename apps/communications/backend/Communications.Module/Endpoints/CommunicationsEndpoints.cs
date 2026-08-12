using System.Security.Claims;
using System.Text.Json;

using Asp.Versioning;

using Microsoft.AspNetCore.Antiforgery;
using Microsoft.AspNetCore.Mvc;
using Microsoft.EntityFrameworkCore;

using Vantigo.Communications.Authorization;
using Vantigo.Communications.Database.Communications;
using Vantigo.Communications.Endpoints.Dtos;
using Vantigo.Communications.Services;
using Vantigo.Contracts;
using Vantigo.Contracts.AspNetCore.Authorization;

namespace Vantigo.Communications.Endpoints;

internal static class CommunicationsEndpoints
{
    public static IEndpointRouteBuilder MapVersionedBusinessEndpoints(this IEndpointRouteBuilder endpoints)
    {
        var api = endpoints.NewVersionedApi().MapGroup("/api/v{version:apiVersion}/communications").HasApiVersion(new ApiVersion(1));
        api.MapGet("/messages", ListMessages)
            .RequirePermission(CommunicationsPermissions.MessagesView)
            .WithSummary("List email messages");
        api.MapGet("/messages/{id:guid}", GetMessage)
            .RequirePermission(CommunicationsPermissions.MessagesView)
            .WithSummary("Get an email message");
        api.MapGet("/messages/{id:guid}/events", ListEvents)
            .RequirePermission(CommunicationsPermissions.MessagesView)
            .WithSummary("List append-only message events");
        api.MapPost("/messages/{id:guid}/resend", ResendMessage)
            .RequirePermission(CommunicationsPermissions.MessagesManage)
            .WithSummary("Re-queue an email message for sending")
            .WithDescription("Scope 'failed' re-queues only submission_failed recipients; scope 'all' re-queues every recipient.");
        api.MapPost("/messages/{id:guid}/archive", ArchiveMessage)
            .RequirePermission(CommunicationsPermissions.MessagesManage)
            // The mutation result is the complete message detail, including
            // bodies and recipients, so message-management alone must not
            // disclose it.
            .RequirePermission(CommunicationsPermissions.MessagesView)
            .WithSummary("Archive an email message")
            .WithDescription("Soft-deletes the message from the default list view and cancels any pending send.");
        api.MapPost("/messages/{id:guid}/unarchive", UnarchiveMessage)
            .RequirePermission(CommunicationsPermissions.MessagesManage)
            .RequirePermission(CommunicationsPermissions.MessagesView)
            .WithSummary("Unarchive an email message");

        api.MapPost("/messages", CreateMessage)
            .RequirePermission(CommunicationsPermissions.MessagesSend)

            .WithSummary("Queue an email message")
            .WithDescription("Requires an authenticated session with same-origin antiforgery protection. Idempotency-Key is required. SMTP is queued durably and delivery is at-least-once.")
            .Produces<EmailCreateResponse>(StatusCodes.Status201Created)
            .Produces<EmailCreateResponse>(StatusCodes.Status200OK)
            .Produces<CommunicationErrorResponse>(StatusCodes.Status400BadRequest)
            .Produces<CommunicationErrorResponse>(StatusCodes.Status401Unauthorized)
            .Produces<CommunicationErrorResponse>(StatusCodes.Status409Conflict)
            .Produces<CommunicationErrorResponse>(StatusCodes.Status422UnprocessableEntity)
            .Produces<CommunicationErrorResponse>(StatusCodes.Status503ServiceUnavailable);

        api.MapGet("/mailboxes", ListMailboxes)
            .RequirePermission(CommunicationsPermissions.MailboxesView);
        api.MapGet("/mailboxes/{id:guid}", GetMailbox)
            .RequirePermission(CommunicationsPermissions.MailboxesView);
        api.MapPost("/mailboxes", CreateMailbox)
            .RequirePermission(CommunicationsPermissions.MailboxesManage)
            .RequirePermission(CommunicationsPermissions.MailboxesView);
        api.MapPut("/mailboxes/{id:guid}", UpdateMailbox)
            .RequirePermission(CommunicationsPermissions.MailboxesManage)
            .RequirePermission(CommunicationsPermissions.MailboxesView);
        api.MapPost("/mailboxes/{id:guid}/verify", VerifyMailbox)
            .RequirePermission(CommunicationsPermissions.MailboxesManage);
        api.MapGet("/suppressions", ListSuppressions)
            .RequirePermission(CommunicationsPermissions.SuppressionsView);
        api.MapGet("/suppressions/{id:guid}", GetSuppression)
            .RequirePermission(CommunicationsPermissions.SuppressionsView);
        api.MapPost("/suppressions", CreateSuppression)
            .RequirePermission(CommunicationsPermissions.SuppressionsManage)
            .RequirePermission(CommunicationsPermissions.SuppressionsView);
        api.MapDelete("/suppressions/{id:guid}", DeleteSuppression)
            .RequirePermission(CommunicationsPermissions.SuppressionsManage);
        return endpoints;
    }

    private static async Task<IResult> ListMessages(int? page, int? pageSize, Guid? mailboxId, bool? includeArchived, CommunicationsDbContext db, CancellationToken cancellationToken)
    {
        var (currentPage, size) = PageValues(page, pageSize);
        var query = db.EmailMessages.AsNoTracking()
            .Where(message => !mailboxId.HasValue || message.MailboxId == mailboxId.Value)
            .Where(message => includeArchived == true || message.ArchivedAt == null);
        var total = await query.CountAsync(cancellationToken);
        var items = await query.OrderByDescending(message => message.CreatedAt)
            .Skip((currentPage - 1) * size).Take(size)
                .Select(message => new MessageListItem(message.Id, message.Subject, message.CreatedAt,
                    message.Deliveries.Count, message.Deliveries.All(delivery => delivery.Status == "relay_accepted") ? "relay_accepted" :
                        message.Deliveries.Any(delivery => delivery.Status == "submission_failed") ? "submission_failed" : "queued", message.Source,
                    message.ArchivedAt,
                    new MailboxSummaryResponse(message.Mailbox!.Id, message.Mailbox.FromAddress, message.Mailbox.DisplayName)))
            .ToListAsync(cancellationToken);
        return TypedResults.Ok(PaginatedResponse<MessageListItem>.Create(items, currentPage, size, total));
    }

    private static async Task<IResult> GetMessage(Guid id, CommunicationsDbContext db, CancellationToken cancellationToken)
    {
        var message = await db.EmailMessages.AsNoTracking().Include(item => item.Mailbox).Include(item => item.Deliveries).Include(item => item.ExternalLinks)
            .SingleOrDefaultAsync(item => item.Id == id, cancellationToken);
        return message is null ? TypedResults.NotFound() : TypedResults.Ok(ToDetail(message));
    }

    private static async Task<IResult> ListEvents(Guid id, int? page, int? pageSize, CommunicationsDbContext db, CancellationToken cancellationToken)
    {
        if (!await db.EmailMessages.AnyAsync(message => message.Id == id, cancellationToken)) return TypedResults.NotFound();
        var (currentPage, size) = PageValues(page, pageSize);
        var query = db.MessageEvents.AsNoTracking().Where(messageEvent => messageEvent.MessageId == id);
        var total = await query.CountAsync(cancellationToken);
        var items = await query.OrderBy(messageEvent => messageEvent.OccurredAt).ThenBy(messageEvent => messageEvent.Id)
            .Skip((currentPage - 1) * size).Take(size)
            .Select(messageEvent => new MessageEventResponse(messageEvent.Id, messageEvent.DeliveryId, messageEvent.EventType, messageEvent.OccurredAt, messageEvent.DataJson))
            .ToListAsync(cancellationToken);
        return TypedResults.Ok(PaginatedResponse<MessageEventResponse>.Create(items, currentPage, size, total));
    }

    private static async Task<IResult> ResendMessage(Guid id, ResendMessageRequest? request, CommunicationsDbContext db, CancellationToken cancellationToken)
    {
        var scope = request?.Scope?.Trim().ToLowerInvariant() ?? "failed";
        if (scope is not ("failed" or "all"))
            return Error(StatusCodes.Status400BadRequest, "invalid_scope", "Scope must be 'failed' or 'all'.");

        await using var transaction = await db.Database.BeginTransactionAsync(cancellationToken);
        var message = await db.EmailMessages.Include(item => item.Deliveries).SingleOrDefaultAsync(item => item.Id == id, cancellationToken);
        if (message is null) return TypedResults.NotFound();
        if (message.ArchivedAt is not null)
            return Error(StatusCodes.Status409Conflict, "message_archived", "An archived message cannot be resent. Unarchive it first.");
        var activeJob = await db.OutboxJobs.AnyAsync(job => job.MessageId == id &&
            (job.Status == "pending" || job.Status == "retry" || job.Status == "processing"), cancellationToken);
        if (activeJob)
            return Error(StatusCodes.Status409Conflict, "resend_in_progress", "A send for this message is already pending or in progress.");

        var targets = scope == "failed"
            ? message.Deliveries.Where(delivery => delivery.Status == "submission_failed").ToArray()
            : message.Deliveries.Where(delivery => delivery.Status != "suppressed").ToArray();
        if (targets.Length == 0)
            return Error(StatusCodes.Status400BadRequest, "no_deliveries_to_resend",
                scope == "failed" ? "This message has no failed recipients to resend." : "This message has no recipients to resend.");

        var now = DateTimeOffset.UtcNow;
        foreach (var delivery in targets)
        {
            delivery.Status = "queued";
            delivery.LastError = null;
            delivery.AcceptedAt = null;
        }
        db.MessageEvents.Add(new MessageEvent
        {
            Id = Guid.NewGuid(),
            MessageId = message.Id,
            EventType = "resend_requested",
            OccurredAt = now,
            DataJson = JsonSerializer.Serialize(new { scope, recipientCount = targets.Length }),
        });
        db.OutboxJobs.Add(new OutboxJob { Id = Guid.NewGuid(), MessageId = message.Id, NextAttemptAt = now, CreatedAt = now });
        await db.SaveChangesAsync(cancellationToken);
        await transaction.CommitAsync(cancellationToken);
        return TypedResults.Ok(new ResendMessageResponse(message.Id, "queued", scope, targets.Length));
    }

    private static async Task<IResult> ArchiveMessage(Guid id, CommunicationsDbContext db, CancellationToken cancellationToken)
    {
        await using var transaction = await db.Database.BeginTransactionAsync(cancellationToken);
        var message = await db.EmailMessages.Include(item => item.Mailbox).Include(item => item.Deliveries).Include(item => item.ExternalLinks)
            .SingleOrDefaultAsync(item => item.Id == id, cancellationToken);
        if (message is null) return TypedResults.NotFound();
        if (message.ArchivedAt is not null)
        {
            await transaction.CommitAsync(cancellationToken);
            return TypedResults.Ok(ToDetail(message));
        }

        var now = DateTimeOffset.UtcNow;
        message.ArchivedAt = now;
        // Cancel sends that have not been claimed yet. Jobs in the processing
        // state hold a lease and are mid-send, so they are left untouched.
        await db.OutboxJobs.Where(job => job.MessageId == id && (job.Status == "pending" || job.Status == "retry"))
            .ExecuteUpdateAsync(setters => setters
                .SetProperty(job => job.Status, "cancelled")
                .SetProperty(job => job.CompletedAt, now), cancellationToken);
        foreach (var delivery in message.Deliveries.Where(delivery => delivery.Status is "queued" or "retrying"))
            delivery.Status = "cancelled";
        db.MessageEvents.Add(new MessageEvent { Id = Guid.NewGuid(), MessageId = message.Id, EventType = "archived", OccurredAt = now });
        await db.SaveChangesAsync(cancellationToken);
        await transaction.CommitAsync(cancellationToken);
        return TypedResults.Ok(ToDetail(message));
    }

    private static async Task<IResult> UnarchiveMessage(Guid id, CommunicationsDbContext db, CancellationToken cancellationToken)
    {
        var message = await db.EmailMessages.Include(item => item.Mailbox).Include(item => item.Deliveries).Include(item => item.ExternalLinks)
            .SingleOrDefaultAsync(item => item.Id == id, cancellationToken);
        if (message is null) return TypedResults.NotFound();
        if (message.ArchivedAt is not null)
        {
            message.ArchivedAt = null;
            db.MessageEvents.Add(new MessageEvent { Id = Guid.NewGuid(), MessageId = message.Id, EventType = "unarchived", OccurredAt = DateTimeOffset.UtcNow });
            await db.SaveChangesAsync(cancellationToken);
        }
        return TypedResults.Ok(ToDetail(message));
    }

    private static async Task<IResult> CreateMessage(
        CreateEmailRequest? request,
        HttpContext httpContext,
        IAntiforgery antiforgery,
        ICustomerDirectory customerDirectory,
        CommunicationsDbContext db,
        CancellationToken cancellationToken)
    {
        try { await antiforgery.ValidateRequestAsync(httpContext); }
        catch (AntiforgeryValidationException)
        { return Error(StatusCodes.Status400BadRequest, "csrf_validation_failed", "A valid X-XSRF-TOKEN header and antiforgery cookie are required."); }

        var key = httpContext.Request.Headers["Idempotency-Key"].FirstOrDefault();
        if (string.IsNullOrWhiteSpace(key) || key.Length > 200 || key != key.Trim() || key.Any(char.IsControl))
            return Error(StatusCodes.Status400BadRequest, "idempotency_key_required", "A valid Idempotency-Key header is required.");
        var errors = CommunicationValidation.Validate(request);
        if (errors.Count > 0) return ValidationError(errors);

        var payloadFingerprint = EmailPayloadFingerprint.Create(request!);
        for (var attempt = 0; attempt < 3; attempt++)
        {
            try
            {
                return await PersistMessageAsync(request!, key, payloadFingerprint, httpContext, customerDirectory, db, cancellationToken);
            }
            catch (Exception exception) when (IsIdempotencyRace(exception))
            {
                if (attempt < 2)
                {
                    db.ChangeTracker.Clear();
                    await Task.Delay(TimeSpan.FromMilliseconds(20 * (attempt + 1)), cancellationToken);
                    continue;
                }

                return await ReconcileIdempotencyAsync(key, payloadFingerprint, db, cancellationToken);
            }
        }

        return Error(StatusCodes.Status409Conflict, "idempotency_unavailable", "The idempotency request could not be reconciled.");
    }

    private static async Task<IResult> PersistMessageAsync(
        CreateEmailRequest request,
        string key,
        string payloadFingerprint,
        HttpContext httpContext,
        ICustomerDirectory customerDirectory,
        CommunicationsDbContext db,
        CancellationToken cancellationToken)
    {
        await using var transaction = await db.Database.BeginTransactionAsync(System.Data.IsolationLevel.Serializable, cancellationToken);
        var existing = await db.IdempotencyRecords.SingleOrDefaultAsync(record => record.Key == key, cancellationToken);
        if (existing is not null)
        {
            if (!string.Equals(existing.PayloadFingerprint, payloadFingerprint, StringComparison.Ordinal))
                return Error(StatusCodes.Status409Conflict, "idempotency_key_reused", "The Idempotency-Key was already used with a different payload.");
            await transaction.CommitAsync(cancellationToken);
            return TypedResults.Ok(new EmailCreateResponse(existing.MessageId, "queued", key));
        }

        var mailbox = request.MailboxId.HasValue
            ? await db.SharedMailboxes.SingleOrDefaultAsync(item => item.Id == request.MailboxId.Value && item.IsActive, cancellationToken)
            : await db.SharedMailboxes.Where(item => item.IsActive).OrderByDescending(item => item.IsDefault).ThenBy(item => item.CreatedAt).FirstOrDefaultAsync(cancellationToken);
        if (request.MailboxId.HasValue && mailbox is null)
            return Error(StatusCodes.Status422UnprocessableEntity, "mailbox_invalid", "The selected mailbox does not exist or is inactive.");
        if (mailbox is null) return Error(StatusCodes.Status503ServiceUnavailable, "mailbox_not_configured", "A shared mailbox has not been configured.");
        var recipients = RecipientRequests(request).ToArray();
        var normalized = recipients.Select(recipient => EmailSuppression.Normalize(recipient.EmailAddress)).ToArray();
        var suppressed = await db.Suppressions.AsNoTracking().Where(item => normalized.Contains(item.NormalizedEmailAddress)).Select(item => item.NormalizedEmailAddress).ToListAsync(cancellationToken);
        if (suppressed.Count > 0) return Error(StatusCodes.Status422UnprocessableEntity, "recipient_suppressed", "One or more recipients are suppressed.", new Dictionary<string, string[]> { ["recipients"] = suppressed.ToArray() });

        var now = DateTimeOffset.UtcNow;
        var message = new EmailMessage
        {
            Id = Guid.NewGuid(),
            MailboxId = mailbox.Id,
            Subject = request.Subject!,
            TextBody = request.TextBody,
            HtmlBody = request.HtmlBody,
            CreatedAt = now,
            CreatedByUserId = Guid.TryParse(httpContext.User.FindFirstValue(ClaimTypes.NameIdentifier), out var userId) ? userId : null,
            Source = string.IsNullOrWhiteSpace(request.Source) ? null : request.Source,
        };
        foreach (var recipient in recipients)
        {
            var delivery = new RecipientDelivery
            {
                Id = Guid.NewGuid(),
                MessageId = message.Id,
                EmailAddress = recipient.EmailAddress,
                RecipientType = recipient.Type,
                CreatedAt = now,
            };
            message.Deliveries.Add(delivery);
            message.Events.Add(new MessageEvent
            {
                Id = Guid.NewGuid(),
                MessageId = message.Id,
                DeliveryId = delivery.Id,
                EventType = "queued",
                OccurredAt = now,
            });
        }
        foreach (var link in request.ExternalLinks ?? [])
        {
            var displayLabel = link!.DisplayLabel;
            if (string.IsNullOrWhiteSpace(displayLabel) &&
                string.Equals(link.SourceSystem, "customers", StringComparison.OrdinalIgnoreCase) &&
                int.TryParse(link.SourceInstance, out var customerId))
            {
                displayLabel = (link.EntityType?.Contains("contact", StringComparison.OrdinalIgnoreCase) == true
                    ? (await customerDirectory.FindContactAsync(customerId, cancellationToken))?.Email
                    : (await customerDirectory.FindCustomerAsync(customerId, cancellationToken))?.Name);
            }
            message.ExternalLinks.Add(new ExternalEntityLink
            {
                Id = Guid.NewGuid(),
                MessageId = message.Id,
                SourceSystem = link!.SourceSystem!,
                SourceInstance = link.SourceInstance!,
                EntityType = link.EntityType!,
                ExternalEntityId = link.ExternalEntityId!,
                DisplayLabel = string.IsNullOrEmpty(displayLabel) ? null : displayLabel,
            });
        }
        message.Events.Add(new MessageEvent { Id = Guid.NewGuid(), MessageId = message.Id, EventType = "message_queued", OccurredAt = now });
        db.EmailMessages.Add(message);
        db.IdempotencyRecords.Add(new IdempotencyRecord { Id = Guid.NewGuid(), Key = key, PayloadFingerprint = payloadFingerprint, MessageId = message.Id, CreatedAt = now });
        db.OutboxJobs.Add(new OutboxJob { Id = Guid.NewGuid(), MessageId = message.Id, NextAttemptAt = now, CreatedAt = now });
        await db.SaveChangesAsync(cancellationToken);
        await transaction.CommitAsync(cancellationToken);
        return TypedResults.Created($"/api/v1/communications/messages/{message.Id}", new EmailCreateResponse(message.Id, "queued", key));
    }

    private static async Task<IResult> ReconcileIdempotencyAsync(string key, string fingerprint, CommunicationsDbContext db, CancellationToken cancellationToken)
    {
        db.ChangeTracker.Clear();
        var existing = await db.IdempotencyRecords.AsNoTracking().SingleOrDefaultAsync(record => record.Key == key, cancellationToken);
        if (existing is null) return Error(StatusCodes.Status409Conflict, "idempotency_unavailable", "The idempotency request could not be reconciled.");
        return string.Equals(existing.PayloadFingerprint, fingerprint, StringComparison.Ordinal)
            ? TypedResults.Ok(new EmailCreateResponse(existing.MessageId, "queued", key))
            : Error(StatusCodes.Status409Conflict, "idempotency_key_reused", "The Idempotency-Key was already used with a different payload.");
    }

    private static bool IsIdempotencyRace(Exception exception)
    {
        for (var current = exception; current is not null; current = current.InnerException)
        {
            if (current is Npgsql.PostgresException postgres && postgres.SqlState is Npgsql.PostgresErrorCodes.UniqueViolation or Npgsql.PostgresErrorCodes.SerializationFailure or Npgsql.PostgresErrorCodes.DeadlockDetected)
                return true;
        }
        return false;
    }

    private static async Task<IResult> ListMailboxes(CommunicationsDbContext db, CancellationToken cancellationToken)
    {
        var mailboxes = await db.SharedMailboxes.AsNoTracking().Include(mailbox => mailbox.Credential).OrderBy(mailbox => mailbox.CreatedAt).ToListAsync(cancellationToken);
        return TypedResults.Ok(mailboxes.Select(ToMailboxResponse).ToArray());
    }

    private static async Task<IResult> GetMailbox(Guid id, CommunicationsDbContext db, CancellationToken cancellationToken)
    {
        var mailbox = await db.SharedMailboxes.AsNoTracking().Include(item => item.Credential).SingleOrDefaultAsync(item => item.Id == id, cancellationToken);
        return mailbox is null ? TypedResults.NotFound() : TypedResults.Ok(ToMailboxResponse(mailbox));
    }

    private static async Task<IResult> CreateMailbox(CreateMailboxRequest? request, CommunicationsDbContext db, MailboxCredentialProtector protector, CancellationToken cancellationToken)
    {
        var errors = CommunicationValidation.ValidateMailbox(request);
        if (errors.Count > 0) return ValidationError(errors);
        var now = DateTimeOffset.UtcNow;
        await using var transaction = await db.Database.BeginTransactionAsync(cancellationToken);
        var provider = CommunicationValidation.ProviderName(request!.Provider);
        var mailbox = new SharedMailbox { Id = Guid.NewGuid(), FromAddress = request.FromAddress!, DisplayName = string.IsNullOrEmpty(request.DisplayName) ? null : request.DisplayName, Provider = provider, CreatedAt = now };
        mailbox.IsDefault = request.IsDefault == true || !await db.SharedMailboxes.AnyAsync(cancellationToken);
        AddCredential(mailbox, request.Smtp, request.Mailgun, provider, protector, now);
        if (mailbox.IsDefault) await db.SharedMailboxes.Where(item => item.IsDefault).ExecuteUpdateAsync(setters => setters.SetProperty(item => item.IsDefault, false), cancellationToken);
        db.SharedMailboxes.Add(mailbox);
        try { await db.SaveChangesAsync(cancellationToken); }
        catch (DbUpdateException exception) when (IsUniqueViolation(exception))
        { return Error(StatusCodes.Status409Conflict, "mailbox_address_exists", "A mailbox with this FromAddress already exists."); }
        await transaction.CommitAsync(cancellationToken);
        return TypedResults.Created($"/api/v1/communications/mailboxes/{mailbox.Id}", ToMailboxResponse(mailbox));
    }

    private static async Task<IResult> UpdateMailbox(Guid id, UpdateMailboxRequest? request, CommunicationsDbContext db, MailboxCredentialProtector protector, CancellationToken cancellationToken)
    {
        var errors = CommunicationValidation.ValidateMailboxUpdate(request);
        if (errors.Count > 0) return ValidationError(errors);
        var mailbox = await db.SharedMailboxes.Include(item => item.Credential).SingleOrDefaultAsync(item => item.Id == id, cancellationToken);
        if (mailbox is null) return TypedResults.NotFound();
        var activeOtherCount = await db.SharedMailboxes.CountAsync(item => item.Id != id && item.IsActive, cancellationToken);
        if (request!.IsDefault == false && mailbox.IsDefault)
            return Error(StatusCodes.Status409Conflict, "mailbox_default_required", "Another mailbox must be promoted before this default mailbox can be demoted.");
        if (request.IsActive == false && mailbox.IsDefault && activeOtherCount > 0)
            return Error(StatusCodes.Status409Conflict, "mailbox_default_required", "The default mailbox cannot be deactivated while another mailbox is active.");
        await using var transaction = await db.Database.BeginTransactionAsync(cancellationToken);
        if (request.DisplayName is not null) mailbox.DisplayName = string.IsNullOrEmpty(request.DisplayName) ? null : request.DisplayName;
        if (request.IsActive is { } isActive) mailbox.IsActive = isActive;
        if (request.IsDefault == true) mailbox.IsDefault = true;
        if (request.Provider is not null || request.Smtp is not null || request.Mailgun is not null)
        {
            var provider = CommunicationValidation.ProviderName(request.Provider);
            mailbox.Provider = provider;
            // Replace the whole credential config object. The old row is deleted
            // immediately so the insert of its successor (same unique MailboxId)
            // cannot conflict within a single change-tracker save.
            if (mailbox.Credential is not null)
            {
                db.Entry(mailbox.Credential).State = EntityState.Detached;
                await db.MailboxProviderCredentials.Where(item => item.MailboxId == mailbox.Id).ExecuteDeleteAsync(cancellationToken);
            }
            mailbox.Credential = null;
            AddCredential(mailbox, request.Smtp, request.Mailgun, provider, protector, DateTimeOffset.UtcNow);
            // The new credential has a client-generated key, so it must be added
            // explicitly: navigation fixup on a tracked mailbox would otherwise
            // mark it Modified and issue an UPDATE for a row that does not exist.
            if (mailbox.Credential is not null) db.MailboxProviderCredentials.Add(mailbox.Credential);
        }
        if (mailbox.IsDefault) await db.SharedMailboxes.Where(item => item.Id != id && item.IsDefault).ExecuteUpdateAsync(setters => setters.SetProperty(item => item.IsDefault, false), cancellationToken);
        await db.SaveChangesAsync(cancellationToken);
        await transaction.CommitAsync(cancellationToken);
        return TypedResults.Ok(ToMailboxResponse(mailbox));
    }

    private static async Task<IResult> VerifyMailbox(Guid id, CommunicationsDbContext db,
        [FromServices] SmtpDeliveryProvider smtp, [FromServices] MailgunDeliveryProvider mailgun, CancellationToken cancellationToken)
    {
        var mailbox = await db.SharedMailboxes.Include(item => item.Credential).SingleOrDefaultAsync(item => item.Id == id, cancellationToken);
        if (mailbox is null) return TypedResults.NotFound();
        try
        {
            using var timeout = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
            timeout.CancelAfter(TimeSpan.FromSeconds(10));
            if (mailbox.Provider == "smtp") await smtp.VerifyAsync(mailbox, timeout.Token);
            else if (mailbox.Provider == "mailgun") await mailgun.VerifyAsync(mailbox, timeout.Token);
            else throw new InvalidOperationException($"Unknown mailbox provider '{mailbox.Provider}'.");
            return TypedResults.Ok(new { ok = true });
        }
        catch (Exception exception) when (!cancellationToken.IsCancellationRequested)
        { return Error(StatusCodes.Status422UnprocessableEntity, "verification_failed", exception.Message); }
    }

    private static async Task<IResult> ListSuppressions(CommunicationsDbContext db, CancellationToken cancellationToken) =>
        TypedResults.Ok(await db.Suppressions.AsNoTracking().OrderByDescending(item => item.CreatedAt)
            .Select(item => new SuppressionResponse(item.Id, item.NormalizedEmailAddress, item.Reason, item.CreatedAt)).ToListAsync(cancellationToken));

    private static async Task<IResult> GetSuppression(Guid id, CommunicationsDbContext db, CancellationToken cancellationToken)
    {
        var suppression = await db.Suppressions.AsNoTracking().SingleOrDefaultAsync(item => item.Id == id, cancellationToken);
        return suppression is null ? TypedResults.NotFound() : TypedResults.Ok(new SuppressionResponse(suppression.Id, suppression.NormalizedEmailAddress, suppression.Reason, suppression.CreatedAt));
    }

    private static async Task<IResult> CreateSuppression(CreateSuppressionRequest? request, CommunicationsDbContext db, CancellationToken cancellationToken)
    {
        var errors = CommunicationValidation.ValidateSuppression(request);
        if (errors.Count > 0) return ValidationError(errors);
        var normalized = EmailSuppression.Normalize(request!.EmailAddress!);
        var existing = await db.Suppressions.SingleOrDefaultAsync(item => item.NormalizedEmailAddress == normalized, cancellationToken);
        if (existing is not null) return TypedResults.Ok(new SuppressionResponse(existing.Id, existing.NormalizedEmailAddress, existing.Reason, existing.CreatedAt));
        var suppression = new Suppression { Id = Guid.NewGuid(), NormalizedEmailAddress = normalized, Reason = string.IsNullOrEmpty(request.Reason) ? null : request.Reason, CreatedAt = DateTimeOffset.UtcNow };
        db.Suppressions.Add(suppression);
        await db.SaveChangesAsync(cancellationToken);
        return TypedResults.Created($"/api/v1/communications/suppressions/{suppression.Id}", new SuppressionResponse(suppression.Id, suppression.NormalizedEmailAddress, suppression.Reason, suppression.CreatedAt));
    }

    private static async Task<IResult> DeleteSuppression(Guid id, CommunicationsDbContext db, CancellationToken cancellationToken)
    {
        var suppression = await db.Suppressions.SingleOrDefaultAsync(item => item.Id == id, cancellationToken);
        if (suppression is null) return TypedResults.NotFound();
        db.Suppressions.Remove(suppression);
        await db.SaveChangesAsync(cancellationToken);
        return TypedResults.NoContent();
    }

    private static IEnumerable<(string EmailAddress, string Type)> RecipientRequests(CreateEmailRequest request)
    {
        foreach (var recipient in request.To ?? []) yield return (recipient!.Email!, "to");
        foreach (var recipient in request.Cc ?? []) yield return (recipient!.Email!, "cc");
        foreach (var recipient in request.Bcc ?? []) yield return (recipient!.Email!, "bcc");
    }

    private static MessageDetailResponse ToDetail(EmailMessage message) => new(
        message.Id, message.MailboxId, message.Subject, message.TextBody, message.HtmlBody, message.CreatedAt, message.Source, message.ArchivedAt,
        message.Deliveries.OrderBy(delivery => delivery.CreatedAt).Select(delivery => new DeliveryResponse(delivery.Id, delivery.EmailAddress, delivery.RecipientType, delivery.Status, delivery.Attempts, delivery.LastError, delivery.AcceptedAt)).ToArray(),
        message.ExternalLinks.Select(link => new ExternalEntityLinkResponse(link.Id, link.SourceSystem, link.SourceInstance, link.EntityType, link.ExternalEntityId, link.DisplayLabel)).ToArray(),
        new MailboxSummaryResponse(message.Mailbox!.Id, message.Mailbox.FromAddress, message.Mailbox.DisplayName));

    private static void AddCredential(SharedMailbox mailbox, SmtpMailboxCredentialRequest? smtp, MailgunMailboxCredentialRequest? mailgun,
        string provider, MailboxCredentialProtector protector, DateTimeOffset now)
    {
        if (provider == "smtp" && smtp is not null)
        {
            mailbox.Credential = new MailboxProviderCredential
            {
                Id = Guid.NewGuid(),
                MailboxId = mailbox.Id,
                Provider = provider,
                SettingsJson = JsonSerializer.Serialize(new SmtpProviderSettings(smtp.Host!.Trim(), smtp.Port!.Value, smtp.UseSsl ?? false, string.IsNullOrWhiteSpace(smtp.Username) ? null : smtp.Username), SmtpDeliveryProvider.JsonOptions),
                SecretCiphertext = protector.Protect(smtp.Password ?? string.Empty),
                CreatedAt = now,
            };
        }
        else if (provider == "mailgun" && mailgun is not null)
        {
            mailbox.Credential = new MailboxProviderCredential
            {
                Id = Guid.NewGuid(),
                MailboxId = mailbox.Id,
                Provider = provider,
                SettingsJson = JsonSerializer.Serialize(new MailgunProviderSettings(mailgun.Domain!.Trim(), mailgun.Region!.Trim().ToLowerInvariant()), SmtpDeliveryProvider.JsonOptions),
                SecretCiphertext = protector.Protect(mailgun.ApiKey!.Trim()),
                CreatedAt = now,
            };
        }
    }

    private static MailboxResponse ToMailboxResponse(SharedMailbox mailbox)
    {
        MailboxSettingsSummary? settings = null;
        if (mailbox.Credential is not null)
        {
            if (mailbox.Provider == "smtp")
            {
                var smtp = JsonSerializer.Deserialize<SmtpProviderSettings>(mailbox.Credential.SettingsJson, SmtpDeliveryProvider.JsonOptions);
                settings = smtp is null ? null : new MailboxSettingsSummary(smtp.Host, smtp.Port, smtp.UseSsl, smtp.Username, null, null);
            }
            else if (mailbox.Provider == "mailgun")
            {
                var mailgun = JsonSerializer.Deserialize<MailgunProviderSettings>(mailbox.Credential.SettingsJson, SmtpDeliveryProvider.JsonOptions);
                settings = mailgun is null ? null : new MailboxSettingsSummary(null, null, null, null, mailgun.Domain, mailgun.Region);
            }
        }
        return new MailboxResponse(mailbox.Id, mailbox.FromAddress, mailbox.DisplayName, mailbox.CreatedAt, mailbox.IsActive,
            mailbox.Provider, mailbox.IsDefault, mailbox.Credential is not null, settings);
    }

    private static bool IsUniqueViolation(DbUpdateException exception) =>
        exception.InnerException is Npgsql.PostgresException { SqlState: Npgsql.PostgresErrorCodes.UniqueViolation };

    private static (int Page, int PageSize) PageValues(int? page, int? pageSize) =>
        (Math.Max(page ?? 1, 1), Math.Clamp(pageSize ?? 25, 1, 100));

    private static IResult ValidationError(Dictionary<string, string[]> errors) => Error(StatusCodes.Status400BadRequest, "invalid_request", "The request is invalid.", errors);
    private static IResult Error(int status, string code, string message, IReadOnlyDictionary<string, string[]>? fields = null) => TypedResults.Json(new CommunicationErrorResponse(new CommunicationError(code, message, fields)), statusCode: status);
}