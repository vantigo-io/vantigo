using Microsoft.EntityFrameworkCore;

using Vantigo.Communications.Api.Database.Communications;

namespace Vantigo.Communications.Api.Services;

public sealed class OutboxJobProcessor(CommunicationsDbContext db, IEmailSender sender, IConfiguration configuration)
{
    public Task<bool> ProcessOneAsync(CancellationToken cancellationToken) => ProcessOneAsync(null, cancellationToken);

    public async Task<bool> ProcessOneAsync(Guid? onlyMessageId, CancellationToken cancellationToken)
    {
        var leaseId = Guid.NewGuid().ToString("N");
        OutboxJob? job = null;
        await using (var transaction = await db.Database.BeginTransactionAsync(cancellationToken))
        {
            var now = DateTimeOffset.UtcNow;
            var leaseUntil = now.AddSeconds(configuration.GetValue("Outbox:LeaseSeconds", 60));
            var maximumClaimAttempts = Math.Max(3, configuration.GetValue("Outbox:ClaimAttempts", 10));
            for (var claimAttempt = 0; claimAttempt < maximumClaimAttempts && job is null; claimAttempt++)
            {
                var candidate = await db.OutboxJobs.AsNoTracking()
                    .Where(item => (item.Status == "pending" || item.Status == "retry") && item.NextAttemptAt <= now ||
                        item.Status == "processing" && item.LeaseUntil < now)
                    .Where(item => !onlyMessageId.HasValue || item.MessageId == onlyMessageId.Value)
                    .OrderBy(item => item.NextAttemptAt)
                    .FirstOrDefaultAsync(cancellationToken);
                if (candidate is null) break;

                // The conditional update is the claim lock. It avoids raw SQL that
                // depends on provider-specific quoted PascalCase column names.
                var claimed = await db.OutboxJobs.Where(item => item.Id == candidate.Id &&
                        ((item.Status == "pending" || item.Status == "retry") && item.NextAttemptAt <= now ||
                         item.Status == "processing" && item.LeaseUntil < now) &&
                        (!onlyMessageId.HasValue || item.MessageId == onlyMessageId.Value))
                    .ExecuteUpdateAsync(setters => setters
                        .SetProperty(item => item.Status, "processing")
                        .SetProperty(item => item.LeaseId, leaseId)
                        .SetProperty(item => item.LeaseUntil, leaseUntil)
                        .SetProperty(item => item.Attempts, item => item.Attempts + 1), cancellationToken);
                if (claimed == 0) continue;

                db.ChangeTracker.Clear();
                job = await db.OutboxJobs.SingleAsync(item => item.Id == candidate.Id, cancellationToken);
                var deliveries = await db.RecipientDeliveries.Where(item => item.MessageId == job.MessageId).ToListAsync(cancellationToken);
                foreach (var delivery in deliveries)
                {
                    delivery.Attempts++;
                    delivery.Status = "sending";
                }
                await db.SaveChangesAsync(cancellationToken);
            }

            if (job is null) return false;
            await transaction.CommitAsync(cancellationToken);
        }

        try
        {
            // The claim transaction committed tracked delivery instances in the
            // sending state. Start the send phase from fresh database state so a
            // later completion update cannot be masked by stale EF tracking.
            db.ChangeTracker.Clear();
            var message = await db.EmailMessages.Include(item => item.Mailbox).Include(item => item.Deliveries)
                .SingleAsync(item => item.Id == job.MessageId, cancellationToken);
            if (message.Mailbox is null) throw new InvalidOperationException("The message mailbox no longer exists.");

            // Suppression is deliberately checked after claiming and immediately
            // before sender invocation. The all-or-nothing policy prevents a mixed
            // recipient submission when a suppression was added while queued.
            var normalizedAddresses = message.Deliveries.Select(delivery => EmailSuppression.Normalize(delivery.EmailAddress)).ToArray();
            var suppressed = await db.Suppressions.AsNoTracking()
                .Where(item => normalizedAddresses.Contains(item.NormalizedEmailAddress))
                .Select(item => item.NormalizedEmailAddress)
                .ToListAsync(cancellationToken);
            if (suppressed.Count > 0)
            {
                await CancelSuppressedAsync(job.Id, leaseId, message, cancellationToken);
                return true;
            }

            await sender.SendAsync(EmailEnvelopeFactory.Create(message, message.Mailbox), cancellationToken);
            var now = DateTimeOffset.UtcNow;
            await using var transaction = await db.Database.BeginTransactionAsync(cancellationToken);
            var current = await db.OutboxJobs.SingleAsync(item => item.Id == job.Id, cancellationToken);
            if (current.LeaseId != leaseId) return true;
            current.Status = "completed";
            current.CompletedAt = now;
            current.LeaseId = null;
            current.LeaseUntil = null;
            foreach (var delivery in message.Deliveries)
            {
                delivery.Status = "relay_accepted";
                delivery.LastError = null;
                delivery.AcceptedAt = now;
                db.MessageEvents.Add(new MessageEvent
                {
                    Id = Guid.NewGuid(),
                    MessageId = message.Id,
                    DeliveryId = delivery.Id,
                    EventType = "relay_accepted",
                    OccurredAt = now,
                });
            }
            await db.SaveChangesAsync(cancellationToken);
            await transaction.CommitAsync(cancellationToken);
            return true;
        }
        catch (Exception exception) when (!cancellationToken.IsCancellationRequested)
        {
            db.ChangeTracker.Clear();
            await MarkFailedAsync(job.Id, leaseId, exception.Message, cancellationToken);
            return true;
        }
    }

