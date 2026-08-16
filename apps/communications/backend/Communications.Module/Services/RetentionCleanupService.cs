using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.Options;

using Vantigo.Communications.Database.Communications;
using Vantigo.Communications.Infrastructure.Storage;
using Vantigo.Configuration;
using Vantigo.Storage.Abstractions;
using Vantigo.Tenancy;
using Vantigo.Tenancy.Abstractions;

namespace Vantigo.Communications.Services;

/// <summary>
/// Deletes only terminal communication history. Queued and retryable work is
/// intentionally excluded so retention cannot interrupt delivery.
/// </summary>
public sealed class RetentionCleanupService(
    CommunicationsDbContext db,
    ITenantDirectory tenantDirectory,
    ILogger<RetentionCleanupService> logger,
    IOptions<CommunicationsOptions> options)
{
    private readonly CommunicationsOptions communications = options.Value;

    public async Task<int> CleanupBatchAsync(DateTimeOffset now, CancellationToken cancellationToken)
    {
        var deleted = 0;
        // System-context discovery; tenant scope is entered before processing.
        // The directory is the authoritative bounded list of active tenants.
        foreach (var tenant in await tenantDirectory.GetActiveTenantsAsync(cancellationToken))
        {
            try
            {
                db.ChangeTracker.Clear();
                using var tenantScope = AmbientTenantContext.Enter(tenant);
                deleted += await CleanupTenantBatchAsync(now, cancellationToken);
            }
            catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested) { throw; }
            catch (Exception exception)
            {
                logger.LogError(exception, "Communications retention cleanup failed for tenant {TenantId}.", tenant.Value);
            }
            finally { db.ChangeTracker.Clear(); }
        }

        return deleted;
    }

    private async Task<int> CleanupTenantBatchAsync(DateTimeOffset now, CancellationToken cancellationToken)
    {
        var days = Math.Max(1, communications.Retention.Days);
        var batchSize = Math.Clamp(communications.Retention.BatchSize, 1, 1000);
        var cutoff = now.AddDays(-days);
        await using var transaction = await db.Database.BeginTransactionAsync(cancellationToken);
        var messageIds = await db.ConversationMessages.AsNoTracking()
            .Where(message => message.CreatedAt < cutoff &&
                (!db.OutboxJobs.Any(job => job.MessageId == message.Id) ||
                 db.OutboxJobs.Where(job => job.MessageId == message.Id)
                    .All(job => job.Status == "completed" || job.Status == "cancelled" || job.Status == "failed")))
            .OrderBy(message => message.CreatedAt)
            .Select(message => message.Id)
            .Take(batchSize)
            .ToListAsync(cancellationToken);

        // Retention owns terminal inbound jobs and receipts independently of a
        // ConversationMessage. Failed jobs commonly have no message at all.
        var terminalJobs = await db.InboundEmailJobs.AsNoTracking()
            .Where(job => (job.Status == "completed" || job.Status == "failed") &&
                (job.CompletedAt ?? job.CreatedAt) < cutoff)
            .OrderBy(job => job.CompletedAt ?? job.CreatedAt)
            .Take(batchSize)
            .Select(job => new { job.Id, job.InboundReceiptId, job.RawMimeStorageKey })
            .ToListAsync(cancellationToken);
        var terminalReceipts = await db.InboundReceipts.AsNoTracking()
            .Where(receipt => (receipt.Status == "completed" || receipt.Status == "failed") && receipt.ReceivedAt < cutoff &&
                !db.InboundEmailJobs.Any(job => job.InboundReceiptId == receipt.Id))
            .OrderBy(receipt => receipt.ReceivedAt)
            .Take(batchSize)
            .Select(receipt => new { receipt.Id, receipt.ChannelId })
            .ToListAsync(cancellationToken);
        var receiptIds = terminalJobs.Select(job => job.InboundReceiptId)
            .Concat(terminalReceipts.Select(receipt => receipt.Id))
            .Distinct()
            .ToArray();
        var inboundJobIds = terminalJobs.Select(job => job.Id).ToArray();

        await db.MessageEvents.Where(item => messageIds.Contains(item.MessageId)).ExecuteDeleteAsync(cancellationToken);
        await db.MessageDeliveries.Where(item => messageIds.Contains(item.MessageId)).ExecuteDeleteAsync(cancellationToken);
        var attachments = await db.MessageAttachments.Where(item => messageIds.Contains(item.MessageId)).ToListAsync(cancellationToken);
        var rawKeys = await db.ConversationMessages
            .Where(item => messageIds.Contains(item.Id) && item.RawPayloadStorageKey != null)
            .Select(item => new { item.Id, Key = item.RawPayloadStorageKey! })
            .ToListAsync(cancellationToken);
        foreach (var attachment in attachments)
            await ObjectOwnershipLifecycle.QueueForDeletionAsync(db, attachment.StorageKey, attachment.MessageId, now, cancellationToken);
        var rawStorageKeys = new HashSet<string>(rawKeys.Select(raw => raw.Key), StringComparer.Ordinal);
        foreach (var job in terminalJobs)
            if (!string.IsNullOrWhiteSpace(job.RawMimeStorageKey)) rawStorageKeys.Add(job.RawMimeStorageKey);
        foreach (var receipt in terminalReceipts)
            rawStorageKeys.Add($"inbound/{receipt.ChannelId:N}/{receipt.Id:N}.eml");
        foreach (var key in rawStorageKeys)
            await ObjectOwnershipLifecycle.QueueForDeletionAsync(db, key, rawKeys.FirstOrDefault(raw => raw.Key == key)?.Id, now, cancellationToken);
        // Persist every reservation before deleting the rows that identify its owner.
        await db.SaveChangesAsync(cancellationToken);
        await db.MessageAttachments.Where(item => messageIds.Contains(item.MessageId)).ExecuteDeleteAsync(cancellationToken);
        await db.IdempotencyRecords.Where(item => messageIds.Contains(item.MessageId)).ExecuteDeleteAsync(cancellationToken);
        await db.OutboxJobs.Where(item => messageIds.Contains(item.MessageId)).ExecuteDeleteAsync(cancellationToken);
        var deleted = await db.ConversationMessages.Where(item => messageIds.Contains(item.Id)).ExecuteDeleteAsync(cancellationToken);
        if (inboundJobIds.Length > 0)
            deleted += await db.InboundEmailJobs.Where(item => inboundJobIds.Contains(item.Id)).ExecuteDeleteAsync(cancellationToken);
        if (receiptIds.Length > 0)
            deleted += await db.InboundReceipts.Where(item => receiptIds.Contains(item.Id)).ExecuteDeleteAsync(cancellationToken);
        await db.ConversationParticipants.Where(item => !db.ConversationMessages.Any(message => message.ConversationId == item.ConversationId)).ExecuteDeleteAsync(cancellationToken);
        await db.Participants.Where(item => !db.ConversationParticipants.Any(link => link.ParticipantId == item.Id)).ExecuteDeleteAsync(cancellationToken);
        await transaction.CommitAsync(cancellationToken);
        return deleted;
    }
}

