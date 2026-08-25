using System.Net;
using System.Security.Cryptography;
using System.Text;
using System.Text.Json;

using Microsoft.AspNetCore.Http.Features;
using Microsoft.AspNetCore.Mvc;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.Options;

using MimeKit;

using Vantigo.Communications.Database.Communications;
using Vantigo.Communications.Infrastructure.Storage;
using Vantigo.Communications.Services;
using Vantigo.Contracts.Web;
using Vantigo.Storage.Abstractions;
using Vantigo.Tenancy;
using Vantigo.Tenancy.Abstractions;

namespace Vantigo.Communications.Endpoints;

internal static class MailgunInboundEndpoints
{
    private const string Provider = "mailgun";

    internal static void MapMailgunInboundEndpoint(this IEndpointRouteBuilder endpoints)
    {
        var options = endpoints.ServiceProvider.GetRequiredService<IOptions<MailgunInboundOptions>>().Value;
        endpoints.MapPost("/api/v1/communications/inbound/mailgun/{channelId:guid}", ReceiveAsync)
            .WithMetadata(new SkipAntiforgeryAttribute(), new RequestSizeLimitAttribute(options.MaxRequestBytes),
                new RequestFormLimitsAttribute { MultipartBodyLengthLimit = options.MaxRequestBytes, ValueLengthLimit = options.MaxBodyBytes, ValueCountLimit = options.MaxFormKeys, MultipartBoundaryLengthLimit = 256, MemoryBufferThreshold = 64 * 1024 });
    }

    private static async Task<IResult> ReceiveAsync(Guid channelId, HttpContext http, CommunicationsDbContext db,
        MailboxCredentialProtector protector, IObjectStore<CommunicationsStorageScope> objectStore, IOptions<MailgunInboundOptions> options,
        MailgunInboundThrottle throttle, CancellationToken cancellationToken)
    {
        var limit = options.Value;
        if (!HttpMethods.IsPost(http.Request.Method) ||
            (!http.Request.HasFormContentType && !string.Equals(http.Request.ContentType?.Split(';')[0].Trim(), "application/x-www-form-urlencoded", StringComparison.OrdinalIgnoreCase)))
            return TypedResults.StatusCode(StatusCodes.Status406NotAcceptable);
        if (http.Request.ContentLength is > 0 and var contentLength && contentLength > limit.MaxRequestBytes)
            return TypedResults.StatusCode(StatusCodes.Status406NotAcceptable);
        var remote = http.Connection.RemoteIpAddress?.ToString() ?? "unknown";

        // A per-IP probe limit runs before anything keyed by the caller-chosen
        // channel id, and per-channel throttle state is only allocated for
        // channels that actually exist, so unknown-channel floods can neither
        // grow limiter state nor turn into a database lookup per request.
        if (!throttle.TryTake($"probe|{remote}", 60, TimeSpan.FromMinutes(1)))
            return TypedResults.StatusCode(StatusCodes.Status429TooManyRequests);
        var channelIsKnown = await throttle.IsKnownChannelAsync(channelId, () =>
            db.Channels.IgnoreQueryFilters().AsNoTracking()
                .AnyAsync(item => item.Id == channelId && item.IsActive && item.Type == "email" && item.Provider == Provider, cancellationToken));
        if (!channelIsKnown)
            return TypedResults.StatusCode(StatusCodes.Status406NotAcceptable);

        var gateKey = $"{channelId:N}|{remote}";
        if (!throttle.TryTake(gateKey, 20, TimeSpan.FromMinutes(1))) return TypedResults.StatusCode(StatusCodes.Status429TooManyRequests);
        var gate = throttle.GetGate(gateKey);
        if (!await gate.WaitAsync(TimeSpan.FromSeconds(2), cancellationToken)) return TypedResults.StatusCode(StatusCodes.Status429TooManyRequests);
        try { return await ReceiveCoreAsync(channelId, http, db, protector, objectStore, limit, cancellationToken); }
        finally { gate.Release(); }
    }

