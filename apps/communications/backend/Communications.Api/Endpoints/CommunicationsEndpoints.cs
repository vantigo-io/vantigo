using System.Security.Claims;

using Asp.Versioning;

using Microsoft.AspNetCore.Antiforgery;
using Microsoft.EntityFrameworkCore;

using Vantigo.Communications.Api.Database.Accounts;
using Vantigo.Communications.Api.Database.Communications;
using Vantigo.Communications.Api.Endpoints.Auth;
using Vantigo.Communications.Api.Endpoints.Dtos;
using Vantigo.Communications.Api.Services;

namespace Vantigo.Communications.Api.Endpoints;

internal static class CommunicationsEndpoints
{
    internal static IEndpointRouteBuilder MapVersionedBusinessEndpoints(this IEndpointRouteBuilder endpoints)
    {
        var api = endpoints.NewVersionedApi().MapGroup("/api/v{version:apiVersion}").HasApiVersion(new ApiVersion(1));
        var business = api.MapGroup("").RequireAuthorization(AuthPolicies.Business);
        business.MapGet("/messages", ListMessages).WithSummary("List email messages");
        business.MapGet("/messages/{id:guid}", GetMessage).WithSummary("Get an email message");
        business.MapGet("/messages/{id:guid}/events", ListEvents).WithSummary("List append-only message events");

        // Creating with a service key is intentionally not protected by the cookie
        // policy. The handler below requires either the key or a Business session.
        api.MapPost("/messages", CreateMessage)
            .WithSummary("Queue an email message")
            .WithDescription("Requires a Business session with same-origin antiforgery protection or X-Vantigo-Api-Key. Idempotency-Key is required. SMTP is queued durably and delivery is at-least-once.")
            .AddOpenApiOperationTransformer((operation, _, _) =>
            {
                OpenApiMetadata.AddCreateHeaders(operation);
                return Task.CompletedTask;
            })
            .Produces<EmailCreateResponse>(StatusCodes.Status201Created)
            .Produces<EmailCreateResponse>(StatusCodes.Status200OK)
            .Produces<AuthErrorResponse>(StatusCodes.Status400BadRequest)
            .Produces<AuthErrorResponse>(StatusCodes.Status401Unauthorized)
            .Produces<AuthErrorResponse>(StatusCodes.Status409Conflict)
            .Produces<AuthErrorResponse>(StatusCodes.Status422UnprocessableEntity)
            .Produces<AuthErrorResponse>(StatusCodes.Status503ServiceUnavailable);

        var owner = api.MapGroup("").RequireAuthorization(AuthPolicies.Owner);
        owner.MapGet("/mailboxes", ListMailboxes);
        owner.MapGet("/mailboxes/{id:guid}", GetMailbox);
        owner.MapPost("/mailboxes", CreateMailbox).RequireAntiforgery();
        owner.MapGet("/suppressions", ListSuppressions);
        owner.MapGet("/suppressions/{id:guid}", GetSuppression);
        owner.MapPost("/suppressions", CreateSuppression).RequireAntiforgery();
        owner.MapDelete("/suppressions/{id:guid}", DeleteSuppression).RequireAntiforgery();
        return endpoints;
    }

    private static async Task<IResult> ListMessages(int? page, int? pageSize, CommunicationsDbContext db, CancellationToken cancellationToken)
    {
        var (currentPage, size) = PageValues(page, pageSize);
        var query = db.EmailMessages.AsNoTracking();
        var total = await query.CountAsync(cancellationToken);
        var items = await query.OrderByDescending(message => message.CreatedAt)
            .Skip((currentPage - 1) * size).Take(size)
            .Select(message => new MessageListItem(message.Id, message.Subject, message.CreatedAt,
                message.Deliveries.Count, message.Deliveries.All(delivery => delivery.Status == "relay_accepted") ? "relay_accepted" :
                    message.Deliveries.Any(delivery => delivery.Status == "submission_failed") ? "submission_failed" : "queued", message.Source))
            .ToListAsync(cancellationToken);
        return TypedResults.Ok(PaginatedResponse<MessageListItem>.Create(items, currentPage, size, total));
    }