public sealed class AttachmentCleanupService(
    CommunicationsDbContext db,
    ITenantDirectory tenantDirectory,
    ILogger<AttachmentCleanupService> logger,
    IObjectStore<CommunicationsStorageScope> objectStore)
{
    public async Task<int> CleanupBatchAsync(DateTimeOffset now, CancellationToken cancellationToken)
    {
        var cleaned = 0;
        // System-context discovery; tenant scope is entered before processing.
        // Cleanup is intentionally iterated from the active tenant directory.
        foreach (var tenant in await tenantDirectory.GetActiveTenantsAsync(cancellationToken))
        {
            try
            {
                db.ChangeTracker.Clear();
                using var tenantScope = AmbientTenantContext.Enter(tenant);
                cleaned += await CleanupTenantBatchAsync(now, cancellationToken);
            }
            catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested) { throw; }
            catch (Exception exception)
            {
                logger.LogError(exception, "Communications object cleanup failed for tenant {TenantId}.", tenant.Value);
            }
            finally { db.ChangeTracker.Clear(); }
        }

        return cleaned;
    }

    private async Task<int> CleanupTenantBatchAsync(DateTimeOffset now, CancellationToken cancellationToken)
    {

        var candidates = await db.AttachmentCleanupRecords
            .Where(item => (item.Status == ObjectOwnershipLifecycle.Pending && item.NextAttemptAt <= now) ||
                (item.Status == ObjectOwnershipLifecycle.Staged && item.ReservationExpiresAt <= now) ||
                (item.Status == ObjectOwnershipLifecycle.Deleting && item.LeaseUntil <= now))
            .OrderBy(item => item.CreatedAt).Take(100).Select(item => item.Id).ToListAsync(cancellationToken);
        var records = new List<AttachmentCleanupRecord>();
        foreach (var id in candidates)
        {
            var lease = Guid.NewGuid().ToString("N");
            var claimed = await db.AttachmentCleanupRecords.Where(item => item.Id == id &&
                ((item.Status == ObjectOwnershipLifecycle.Pending && item.NextAttemptAt <= now) ||
                 (item.Status == ObjectOwnershipLifecycle.Staged && item.ReservationExpiresAt <= now) ||
                 (item.Status == ObjectOwnershipLifecycle.Deleting && item.LeaseUntil <= now)))
                .ExecuteUpdateAsync(setters => setters.SetProperty(item => item.Status, ObjectOwnershipLifecycle.Deleting)
                    .SetProperty(item => item.LeaseId, lease).SetProperty(item => item.LeaseUntil, now.AddMinutes(5)), cancellationToken);
            if (claimed == 0) continue;
            var record = await db.AttachmentCleanupRecords.SingleAsync(item => item.Id == id, cancellationToken);
            records.Add(record);
        }
        foreach (var record in records)
        {
            try
            {
                await objectStore.DeleteAsync(record.StorageKey, cancellationToken);
                record.Status = ObjectOwnershipLifecycle.Completed;
                record.LeaseId = null;
                record.LeaseUntil = null;
            }
            catch (Exception) when (!cancellationToken.IsCancellationRequested)
            {
                record.Status = ObjectOwnershipLifecycle.Pending;
                record.Attempts++;
                record.LastError = "Object cleanup failed.";
                record.NextAttemptAt = now.AddSeconds(Math.Min(3600, Math.Pow(2, Math.Min(record.Attempts, 10))));
                record.LeaseId = null;
                record.LeaseUntil = null;
            }
        }
        await db.SaveChangesAsync(cancellationToken);
        return records.Count;
    }
}