    private static async Task<IResult> ReceiveCoreAsync(Guid channelId, HttpContext http, CommunicationsDbContext db,
        MailboxCredentialProtector protector, IObjectStore<CommunicationsStorageScope> objectStore, MailgunInboundOptions limit, CancellationToken cancellationToken)
    {

        IFormCollection form;
        try
        {
            form = await http.Request.ReadFormAsync(new FormOptions
            {
                MultipartBodyLengthLimit = limit.MaxRequestBytes,
                ValueLengthLimit = limit.MaxBodyBytes,
                KeyLengthLimit = 256,
                ValueCountLimit = limit.MaxFormKeys,
                MultipartBoundaryLengthLimit = 256,
                MemoryBufferThreshold = 64 * 1024,
            }, cancellationToken);
        }
        catch (InvalidDataException) { return TypedResults.StatusCode(StatusCodes.Status406NotAcceptable); }
        catch (IOException) { return TypedResults.StatusCode(StatusCodes.Status406NotAcceptable); }

        var timestamp = Scalar(form, "timestamp", 32);
        var token = Scalar(form, "token", 256);
        var signature = Scalar(form, "signature", 64);
        // System-context discovery; tenant scope is entered before processing.
        // Resolve only the target channel's id and tenant before reading credentials
        // or writing any tenant-owned rows.
        var target = await db.Channels.IgnoreQueryFilters().AsNoTracking()
            .Where(item => item.Id == channelId && item.IsActive && item.Type == "email" && item.Provider == Provider)
            .Select(item => new { item.Id, item.TenantId })
            .SingleOrDefaultAsync(cancellationToken);
        if (target is null || timestamp is null || token is null || signature is null)
            return TypedResults.StatusCode(StatusCodes.Status406NotAcceptable);

        using var tenantScope = AmbientTenantContext.Enter(new TenantId(target.TenantId));
        var channel = await db.Channels.Include(item => item.Credential)
            .SingleOrDefaultAsync(item => item.Id == target.Id && item.IsActive && item.Type == "email" && item.Provider == Provider && item.Credential != null, cancellationToken);
        if (channel is null)
            return TypedResults.StatusCode(StatusCodes.Status406NotAcceptable);

        MailgunCredentialSecrets secrets;
        try { secrets = MailgunCredentialSecretReader.Read(protector, channel.Credential!); }
        catch (Exception) { return TypedResults.StatusCode(StatusCodes.Status406NotAcceptable); }
        if (!MailgunSignatureVerifier.Verify(timestamp, token, signature, secrets.InboundSigningKey ?? string.Empty,
            DateTimeOffset.UtcNow, TimeSpan.FromSeconds(Math.Clamp(limit.MaxPastSkewSeconds, 1, 3600)), TimeSpan.FromSeconds(Math.Clamp(limit.MaxFutureSkewSeconds, 1, 3600))))
            return TypedResults.StatusCode(StatusCodes.Status406NotAcceptable);

        var reservationResult = await ReserveReceiptAsync(channelId, token!, db, cancellationToken);
        if (reservationResult.Acknowledge) return TypedResults.Ok();
        if (reservationResult.Retryable || reservationResult.Receipt is null || reservationResult.Cleanup is null)
            return TypedResults.StatusCode(StatusCodes.Status503ServiceUnavailable);
        var reservation = reservationResult.Receipt;
        var stagedCleanup = reservationResult.Cleanup;
        var storageKey = stagedCleanup.StorageKey;

        var attachments = new List<MailgunFormAttachment>();
        long aggregateAttachmentBytes = 0;
        try
        {
            foreach (var file in form.Files)
            {
                if (attachments.Count >= limit.MaxAttachments || file.Length > limit.MaxAttachmentBytes)
                    return await RejectReservationAsync(db, reservation, stagedCleanup, StatusCodes.Status406NotAcceptable, cancellationToken);
                await using var source = file.OpenReadStream();
                await using var buffer = new MemoryStream();
                await source.CopyToAsync(buffer, cancellationToken);
                aggregateAttachmentBytes += buffer.Length;
                if (buffer.Length > limit.MaxAttachmentBytes || aggregateAttachmentBytes > limit.MaxAggregateAttachmentBytes)
                    return await RejectReservationAsync(db, reservation, stagedCleanup, StatusCodes.Status406NotAcceptable, cancellationToken);
                attachments.Add(new MailgunFormAttachment(SafeFileName(file.FileName), file.ContentType, buffer.ToArray()));
            }
        }
        catch (Exception) when (!cancellationToken.IsCancellationRequested)
        {
            await ReleaseReservationAsync(db, reservation.Id, cancellationToken);
            return TypedResults.StatusCode(StatusCodes.Status503ServiceUnavailable);
        }

        var formMessage = new MailgunInboundForm(timestamp, token, signature, Scalar(form, "sender", 998), Scalar(form, "from", 998),
            Scalar(form, "recipient", 998), Scalar(form, "cc", 998), Scalar(form, "subject", 998), Body(form, "body-plain", limit.MaxBodyBytes),
            Body(form, "body-html", limit.MaxBodyBytes), Body(form, "body-mime", limit.MaxMimeBytes), Body(form, "message-headers", limit.MaxHeaderBytes), attachments);
        if (HasOversizedBody(form, "body-plain", limit.MaxBodyBytes) || HasOversizedBody(form, "body-html", limit.MaxBodyBytes) || HasOversizedBody(form, "body-mime", limit.MaxMimeBytes) || HasOversizedBody(form, "message-headers", limit.MaxHeaderBytes))
            return await RejectReservationAsync(db, reservation, stagedCleanup, StatusCodes.Status406NotAcceptable, cancellationToken);
        if (!TryParseMailbox(formMessage.Sender, out _) && !TryParseMailbox(formMessage.From, out _))
            return await RejectReservationAsync(db, reservation, stagedCleanup, StatusCodes.Status406NotAcceptable, cancellationToken);
        byte[] rawMime;
        var synthetic = string.IsNullOrWhiteSpace(formMessage.Mime);
        try
        {
            rawMime = synthetic ? MailgunInboundMimeBuilder.CreateSynthetic(formMessage, DateTimeOffset.UtcNow) : Encoding.UTF8.GetBytes(formMessage.Mime!);
            if (rawMime.Length > limit.MaxMimeBytes) return await RejectReservationAsync(db, reservation, stagedCleanup, StatusCodes.Status406NotAcceptable, cancellationToken);
        }
        catch (Exception) { return await RejectReservationAsync(db, reservation, stagedCleanup, StatusCodes.Status406NotAcceptable, cancellationToken); }

        try
        {
            await using var content = new MemoryStream(rawMime, writable: false);
            await objectStore.PutAsync(storageKey, content, "message/rfc822", cancellationToken);
        }
        catch (Exception)
        {
            await ReleaseReservationAsync(db, reservation.Id, cancellationToken);
            return TypedResults.StatusCode(StatusCodes.Status503ServiceUnavailable);
        }

        var now = DateTimeOffset.UtcNow;
        var rfcMessageId = TryReadMessageId(formMessage.Headers) ?? $"hash-{Convert.ToHexString(SHA256.HashData(rawMime)).ToLowerInvariant()}@mailgun.invalid";
        reservation.RfcMessageId = rfcMessageId;
        reservation.PayloadHash = Convert.ToHexString(SHA256.HashData(rawMime)).ToLowerInvariant();
        reservation.Status = "reserved";
        var job = new InboundEmailJob { Id = reservation.Id, ChannelId = channelId, InboundReceiptId = reservation.Id, RawMimeStorageKey = storageKey, Status = "pending", NextAttemptAt = now, EnvelopeSenderAddress = CleanAddress(formMessage.Sender), EnvelopeRecipientAddress = CleanAddress(formMessage.Recipient), IsSynthetic = synthetic, ReceivedAt = now, CreatedAt = now, InboundReceipt = reservation };
        db.InboundEmailJobs.Add(job);
        try
        {
            await ObjectOwnershipLifecycle.MarkOwnedAsync(db, [storageKey], cancellationToken);
            await db.SaveChangesAsync(cancellationToken);
            return TypedResults.Ok();
        }
        catch (DbUpdateException exception) when (IsUniqueViolation(exception))
        {
            db.ChangeTracker.Clear();
            return await ResolveUniqueInboundWriteAsync(channelId, reservation.Id, rfcMessageId, storageKey, db, cancellationToken);
        }
        catch (Exception)
        {
            await ReleaseReservationAsync(db, reservation.Id, cancellationToken);
            return TypedResults.StatusCode(StatusCodes.Status503ServiceUnavailable);
        }
    }

