using System.Net;

using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;

using Vantigo.Communications.Database.Communications;
using Vantigo.Communications.Services;

namespace Vantigo.Communications.Module.Tests.Integration;

[Collection(CommunicationsModuleCollection.Name)]
public sealed class AttachmentScanningTests(CommunicationsModuleFactory factory)
{
    [Fact]
    public async Task Expired_upload_is_queued_once_across_repeated_polls()
    {
        await factory.ResetChannelStateAsync();
        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<CommunicationsDbContext>();
        var channel = await db.Channels.SingleAsync();
        var now = DateTimeOffset.UtcNow;
        var conversation = new Conversation { Id = Guid.NewGuid(), ChannelId = channel.Id, LastActivityAt = now, CreatedAt = now };
        var key = $"expired/{Guid.NewGuid():N}";
        db.Conversations.Add(conversation);
        db.AttachmentUploads.Add(new AttachmentUpload
        {
            Id = Guid.NewGuid(),
            ConversationId = conversation.Id,
            UploadedByUserId = Guid.NewGuid(),
            IdempotencyKey = Guid.NewGuid().ToString("N"),
            FileName = "expired.txt",
            ContentType = "text/plain",
            SizeBytes = 1,
            ContentHash = "hash",
            StorageKey = key,
            ExpiresAt = now.AddMinutes(-1),
            CreatedAt = now,
            ScanStatus = "pending",
            NextScanAt = now,
        });
        await db.SaveChangesAsync();
        await factory.CommunicationsStore.PutAsync(key, new MemoryStream("x"u8.ToArray()), "text/plain");
        var processor = scope.ServiceProvider.GetRequiredService<AttachmentScanProcessor>();

        Assert.False(await processor.ProcessOneAsync(CancellationToken.None));
        db.ChangeTracker.Clear();
        Assert.False(await processor.ProcessOneAsync(CancellationToken.None));

        Assert.Equal(1, await db.AttachmentCleanupRecords.CountAsync(item => item.StorageKey == key));
        Assert.Equal("expired", await db.AttachmentUploads.Where(item => item.StorageKey == key).Select(item => item.ScanStatus).SingleAsync());
        var cleaned = await scope.ServiceProvider.GetRequiredService<AttachmentCleanupService>().CleanupBatchAsync(DateTimeOffset.UtcNow, CancellationToken.None);
        Assert.Equal(1, cleaned);
        Assert.False(await factory.CommunicationsStore.ExistsAsync(key));
        db.ChangeTracker.Clear();
        Assert.Equal(1, await db.AttachmentCleanupRecords.CountAsync(item => item.StorageKey == key));
    }

    [Fact]
    public async Task Stale_scan_result_cannot_replace_a_newer_lease_or_create_cleanup()
    {
        await factory.ResetChannelStateAsync();
        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<CommunicationsDbContext>();
        var channel = await db.Channels.SingleAsync();
        var now = DateTimeOffset.UtcNow;
        var conversation = new Conversation { Id = Guid.NewGuid(), ChannelId = channel.Id, Subject = "Fence", LastActivityAt = now, CreatedAt = now };
        var message = new ConversationMessage { Id = Guid.NewGuid(), ConversationId = conversation.Id, Direction = "inbound", TextBody = "body", OccurredAt = now, CreatedAt = now };
        var attachment = new MessageAttachment
        {
            Id = Guid.NewGuid(),
            MessageId = message.Id,
            FileName = "test.txt",
            ContentType = "text/plain",
            SizeBytes = 4,
            ContentHash = "hash",
            StorageKey = $"fence/{Guid.NewGuid():N}",
            ScanStatus = "pending",
            NextScanAt = now,
            CreatedAt = now,
        };
        message.Attachments.Add(attachment);
        conversation.Messages.Add(message);
        db.Conversations.Add(conversation);
        await db.SaveChangesAsync();
        await factory.CommunicationsStore.PutAsync(attachment.StorageKey, new MemoryStream("test"u8.ToArray()), attachment.ContentType);

        var scanStarted = new TaskCompletionSource<bool>(TaskCreationOptions.RunContinuationsAsynchronously);
        var continueScan = new TaskCompletionSource<bool>(TaskCreationOptions.RunContinuationsAsynchronously);
        factory.AttachmentScanner.ScanStarted = scanStarted;
        factory.AttachmentScanner.ContinueScan = continueScan;
        factory.AttachmentScanner.Result = new(AttachmentScanVerdict.Malware);

        try
        {
            var processing = Task.Run(async () =>
            {
                await using var workerScope = factory.Services.CreateAsyncScope();
                return await workerScope.ServiceProvider.GetRequiredService<AttachmentScanProcessor>().ProcessOneAsync(CancellationToken.None);
            });
            await scanStarted.Task.WaitAsync(TimeSpan.FromSeconds(10));

            await using var takeoverScope = factory.Services.CreateAsyncScope();
            var takeoverDb = takeoverScope.ServiceProvider.GetRequiredService<CommunicationsDbContext>();
            var stolen = await takeoverDb.MessageAttachments.Where(item => item.Id == attachment.Id)
                .ExecuteUpdateAsync(setters => setters.SetProperty(item => item.ScanStatus, "scanning")
                    .SetProperty(item => item.ScanLeaseId, "newer-worker")
                    .SetProperty(item => item.ScanLeaseUntil, DateTimeOffset.UtcNow.AddMinutes(2)));
            Assert.Equal(1, stolen);
            continueScan.TrySetResult(true);
            Assert.True(await processing);

            takeoverDb.ChangeTracker.Clear();
            var persisted = await takeoverDb.MessageAttachments.SingleAsync(item => item.Id == attachment.Id);
            Assert.Equal("scanning", persisted.ScanStatus);
            Assert.Equal("newer-worker", persisted.ScanLeaseId);
            Assert.False(await takeoverDb.AttachmentCleanupRecords.AnyAsync(item => item.StorageKey == attachment.StorageKey));
        }
        finally
        {
            factory.AttachmentScanner.ScanStarted = null;
            factory.AttachmentScanner.ContinueScan = null;
            factory.AttachmentScanner.Result = new(AttachmentScanVerdict.Clean);
        }
    }
}