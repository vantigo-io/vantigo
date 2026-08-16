using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;

using Vantigo.Communications.Database.Communications;
using Vantigo.Communications.Services;
namespace Vantigo.Communications.Module.Tests.Integration;

[Collection(CommunicationsModuleCollection.Name)]
public sealed class RetentionCleanupTests(CommunicationsModuleFactory factory)
{
    [Fact]
    public async Task Deletes_terminal_history_but_keeps_retrying_work()
    {
        using var scope = factory.Services.CreateScope(); var db = scope.ServiceProvider.GetRequiredService<CommunicationsDbContext>(); var channel = await db.Channels.SingleAsync(); var old = DateTimeOffset.UtcNow.AddDays(-400);
        var terminal = new ConversationMessage { Id = Guid.NewGuid(), Conversation = new Conversation { Id = Guid.NewGuid(), ChannelId = channel.Id, LastActivityAt = old, CreatedAt = old }, ConversationId = Guid.NewGuid(), Direction = "outbound", Subject = "old terminal", CreatedAt = old, OccurredAt = old }; var retrying = new ConversationMessage { Id = Guid.NewGuid(), Conversation = new Conversation { Id = Guid.NewGuid(), ChannelId = channel.Id, LastActivityAt = old, CreatedAt = old }, ConversationId = Guid.NewGuid(), Direction = "outbound", Subject = "old retry", CreatedAt = old, OccurredAt = old }; db.ConversationMessages.AddRange(terminal, retrying); db.OutboxJobs.AddRange(new OutboxJob { Id = Guid.NewGuid(), MessageId = terminal.Id, Status = "completed", NextAttemptAt = old, CreatedAt = old }, new OutboxJob { Id = Guid.NewGuid(), MessageId = retrying.Id, Status = "retry", NextAttemptAt = old, CreatedAt = old }); await db.SaveChangesAsync();
        var deleted = await scope.ServiceProvider.GetRequiredService<RetentionCleanupService>().CleanupBatchAsync(DateTimeOffset.UtcNow, CancellationToken.None); db.ChangeTracker.Clear(); Assert.True(deleted >= 1); Assert.False(await db.ConversationMessages.AnyAsync(item => item.Id == terminal.Id)); Assert.True(await db.ConversationMessages.AnyAsync(item => item.Id == retrying.Id));
    }

    [Fact]
    public async Task Persists_raw_mime_cleanup_before_deleting_message_and_worker_removes_object()
    {
        await factory.ResetChannelStateAsync();
        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<CommunicationsDbContext>();
        var channel = await db.Channels.SingleAsync();
        var old = DateTimeOffset.UtcNow.AddDays(-400);
        var message = new ConversationMessage
        {
            Id = Guid.NewGuid(),
            Conversation = new Conversation { Id = Guid.NewGuid(), ChannelId = channel.Id, LastActivityAt = old, CreatedAt = old },
            ConversationId = Guid.NewGuid(),
            Direction = "inbound",
            CreatedAt = old,
            OccurredAt = old,
            RawPayloadStorageKey = $"inbound/{channel.Id:N}/{Guid.NewGuid():N}.eml",
        };
        db.ConversationMessages.Add(message);
        await db.SaveChangesAsync();
        await factory.CommunicationsStore.PutAsync(message.RawPayloadStorageKey, new MemoryStream("raw mime"u8.ToArray()), "message/rfc822");

        var retention = scope.ServiceProvider.GetRequiredService<RetentionCleanupService>();
        Assert.Equal(1, await retention.CleanupBatchAsync(DateTimeOffset.UtcNow, CancellationToken.None));
        db.ChangeTracker.Clear();
        var record = await db.AttachmentCleanupRecords.SingleAsync(item => item.StorageKey == message.RawPayloadStorageKey);
        Assert.Equal("pending", record.Status);
        Assert.False(await db.ConversationMessages.AnyAsync(item => item.Id == message.Id));

        var cleaned = await scope.ServiceProvider.GetRequiredService<AttachmentCleanupService>()
            .CleanupBatchAsync(DateTimeOffset.UtcNow, CancellationToken.None);
        Assert.Equal(1, cleaned);
        Assert.False(await factory.CommunicationsStore.ExistsAsync(message.RawPayloadStorageKey));
        db.ChangeTracker.Clear();
        Assert.Equal("completed", (await db.AttachmentCleanupRecords.SingleAsync(item => item.Id == record.Id)).Status);
    }