    private static string? Scalar(IFormCollection form, string name, int limit)
    {
        var value = form[name].FirstOrDefault();
        return value is null || value.Length > limit || value.Any(char.IsControl) ? null : value;
    }

    private static string? Body(IFormCollection form, string name, int byteLimit)
    {
        var value = form[name].FirstOrDefault();
        return value is null || Encoding.UTF8.GetByteCount(value) > byteLimit ? null : value;
    }

    private static bool HasOversizedBody(IFormCollection form, string name, int byteLimit)
        => form.TryGetValue(name, out var values) && values.Any(value => value is not null && Encoding.UTF8.GetByteCount(value) > byteLimit);

    private static bool TryParseMailbox(string? value, out MailboxAddress? mailbox)
    {
        mailbox = null;
        if (string.IsNullOrWhiteSpace(value) || value.Length > 998) return false;
        try { mailbox = MailboxAddress.Parse(value); return !string.IsNullOrWhiteSpace(mailbox.Address); }
        catch (ParseException) { return false; }
    }

    private static async Task<IResult> RejectReservationAsync(CommunicationsDbContext db, InboundReceipt reservation, AttachmentCleanupRecord cleanup, int status, CancellationToken cancellationToken)
    {
        reservation.Status = "failed";
        reservation.ReservationExpiresAt = DateTimeOffset.UtcNow;
        cleanup.Status = "pending";
        cleanup.NextAttemptAt = DateTimeOffset.UtcNow;
        await db.SaveChangesAsync(cancellationToken);
        return TypedResults.StatusCode(status);
    }

