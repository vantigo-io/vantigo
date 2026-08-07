using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;

using Vantigo.Communications.Api.Database.Communications;
using Vantigo.Communications.Api.Services;

namespace Vantigo.Communications.Api.Tests.Integration;

[Collection(CommunicationsApiCollection.Name)]
public sealed class OutboxWorkerTests(CommunicationsApiFactory factory)
{
    [Fact]
    public async Task Worker_uses_replaceable_sender_and_marks_relay_accepted()
    {
        using var scope = factory.Services.CreateScope();
        var db = scope.ServiceProvider.GetRequiredService<CommunicationsDbContext>();
        var mailbox = await db.SharedMailboxes.SingleAsync();
        var message = new EmailMessage
        {
            Id = Guid.NewGuid(),
            MailboxId = mailbox.Id,
            Subject = "worker test",
            TextBody = "body",
            CreatedAt = DateTimeOffset.UtcNow,
        };
        var delivery = new RecipientDelivery
        {
            Id = Guid.NewGuid(),
            MessageId = message.Id,
            EmailAddress = "worker@example.test",
            RecipientType = "to",
            CreatedAt = DateTimeOffset.UtcNow,
        };
        message.Deliveries.Add(delivery);
        db.EmailMessages.Add(message);
        db.OutboxJobs.Add(new OutboxJob { Id = Guid.NewGuid(), MessageId = message.Id, CreatedAt = DateTimeOffset.UtcNow, NextAttemptAt = DateTimeOffset.UtcNow });
        await db.SaveChangesAsync();

        var processor = scope.ServiceProvider.GetRequiredService<OutboxJobProcessor>();
        Assert.True(await processor.ProcessOneAsync(message.Id, CancellationToken.None));
        db.ChangeTracker.Clear();
        var saved = await db.RecipientDeliveries.SingleAsync(item => item.Id == delivery.Id);
        Assert.Equal("relay_accepted", saved.Status);
        Assert.Contains(factory.Sender.Envelopes, envelope => envelope.Subject == "worker test");
    }

    [Fact]
    public async Task Worker_marks_submission_failed_when_sender_fails()
    {
        factory.Sender.ThrowOnSend = true;
        try
        {
            using var scope = factory.Services.CreateScope();
            var db = scope.ServiceProvider.GetRequiredService<CommunicationsDbContext>();
            var mailbox = await db.SharedMailboxes.SingleAsync();
            var message = new EmailMessage
            {
                Id = Guid.NewGuid(),
                MailboxId = mailbox.Id,
                Subject = "failed worker test",
                TextBody = "body",
                CreatedAt = DateTimeOffset.UtcNow,
            };
            message.Deliveries.Add(new RecipientDelivery
            {
                Id = Guid.NewGuid(),
                MessageId = message.Id,
                EmailAddress = "failed@example.test",
                RecipientType = "to",
                CreatedAt = DateTimeOffset.UtcNow,
            });
            db.EmailMessages.Add(message);
            db.OutboxJobs.Add(new OutboxJob { Id = Guid.NewGuid(), MessageId = message.Id, Attempts = 8, CreatedAt = DateTimeOffset.UtcNow, NextAttemptAt = DateTimeOffset.UtcNow });
            await db.SaveChangesAsync();

            var processor = scope.ServiceProvider.GetRequiredService<OutboxJobProcessor>();
            Assert.True(await processor.ProcessOneAsync(message.Id, CancellationToken.None));
            db.ChangeTracker.Clear();
            var saved = await db.RecipientDeliveries.SingleAsync(item => item.MessageId == message.Id);
            Assert.Equal("submission_failed", saved.Status);
            Assert.Equal("failed", await db.OutboxJobs.Where(item => item.MessageId == message.Id).Select(item => item.Status).SingleAsync());
            var failureEvent = await db.MessageEvents.SingleAsync(item => item.MessageId == message.Id && item.EventType == "submission_failed");
            Assert.Contains("fake SMTP failure", failureEvent.DataJson);
        }
        finally
        {
            factory.Sender.ThrowOnSend = false;
        }
    }
}