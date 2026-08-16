using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;

using Vantigo.Communications.Database.Communications;
using Vantigo.Communications.Services;

namespace Vantigo.Communications.Module.Tests.Integration;

[Collection(CommunicationsModuleCollection.Name)]
public sealed class ObjectLifecycleTests(CommunicationsModuleFactory factory)
{
    [Fact]
    public async Task Purger_deletes_only_known_communications_keys_through_typed_scope()
    {
        await factory.ResetChannelStateAsync();
        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<CommunicationsDbContext>();
        var channel = await db.Channels.SingleAsync();
        var now = DateTimeOffset.UtcNow;
        var conversation = new Conversation { Id = Guid.NewGuid(), ChannelId = channel.Id, LastActivityAt = now, CreatedAt = now };
        var message = new ConversationMessage
        {
            Id = Guid.NewGuid(),
            ConversationId = conversation.Id,
            Direction = "inbound",
            CreatedAt = now,
            OccurredAt = now,
            RawPayloadStorageKey = $"raw/{Guid.NewGuid():N}.eml",
        };
        var attachment = new MessageAttachment
        {
            Id = Guid.NewGuid(),
            MessageId = message.Id,
            FileName = "a.txt",
            ContentType = "text/plain",
            SizeBytes = 1,
            ContentHash = "hash",
            StorageKey = $"attachments/{Guid.NewGuid():N}",
            CreatedAt = now,
        };
        var upload = new AttachmentUpload
        {
            Id = Guid.NewGuid(),
            ConversationId = conversation.Id,
            UploadedByUserId = Guid.NewGuid(),
            IdempotencyKey = Guid.NewGuid().ToString("N"),
            FileName = "b.txt",
            ContentType = "text/plain",
            SizeBytes = 1,
            ContentHash = "hash",
            StorageKey = $"staged/{Guid.NewGuid():N}",
            ExpiresAt = now.AddHours(1),
            CreatedAt = now,
        };
        var receipt = new InboundReceipt
        {
            Id = Guid.NewGuid(),
            ChannelId = channel.Id,
            Provider = "mailgun",
            ProviderEventId = Guid.NewGuid().ToString("N"),
            ReceivedAt = now,
        };
        var job = new InboundEmailJob
        {
            Id = Guid.NewGuid(),
            ChannelId = channel.Id,
            InboundReceiptId = receipt.Id,
            RawMimeStorageKey = $"jobs/{Guid.NewGuid():N}.eml",
            Status = "completed",
            ReceivedAt = now,
            CreatedAt = now,
            InboundReceipt = receipt,
        };
        var reservedKey = $"reserved/{Guid.NewGuid():N}";
        db.Conversations.Add(conversation);
        conversation.Messages.Add(message);
        message.Attachments.Add(attachment);
        db.AttachmentUploads.Add(upload);
        db.InboundEmailJobs.Add(job);
        db.AttachmentCleanupRecords.Add(new AttachmentCleanupRecord { Id = Guid.NewGuid(), StorageKey = reservedKey, CreatedAt = now, NextAttemptAt = now });
        await db.SaveChangesAsync();

        var known = new[] { message.RawPayloadStorageKey, attachment.StorageKey, upload.StorageKey, job.RawMimeStorageKey, reservedKey };
        foreach (var key in known)
            await factory.CommunicationsStore.PutAsync(key, new MemoryStream("known"u8.ToArray()), "text/plain");
        await factory.CommunicationsStore.PutAsync("unreferenced/key", new MemoryStream("keep"u8.ToArray()), "text/plain");
        var other = new TestScopedObjectStore<OtherStorageScope>(factory.ObjectStore);
        await other.PutAsync("same-key", new MemoryStream("other"u8.ToArray()), "text/plain");

        await scope.ServiceProvider.GetRequiredService<ICommunicationsObjectPurger>().PurgeAsync();

        foreach (var key in known)
            Assert.False(await factory.CommunicationsStore.ExistsAsync(key));
        Assert.True(await factory.CommunicationsStore.ExistsAsync("unreferenced/key"));
        Assert.True(factory.ObjectStore.ContainsPhysicalKey("other/same-key"));
    }

    [Fact]
    public async Task Owned_object_is_not_selected_by_cleanup_worker()
    {
        await factory.ResetChannelStateAsync();
        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<CommunicationsDbContext>();
        var key = $"owned/{Guid.NewGuid():N}";
        db.AttachmentCleanupRecords.Add(new AttachmentCleanupRecord
        {
            Id = Guid.NewGuid(),
            StorageKey = key,
            Status = "owned",
            CreatedAt = DateTimeOffset.UtcNow,
            NextAttemptAt = DateTimeOffset.UtcNow,
        });
        await db.SaveChangesAsync();
        await factory.CommunicationsStore.PutAsync(key, new MemoryStream("owned"u8.ToArray()), "text/plain");

        Assert.Equal(0, await scope.ServiceProvider.GetRequiredService<AttachmentCleanupService>()
            .CleanupBatchAsync(DateTimeOffset.UtcNow, CancellationToken.None));
        Assert.True(await factory.CommunicationsStore.ExistsAsync(key));
    }

    private sealed class OtherStorageScope : Vantigo.Storage.Abstractions.IStorageScope
    {
        public static string Name => "other";
    }
}