public sealed class CommunicationsRetentionWorker(
    IServiceScopeFactory scopeFactory,
    ILogger<CommunicationsRetentionWorker> logger,
    IOptions<CommunicationsOptions> options) : BackgroundService
{
    private readonly CommunicationsOptions communications = options.Value;

    protected override async Task ExecuteAsync(CancellationToken stoppingToken)
    {
        var minutes = Math.Max(1, communications.Retention.PollMinutes);
        var delay = TimeSpan.FromMinutes(minutes);
        while (!stoppingToken.IsCancellationRequested)
        {
            try
            {
                await using var scope = scopeFactory.CreateAsyncScope();
                var cleanup = scope.ServiceProvider.GetRequiredService<RetentionCleanupService>();
                while (await cleanup.CleanupBatchAsync(DateTimeOffset.UtcNow, stoppingToken) > 0) { }
            }
            catch (OperationCanceledException) when (stoppingToken.IsCancellationRequested) { }
            catch (Exception exception) { logger.LogError(exception, "Communications retention cleanup failed."); }
            await Task.Delay(delay, stoppingToken);
        }
    }
}

public sealed class CommunicationsAttachmentCleanupWorker(IServiceScopeFactory scopeFactory, ILogger<CommunicationsAttachmentCleanupWorker> logger) : BackgroundService
{
    protected override async Task ExecuteAsync(CancellationToken stoppingToken)
    {
        while (!stoppingToken.IsCancellationRequested)
        {
            try
            {
                await using var scope = scopeFactory.CreateAsyncScope();
                await scope.ServiceProvider.GetRequiredService<AttachmentCleanupService>().CleanupBatchAsync(DateTimeOffset.UtcNow, stoppingToken);
            }
            catch (OperationCanceledException) when (stoppingToken.IsCancellationRequested) { }
            catch (Exception exception) { logger.LogError(exception, "Communications object cleanup failed."); }
            await Task.Delay(TimeSpan.FromMinutes(1), stoppingToken);
        }
    }
}