    private static async Task<int> ReleaseReservationAsync(CommunicationsDbContext db, Guid reservationId, CancellationToken cancellationToken)
    {
        var receipt = await db.InboundReceipts.AsNoTracking().SingleOrDefaultAsync(item => item.Id == reservationId, cancellationToken);
        var released = await db.InboundReceipts.Where(item => item.Id == reservationId).ExecuteUpdateAsync(setters => setters
            .SetProperty(item => item.Status, "failed")
            .SetProperty(item => item.ReservationExpiresAt, DateTimeOffset.UtcNow), cancellationToken);
        if (receipt is not null)
        {
            var storageKey = $"inbound/{receipt.ChannelId:N}/{reservationId:N}.eml";
            await db.AttachmentCleanupRecords.Where(item => item.StorageKey == storageKey && item.Status == "staged")
                .ExecuteUpdateAsync(setters => setters.SetProperty(item => item.Status, "pending").SetProperty(item => item.NextAttemptAt, DateTimeOffset.UtcNow), cancellationToken);
        }
        return released;
    }

    private static async Task<ReceiptReservationResult> ReserveReceiptAsync(Guid channelId, string providerEventId,
        CommunicationsDbContext db, CancellationToken cancellationToken)
    {
        for (var attempt = 0; attempt < 2; attempt++)
        {
            await using var transaction = await db.Database.BeginTransactionAsync(cancellationToken);
            var existing = await db.InboundReceipts.SingleOrDefaultAsync(item => item.ChannelId == channelId && item.Provider == Provider && item.ProviderEventId == providerEventId, cancellationToken);
            if (existing?.Status == "completed")
            {
                await transaction.CommitAsync(cancellationToken);
                return ReceiptReservationResult.Ack;
            }

            var existingJob = existing is null ? null : await db.InboundEmailJobs.AsNoTracking().SingleOrDefaultAsync(item => item.InboundReceiptId == existing.Id, cancellationToken);
            if (existingJob is not null && IsDurableJob(existingJob))
            {
                await transaction.CommitAsync(cancellationToken);
                return ReceiptReservationResult.Ack;
            }

            if (existing is not null && existing.Status == "reserved" && existing.ReservationExpiresAt > DateTimeOffset.UtcNow)
            {
                await transaction.CommitAsync(cancellationToken);
                return new ReceiptReservationResult(null, null, false, true);
            }

            // A reservation without a usable job is not an acknowledgement. Remove it
            // while holding the transaction so a retry can atomically take ownership.
            if (existing is not null)
            {
                var orphanStorageKey = $"inbound/{channelId:N}/{existing.Id:N}.eml";
                await db.AttachmentCleanupRecords.Where(item => item.StorageKey == orphanStorageKey && item.Status != "completed")
                    .ExecuteUpdateAsync(setters => setters.SetProperty(item => item.Status, "pending").SetProperty(item => item.NextAttemptAt, DateTimeOffset.UtcNow), cancellationToken);
                db.InboundReceipts.Remove(existing);
            }
            var now = DateTimeOffset.UtcNow;
            var receipt = new InboundReceipt { Id = Guid.NewGuid(), ChannelId = channelId, Provider = Provider, ProviderEventId = providerEventId, Status = "reserved", ReservationExpiresAt = now.AddMinutes(10), ReceivedAt = now };
            var storageKey = $"inbound/{channelId:N}/{receipt.Id:N}.eml";
            var cleanup = new AttachmentCleanupRecord
            {
                Id = Guid.NewGuid(),
                MessageId = null,
                StorageKey = storageKey,
                Status = "staged",
                NextAttemptAt = now,
                ReservationExpiresAt = receipt.ReservationExpiresAt,
                CreatedAt = now,
            };
            db.InboundReceipts.Add(receipt);
            db.AttachmentCleanupRecords.Add(cleanup);
            try
            {
                await db.SaveChangesAsync(cancellationToken);
                await transaction.CommitAsync(cancellationToken);
                return new ReceiptReservationResult(receipt, cleanup, false, false);
            }
            catch (DbUpdateException exception) when (IsUniqueViolation(exception))
            {
                await transaction.RollbackAsync(cancellationToken);
                db.ChangeTracker.Clear();
            }
        }
        return new ReceiptReservationResult(null, null, false, true);
    }