    private static async Task<IResult> GetMessage(Guid id, CommunicationsDbContext db, CancellationToken cancellationToken)
    {
        var message = await db.EmailMessages.AsNoTracking().Include(item => item.Deliveries).Include(item => item.ExternalLinks)
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

    private static async Task<IResult> CreateMessage(
        CreateEmailRequest? request,
        HttpContext httpContext,
        IAntiforgery antiforgery,
        CustomersApiKeyValidator apiKeyValidator,
        CommunicationsDbContext db,
        CancellationToken cancellationToken)
    {
        var suppliedKey = httpContext.Request.Headers["X-Vantigo-Api-Key"].FirstOrDefault();
        var serviceAuthenticated = apiKeyValidator.IsValid(suppliedKey);
        var businessAuthenticated = httpContext.User.Identity?.IsAuthenticated == true &&
            (httpContext.User.IsInRole(AuthRoles.Owner) || httpContext.User.IsInRole(AuthRoles.User));
        if (!serviceAuthenticated && !businessAuthenticated)
            return Error(StatusCodes.Status401Unauthorized, "unauthenticated", "A Business session or service API key is required.");
        if (!serviceAuthenticated && businessAuthenticated)
        {
            try { await antiforgery.ValidateRequestAsync(httpContext); }
            catch (AntiforgeryValidationException)
            { return Error(StatusCodes.Status400BadRequest, "csrf_validation_failed", "A valid X-XSRF-TOKEN header and antiforgery cookie are required."); }
        }

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
                return await PersistMessageAsync(request!, key, payloadFingerprint, httpContext, db, cancellationToken);
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

        var mailbox = await db.SharedMailboxes.Where(item => item.IsActive).OrderBy(item => item.CreatedAt).FirstOrDefaultAsync(cancellationToken);
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
            message.ExternalLinks.Add(new ExternalEntityLink
            {
                Id = Guid.NewGuid(),
                MessageId = message.Id,
                SourceSystem = link!.SourceSystem!,
                SourceInstance = link.SourceInstance!,
                EntityType = link.EntityType!,
                ExternalEntityId = link.ExternalEntityId!,
                DisplayLabel = string.IsNullOrEmpty(link.DisplayLabel) ? null : link.DisplayLabel,
            });
        message.Events.Add(new MessageEvent { Id = Guid.NewGuid(), MessageId = message.Id, EventType = "message_queued", OccurredAt = now });
        db.EmailMessages.Add(message);
        db.IdempotencyRecords.Add(new IdempotencyRecord { Id = Guid.NewGuid(), Key = key, PayloadFingerprint = payloadFingerprint, MessageId = message.Id, CreatedAt = now });
        db.OutboxJobs.Add(new OutboxJob { Id = Guid.NewGuid(), MessageId = message.Id, NextAttemptAt = now, CreatedAt = now });
        await db.SaveChangesAsync(cancellationToken);
        await transaction.CommitAsync(cancellationToken);
        return TypedResults.Created($"/api/v1/messages/{message.Id}", new EmailCreateResponse(message.Id, "queued", key));
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

    private static async Task<IResult> ListMailboxes(CommunicationsDbContext db, CancellationToken cancellationToken) =>
        TypedResults.Ok(await db.SharedMailboxes.AsNoTracking().OrderBy(mailbox => mailbox.CreatedAt)
            .Select(mailbox => new MailboxResponse(mailbox.Id, mailbox.FromAddress, mailbox.DisplayName, mailbox.CreatedAt, mailbox.IsActive)).ToListAsync(cancellationToken));

    private static async Task<IResult> GetMailbox(Guid id, CommunicationsDbContext db, CancellationToken cancellationToken)
    {
        var mailbox = await db.SharedMailboxes.AsNoTracking().SingleOrDefaultAsync(item => item.Id == id, cancellationToken);
        return mailbox is null ? TypedResults.NotFound() : TypedResults.Ok(new MailboxResponse(mailbox.Id, mailbox.FromAddress, mailbox.DisplayName, mailbox.CreatedAt, mailbox.IsActive));
    }

    private static async Task<IResult> CreateMailbox(CreateMailboxRequest? request, CommunicationsDbContext db, CancellationToken cancellationToken)
    {
        var errors = CommunicationValidation.ValidateMailbox(request);
        if (errors.Count > 0) return ValidationError(errors);
        if (await db.SharedMailboxes.AnyAsync(cancellationToken))
            return Error(StatusCodes.Status409Conflict, "mailbox_already_configured", "Only one shared mailbox may be configured.");
        var now = DateTimeOffset.UtcNow;
        var mailbox = new SharedMailbox { Id = Guid.NewGuid(), FromAddress = request!.FromAddress!, DisplayName = string.IsNullOrEmpty(request.DisplayName) ? null : request.DisplayName, CreatedAt = now };
        db.SharedMailboxes.Add(mailbox);
        await db.SaveChangesAsync(cancellationToken);
        return TypedResults.Created($"/api/v1/mailboxes/{mailbox.Id}", new MailboxResponse(mailbox.Id, mailbox.FromAddress, mailbox.DisplayName, mailbox.CreatedAt, mailbox.IsActive));
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
        return TypedResults.Created($"/api/v1/suppressions/{suppression.Id}", new SuppressionResponse(suppression.Id, suppression.NormalizedEmailAddress, suppression.Reason, suppression.CreatedAt));
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
        message.Id, message.MailboxId, message.Subject, message.TextBody, message.HtmlBody, message.CreatedAt, message.Source,
        message.Deliveries.OrderBy(delivery => delivery.CreatedAt).Select(delivery => new DeliveryResponse(delivery.Id, delivery.EmailAddress, delivery.RecipientType, delivery.Status, delivery.Attempts, delivery.LastError, delivery.AcceptedAt)).ToArray(),
        message.ExternalLinks.Select(link => new ExternalEntityLinkResponse(link.Id, link.SourceSystem, link.SourceInstance, link.EntityType, link.ExternalEntityId, link.DisplayLabel)).ToArray());

    private static (int Page, int PageSize) PageValues(int? page, int? pageSize) =>
        (Math.Max(page ?? 1, 1), Math.Clamp(pageSize ?? 25, 1, 100));

    private static IResult ValidationError(Dictionary<string, string[]> errors) => Error(StatusCodes.Status400BadRequest, "invalid_request", "The request is invalid.", errors);
    private static IResult Error(int status, string code, string message, IReadOnlyDictionary<string, string[]>? fields = null) => TypedResults.Json(new AuthErrorResponse(new AuthError(code, message, fields)), statusCode: status);
}