    [Fact]
    public async Task Deletes_failed_terminal_inbound_job_without_message_and_retains_non_terminal_job()
    {
        await factory.ResetChannelStateAsync();
        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<CommunicationsDbContext>();
        var channel = await db.Channels.SingleAsync();
        var old = DateTimeOffset.UtcNow.AddDays(-400);
        var failedReceipt = new InboundReceipt
        {
            Id = Guid.NewGuid(),
            ChannelId = channel.Id,
            Provider = "mailgun",
            ProviderEventId = $"failed-{Guid.NewGuid():N}",
            Status = "failed",
            ReceivedAt = old,
        };
        var failedKey = $"inbound/{channel.Id:N}/{Guid.NewGuid():N}.eml";
        var failedJob = new InboundEmailJob
        {
            Id = Guid.NewGuid(),
            ChannelId = channel.Id,
            InboundReceiptId = failedReceipt.Id,
            RawMimeStorageKey = failedKey,
            Status = "failed",
            Attempts = 5,
            NextAttemptAt = old,
            CompletedAt = old,
            ReceivedAt = old,
            CreatedAt = old,
            InboundReceipt = failedReceipt,
        };
        var pendingReceipt = new InboundReceipt
        {
            Id = Guid.NewGuid(),
            ChannelId = channel.Id,
            Provider = "mailgun",
            ProviderEventId = $"pending-{Guid.NewGuid():N}",
            Status = "reserved",
            ReceivedAt = old,
        };
        var pendingJob = new InboundEmailJob
        {
            Id = Guid.NewGuid(),
            ChannelId = channel.Id,
            InboundReceiptId = pendingReceipt.Id,
            RawMimeStorageKey = $"inbound/{channel.Id:N}/{Guid.NewGuid():N}.eml",
            Status = "pending",
            NextAttemptAt = old,
            ReceivedAt = old,
            CreatedAt = old,
            InboundReceipt = pendingReceipt,
        };
        db.InboundEmailJobs.AddRange(failedJob, pendingJob);
        await db.SaveChangesAsync();
        await factory.CommunicationsStore.PutAsync(failedKey, new MemoryStream("failed"u8.ToArray()), "message/rfc822");

        var deleted = await scope.ServiceProvider.GetRequiredService<RetentionCleanupService>()
            .CleanupBatchAsync(DateTimeOffset.UtcNow, CancellationToken.None);
        Assert.True(deleted >= 2);
        db.ChangeTracker.Clear();
        Assert.False(await db.InboundEmailJobs.AnyAsync(item => item.Id == failedJob.Id));
        Assert.False(await db.InboundReceipts.AnyAsync(item => item.Id == failedReceipt.Id));
        Assert.True(await db.InboundEmailJobs.AnyAsync(item => item.Id == pendingJob.Id));
        Assert.True(await db.InboundReceipts.AnyAsync(item => item.Id == pendingReceipt.Id));

        Assert.Equal(1, await db.AttachmentCleanupRecords.CountAsync(item => item.StorageKey == failedKey));
        Assert.Equal(1, await scope.ServiceProvider.GetRequiredService<AttachmentCleanupService>()
            .CleanupBatchAsync(DateTimeOffset.UtcNow, CancellationToken.None));
        Assert.False(await factory.CommunicationsStore.ExistsAsync(failedKey));
    }

    [Fact]
    public async Task Old_receipt_does_not_expire_a_fresh_completed_job_or_its_raw_mime()
    {
        await factory.ResetChannelStateAsync();
        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<CommunicationsDbContext>();
        var channel = await db.Channels.SingleAsync();
        var now = DateTimeOffset.UtcNow;
        var old = now.AddDays(-400);
        var receipt = new InboundReceipt
        {
            Id = Guid.NewGuid(),
            ChannelId = channel.Id,
            Provider = "mailgun",
            ProviderEventId = $"old-receipt-{Guid.NewGuid():N}",
            Status = "completed",
            ReceivedAt = old,
        };
        var rawKey = $"inbound/{channel.Id:N}/{Guid.NewGuid():N}.eml";
        var job = new InboundEmailJob
        {
            Id = Guid.NewGuid(),
            ChannelId = channel.Id,
            InboundReceiptId = receipt.Id,
            RawMimeStorageKey = rawKey,
            Status = "completed",
            CompletedAt = now,
            ReceivedAt = now,
            CreatedAt = now,
            NextAttemptAt = now,
            InboundReceipt = receipt,
        };
        db.InboundEmailJobs.Add(job);
        await db.SaveChangesAsync();
        await factory.CommunicationsStore.PutAsync(rawKey, new MemoryStream("fresh raw"u8.ToArray()), "message/rfc822");

        var deleted = await scope.ServiceProvider.GetRequiredService<RetentionCleanupService>()
            .CleanupBatchAsync(now, CancellationToken.None);

        Assert.Equal(0, deleted);
        db.ChangeTracker.Clear();
        Assert.True(await db.InboundEmailJobs.AnyAsync(item => item.Id == job.Id));
        Assert.True(await db.InboundReceipts.AnyAsync(item => item.Id == receipt.Id));
        Assert.False(await db.AttachmentCleanupRecords.AnyAsync(item => item.StorageKey == rawKey));
        Assert.True(await factory.CommunicationsStore.ExistsAsync(rawKey));
    }