    private static async Task<IResult> ResolveUniqueInboundWriteAsync(Guid channelId, Guid reservationId, string? rfcMessageId,
        string storageKey, CommunicationsDbContext db, CancellationToken cancellationToken)
    {
        await using var transaction = await db.Database.BeginTransactionAsync(cancellationToken);
        db.ChangeTracker.Clear();
        var receipt = await db.InboundReceipts.SingleOrDefaultAsync(item => item.Id == reservationId, cancellationToken);
        if (receipt?.Status == "completed")
        {
            await transaction.CommitAsync(cancellationToken);
            return TypedResults.Ok();
        }
        var job = receipt is null ? null : await db.InboundEmailJobs.AsNoTracking().SingleOrDefaultAsync(item => item.InboundReceiptId == reservationId, cancellationToken);
        if (job is not null && IsDurableJob(job))
        {
            await transaction.CommitAsync(cancellationToken);
            return TypedResults.Ok();
        }

        if (receipt is not null && !string.IsNullOrWhiteSpace(rfcMessageId))
        {
            var duplicateMessageId = await db.InboundReceipts.AsNoTracking()
                .Where(item => item.ChannelId == channelId && item.Provider == Provider && item.RfcMessageId == rfcMessageId && item.Id != reservationId && item.ConversationMessageId.HasValue)
                .Select(item => item.ConversationMessageId)
                .FirstOrDefaultAsync(cancellationToken);
            if (duplicateMessageId.HasValue)
            {
                receipt.ConversationMessageId = duplicateMessageId;
                receipt.Status = "completed";
                receipt.ReservationExpiresAt = null;
                await db.SaveChangesAsync(cancellationToken);
                await transaction.CommitAsync(cancellationToken);
                return TypedResults.Ok();
            }
        }

        if (receipt is not null)
        {
            receipt.Status = "failed";
            receipt.ReservationExpiresAt = DateTimeOffset.UtcNow;
            await db.SaveChangesAsync(cancellationToken);
        }
        await db.AttachmentCleanupRecords.Where(item => item.StorageKey == storageKey && item.Status == "staged")
            .ExecuteUpdateAsync(setters => setters.SetProperty(item => item.Status, "pending").SetProperty(item => item.NextAttemptAt, DateTimeOffset.UtcNow), cancellationToken);
        await transaction.CommitAsync(cancellationToken);
        return TypedResults.StatusCode(StatusCodes.Status503ServiceUnavailable);
    }

    private static bool IsDurableJob(InboundEmailJob job) => !string.IsNullOrWhiteSpace(job.RawMimeStorageKey) && job.Status is "pending" or "retry" or "processing" or "completed";

    private sealed record ReceiptReservationResult(InboundReceipt? Receipt, AttachmentCleanupRecord? Cleanup, bool Acknowledge, bool Retryable)
    {
        internal static ReceiptReservationResult Ack { get; } = new(null, null, true, false);
    }

    private static string? TryReadMessageId(string? encodedHeaders)
    {
        if (string.IsNullOrWhiteSpace(encodedHeaders) || encodedHeaders.Length > 512 * 1024) return null;
        try
        {
            using var document = JsonDocument.Parse(encodedHeaders);
            if (document.RootElement.ValueKind == JsonValueKind.Object)
            {
                foreach (var property in document.RootElement.EnumerateObject())
                    if (property.Name.Equals("Message-Id", StringComparison.OrdinalIgnoreCase) || property.Name.Equals("Message-ID", StringComparison.OrdinalIgnoreCase))
                        return NormalizeMessageId(property.Value.ToString());
            }
        }
        catch (JsonException) { }
        return null;
    }

    private static string? NormalizeMessageId(string? value) => string.IsNullOrWhiteSpace(value) ? null : EmailThreadResolver.NormalizeMessageId(value);

    private static string? CleanAddress(string? value) => string.IsNullOrWhiteSpace(value) ? null : value.Trim().Length <= 320 ? value.Trim() : value.Trim()[..320];
    private static string SafeFileName(string value) { var name = Path.GetFileName(value).Trim(); return string.IsNullOrWhiteSpace(name) ? "attachment" : name.Length <= 500 ? name : name[..500]; }
    private static bool IsUniqueViolation(DbUpdateException exception) => exception.InnerException is Npgsql.PostgresException { SqlState: Npgsql.PostgresErrorCodes.UniqueViolation };
}