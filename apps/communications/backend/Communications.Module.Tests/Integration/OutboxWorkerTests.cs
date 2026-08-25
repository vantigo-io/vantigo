using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;

using Vantigo.Communications.Database.Communications;
using Vantigo.Communications.Services;

namespace Vantigo.Communications.Module.Tests.Integration;

[Collection(CommunicationsModuleCollection.Name)]
public sealed class OutboxWorkerTests(CommunicationsModuleFactory factory)
{
    [Fact]
    public async Task Worker_uses_replaceable_sender_and_marks_relay_accepted()
    {
        using var scope = factory.Services.CreateScope(); var db = scope.ServiceProvider.GetRequiredService<CommunicationsDbContext>(); var channel = await db.Channels.SingleAsync();
        var now = DateTimeOffset.UtcNow; var conversation = new Conversation { Id = Guid.NewGuid(), ChannelId = channel.Id, Subject = "worker test", LastActivityAt = now, CreatedAt = now }; var message = new ConversationMessage { Id = Guid.NewGuid(), ConversationId = conversation.Id, Direction = "outbound", Subject = "worker test", TextBody = "body", OccurredAt = now, CreatedAt = now }; message.Deliveries.Add(new MessageDelivery { Id = Guid.NewGuid(), MessageId = message.Id, RecipientAddress = "worker@example.test", RecipientType = "to", CreatedAt = now }); db.Conversations.Add(conversation); db.ConversationMessages.Add(message); db.OutboxJobs.Add(new OutboxJob { Id = Guid.NewGuid(), MessageId = message.Id, CreatedAt = now, NextAttemptAt = now }); await db.SaveChangesAsync();
        Assert.True(await scope.ServiceProvider.GetRequiredService<OutboxJobProcessor>().ProcessOneAsync(message.Id, CancellationToken.None)); db.ChangeTracker.Clear(); Assert.Equal("relay_accepted", await db.MessageDeliveries.Where(item => item.MessageId == message.Id).Select(item => item.Status).SingleAsync()); Assert.Contains(factory.Sender.Envelopes, envelope => envelope.Subject == "worker test");
    }

    [Fact]
    public async Task Crash_after_send_resends_observably_with_the_same_message_id()
    {
        // A replica that died between provider acceptance and the completion
        // commit leaves the job leased-out with the imminent-send marker set.
        // Recovery must resend (delivery is at-least-once), reuse the same
        // deterministic message identity, and flag the possible duplicate.
        using var scope = factory.Services.CreateScope();
        var db = scope.ServiceProvider.GetRequiredService<CommunicationsDbContext>();
        var channel = await db.Channels.SingleAsync();
        var now = DateTimeOffset.UtcNow;
        var conversation = new Conversation { Id = Guid.NewGuid(), ChannelId = channel.Id, Subject = "crash recovery test", LastActivityAt = now, CreatedAt = now };
        var message = new ConversationMessage { Id = Guid.NewGuid(), ConversationId = conversation.Id, Direction = "outbound", Subject = "crash recovery test", TextBody = "body", OccurredAt = now, CreatedAt = now };
        message.Deliveries.Add(new MessageDelivery { Id = Guid.NewGuid(), MessageId = message.Id, RecipientAddress = "crash@example.test", RecipientType = "to", Status = "sending", Attempts = 1, CreatedAt = now });
        db.Conversations.Add(conversation);
        db.ConversationMessages.Add(message);
        db.OutboxJobs.Add(new OutboxJob
        {
            Id = Guid.NewGuid(),
            MessageId = message.Id,
            Status = "processing",
            Attempts = 1,
            LeaseId = "dead-replica",
            LeaseUntil = now.AddMinutes(-5),
            DeliveryAttemptedAt = now.AddMinutes(-5),
            NextAttemptAt = now.AddMinutes(-6),
            CreatedAt = now.AddMinutes(-6),
        });
        await db.SaveChangesAsync();

        long possibleDuplicates = 0;
        using var meterListener = new System.Diagnostics.Metrics.MeterListener();
        meterListener.InstrumentPublished = (instrument, listener) =>
        {
            if (instrument.Name == "communications.outbox.possible_duplicate_sends")
                listener.EnableMeasurementEvents(instrument);
        };
        meterListener.SetMeasurementEventCallback<long>((_, value, _, _) => Interlocked.Add(ref possibleDuplicates, value));
        meterListener.Start();

        Assert.True(await scope.ServiceProvider.GetRequiredService<OutboxJobProcessor>().ProcessOneAsync(message.Id, CancellationToken.None));

        db.ChangeTracker.Clear();
        Assert.Equal("relay_accepted", await db.MessageDeliveries.Where(item => item.MessageId == message.Id).Select(item => item.Status).SingleAsync());
        Assert.Equal("completed", await db.OutboxJobs.Where(item => item.MessageId == message.Id).Select(item => item.Status).SingleAsync());
        // The resent envelope carries the same message identity, so the
        // deterministic Message-Id header collapses duplicates at receivers.
        Assert.Contains(factory.Sender.Envelopes, envelope => envelope.MessageId == message.Id);
        Assert.Equal(1, Interlocked.Read(ref possibleDuplicates));
    }

    [Fact]
    public async Task Worker_marks_submission_failed_when_sender_fails()
    {
        factory.Sender.ThrowOnSend = true;
        try
        {
            using var scope = factory.Services.CreateScope(); var db = scope.ServiceProvider.GetRequiredService<CommunicationsDbContext>(); var channel = await db.Channels.SingleAsync(); var now = DateTimeOffset.UtcNow; var conversation = new Conversation { Id = Guid.NewGuid(), ChannelId = channel.Id, Subject = "failed worker test", LastActivityAt = now, CreatedAt = now }; var message = new ConversationMessage { Id = Guid.NewGuid(), ConversationId = conversation.Id, Direction = "outbound", Subject = "failed worker test", TextBody = "body", OccurredAt = now, CreatedAt = now }; message.Deliveries.Add(new MessageDelivery { Id = Guid.NewGuid(), MessageId = message.Id, RecipientAddress = "failed@example.test", RecipientType = "to", CreatedAt = now }); db.Conversations.Add(conversation); db.ConversationMessages.Add(message); db.OutboxJobs.Add(new OutboxJob { Id = Guid.NewGuid(), MessageId = message.Id, Attempts = 8, CreatedAt = now, NextAttemptAt = now }); await db.SaveChangesAsync();
            Assert.True(await scope.ServiceProvider.GetRequiredService<OutboxJobProcessor>().ProcessOneAsync(message.Id, CancellationToken.None)); db.ChangeTracker.Clear(); Assert.Equal("submission_failed", await db.MessageDeliveries.Where(item => item.MessageId == message.Id).Select(item => item.Status).SingleAsync()); Assert.Equal("failed", await db.OutboxJobs.Where(item => item.MessageId == message.Id).Select(item => item.Status).SingleAsync());
        }
        finally { factory.Sender.ThrowOnSend = false; }
    }
}