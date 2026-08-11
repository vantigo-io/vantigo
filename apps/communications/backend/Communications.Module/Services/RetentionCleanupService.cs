using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.Options;

using Vantigo.Communications.Database.Communications;
using Vantigo.Configuration;

namespace Vantigo.Communications.Services;

/// <summary>
/// Deletes only terminal communication history. Queued and retryable work is
/// intentionally excluded so retention cannot interrupt delivery.
/// </summary>
public sealed class RetentionCleanupService(CommunicationsDbContext db, IOptions<CommunicationsOptions> options)
{
    private readonly CommunicationsOptions communications = options.Value;

    public async Task<int> CleanupBatchAsync(DateTimeOffset now, CancellationToken cancellationToken)
    {
        var days = Math.Max(1, communications.Retention.Days);
        var batchSize = Math.Clamp(communications.Retention.BatchSize, 1, 1000);
        var cutoff = now.AddDays(-days);
        var messageIds = await db.EmailMessages.AsNoTracking()
            .Where(message => message.CreatedAt < cutoff &&
                db.OutboxJobs.Any(job => job.MessageId == message.Id) &&
                db.OutboxJobs.Where(job => job.MessageId == message.Id)
                    .All(job => job.Status == "completed" || job.Status == "cancelled" || job.Status == "failed"))
            .OrderBy(message => message.CreatedAt)
            .Select(message => message.Id)
            .Take(batchSize)
            .ToListAsync(cancellationToken);
        if (messageIds.Count == 0) return 0;

        await using var transaction = await db.Database.BeginTransactionAsync(cancellationToken);
        await db.MessageEvents.Where(item => messageIds.Contains(item.MessageId)).ExecuteDeleteAsync(cancellationToken);
        await db.RecipientDeliveries.Where(item => messageIds.Contains(item.MessageId)).ExecuteDeleteAsync(cancellationToken);
        await db.ExternalEntityLinks.Where(item => messageIds.Contains(item.MessageId)).ExecuteDeleteAsync(cancellationToken);
        await db.IdempotencyRecords.Where(item => messageIds.Contains(item.MessageId)).ExecuteDeleteAsync(cancellationToken);
        await db.OutboxJobs.Where(item => messageIds.Contains(item.MessageId)).ExecuteDeleteAsync(cancellationToken);
        var deleted = await db.EmailMessages.Where(item => messageIds.Contains(item.Id)).ExecuteDeleteAsync(cancellationToken);
        await transaction.CommitAsync(cancellationToken);
        return deleted;
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