    private async Task CancelSuppressedAsync(Guid jobId, string leaseId, EmailMessage message, CancellationToken cancellationToken)
    {
        db.ChangeTracker.Clear();
        await using var transaction = await db.Database.BeginTransactionAsync(cancellationToken);
        var current = await db.OutboxJobs.SingleAsync(item => item.Id == jobId, cancellationToken);
        if (current.LeaseId != leaseId) return;
        var deliveries = await db.RecipientDeliveries.Where(item => item.MessageId == message.Id).ToListAsync(cancellationToken);
        var now = DateTimeOffset.UtcNow;
        current.Status = "cancelled";
        current.CompletedAt = now;
        current.LeaseId = null;
        current.LeaseUntil = null;
        foreach (var delivery in deliveries)
        {
            delivery.Status = "suppressed";
            delivery.LastError = null;
            db.MessageEvents.Add(new MessageEvent
            {
                Id = Guid.NewGuid(),
                MessageId = message.Id,
                DeliveryId = delivery.Id,
                EventType = "suppressed",
                OccurredAt = now,
            });
        }
        await db.SaveChangesAsync(cancellationToken);
        await transaction.CommitAsync(cancellationToken);
    }

    private async Task MarkFailedAsync(Guid jobId, string leaseId, string error, CancellationToken cancellationToken)
    {
        db.ChangeTracker.Clear();
        var current = await db.OutboxJobs.SingleOrDefaultAsync(item => item.Id == jobId, cancellationToken);
        if (current is null || current.LeaseId != leaseId) return;
        var maxAttempts = Math.Max(1, configuration.GetValue("Outbox:MaxAttempts", 8));
        var terminal = current.Attempts >= maxAttempts;
        current.Status = terminal ? "failed" : "retry";
        current.LastError = error.Length > 4000 ? error[..4000] : error;
        current.NextAttemptAt = DateTimeOffset.UtcNow.AddSeconds(Math.Min(3600, Math.Pow(2, Math.Min(current.Attempts, 10))));
        current.LeaseId = null;
        current.LeaseUntil = null;
        var deliveries = await db.RecipientDeliveries.Where(delivery => delivery.MessageId == current.MessageId).ToListAsync(cancellationToken);
        foreach (var delivery in deliveries)
        {
            delivery.Status = terminal ? "submission_failed" : "retrying";
            delivery.LastError = current.LastError;
            db.MessageEvents.Add(new MessageEvent
            {
                Id = Guid.NewGuid(),
                MessageId = current.MessageId,
                DeliveryId = delivery.Id,
                EventType = terminal ? "submission_failed" : "retrying",
                OccurredAt = DateTimeOffset.UtcNow,
            });
        }
        await db.SaveChangesAsync(cancellationToken);
    }
}

public sealed class CommunicationsOutboxWorker(IServiceScopeFactory scopeFactory, ILogger<CommunicationsOutboxWorker> logger, IConfiguration configuration) : BackgroundService
{
    protected override async Task ExecuteAsync(CancellationToken stoppingToken)
    {
        var delay = TimeSpan.FromSeconds(Math.Max(1, configuration.GetValue("Outbox:PollSeconds", 5)));
        while (!stoppingToken.IsCancellationRequested)
        {
            try
            {
                await using var scope = scopeFactory.CreateAsyncScope();
                var processor = scope.ServiceProvider.GetRequiredService<OutboxJobProcessor>();
                while (await processor.ProcessOneAsync(stoppingToken)) { }
            }
            catch (OperationCanceledException) when (stoppingToken.IsCancellationRequested) { }
            catch (Exception exception) { logger.LogError(exception, "Communications outbox processing failed."); }
            await Task.Delay(delay, stoppingToken);
        }
    }
}