    [Fact]
    public async Task Old_completed_job_queues_raw_mime_before_deleting_job_and_receipt()
    {
        await factory.ResetChannelStateAsync();
        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<CommunicationsDbContext>();
        var channel = await db.Channels.SingleAsync();
        var old = DateTimeOffset.UtcNow.AddDays(-400);
        var receipt = new InboundReceipt
        {
            Id = Guid.NewGuid(),
            ChannelId = channel.Id,
            Provider = "mailgun",
            ProviderEventId = $"old-job-{Guid.NewGuid():N}",
            Status = "completed",
            ReceivedAt = old,
        };
        var rawKey = $"inbound/{channel.Id:N}/{Guid.NewGuid():N}.eml";
        var job = new InboundEmailJob
        {
            Id = Guid.NewGuid(),
            ChannelId = channel.Id,
            InboundReceiptId = receipt.Id,
            RawMimeStorageKey = rawKey,
            Status = "completed",
            CompletedAt = old,
            ReceivedAt = old,
            CreatedAt = old,
            NextAttemptAt = old,
            InboundReceipt = receipt,
        };
        db.InboundEmailJobs.Add(job);
        await db.SaveChangesAsync();
        await factory.CommunicationsStore.PutAsync(rawKey, new MemoryStream("old raw"u8.ToArray()), "message/rfc822");

        var deleted = await scope.ServiceProvider.GetRequiredService<RetentionCleanupService>()
            .CleanupBatchAsync(DateTimeOffset.UtcNow, CancellationToken.None);

        Assert.True(deleted >= 2);
        db.ChangeTracker.Clear();
        Assert.False(await db.InboundEmailJobs.AnyAsync(item => item.Id == job.Id));
        Assert.False(await db.InboundReceipts.AnyAsync(item => item.Id == receipt.Id));
        Assert.Equal(1, await db.AttachmentCleanupRecords.CountAsync(item => item.StorageKey == rawKey));
        Assert.True(await factory.CommunicationsStore.ExistsAsync(rawKey));
        Assert.Equal(1, await scope.ServiceProvider.GetRequiredService<AttachmentCleanupService>()
            .CleanupBatchAsync(DateTimeOffset.UtcNow, CancellationToken.None));
        Assert.False(await factory.CommunicationsStore.ExistsAsync(rawKey));
    }

    [Fact]
    public async Task Old_orphan_receipt_is_deleted_and_its_conventional_raw_key_is_cleaned()
    {
        await factory.ResetChannelStateAsync();
        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<CommunicationsDbContext>();
        var channel = await db.Channels.SingleAsync();
        var old = DateTimeOffset.UtcNow.AddDays(-400);
        var receipt = new InboundReceipt
        {
            Id = Guid.NewGuid(),
            ChannelId = channel.Id,
            Provider = "mailgun",
            ProviderEventId = $"orphan-{Guid.NewGuid():N}",
            Status = "failed",
            ReceivedAt = old,
        };
        var rawKey = $"inbound/{channel.Id:N}/{receipt.Id:N}.eml";
        db.InboundReceipts.Add(receipt);
        await db.SaveChangesAsync();
        await factory.CommunicationsStore.PutAsync(rawKey, new MemoryStream("orphan raw"u8.ToArray()), "message/rfc822");

        var deleted = await scope.ServiceProvider.GetRequiredService<RetentionCleanupService>()
            .CleanupBatchAsync(DateTimeOffset.UtcNow, CancellationToken.None);

        Assert.Equal(1, deleted);
        db.ChangeTracker.Clear();
        Assert.False(await db.InboundReceipts.AnyAsync(item => item.Id == receipt.Id));
        Assert.Equal(1, await db.AttachmentCleanupRecords.CountAsync(item => item.StorageKey == rawKey));
        Assert.Equal(1, await scope.ServiceProvider.GetRequiredService<AttachmentCleanupService>()
            .CleanupBatchAsync(DateTimeOffset.UtcNow, CancellationToken.None));
        Assert.False(await factory.CommunicationsStore.ExistsAsync(rawKey));
    }

}