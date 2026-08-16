using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;

using Vantigo.Communications.Database.Communications;
using Vantigo.Communications.Services;
using Vantigo.Identity.Database.Accounts;
using Vantigo.Tenancy;
using Vantigo.Tenancy.Abstractions;

namespace Vantigo.Communications.Module.Tests.Integration;

[Collection(CommunicationsModuleCollection.Name)]
public sealed class TenantIsolationTests(CommunicationsModuleFactory factory)
{
    [Fact]
    public async Task Tenant_owned_conversations_and_attachments_are_isolated()
    {
        var tenantA = TestTenantContext.DefaultTenant;
        var tenantB = TenantId.New();
        var conversationId = Guid.NewGuid();
        var attachmentId = Guid.NewGuid();

        await using (var scope = factory.Services.CreateAsyncScope())
        {
            using var tenantScope = AmbientTenantContext.Enter(tenantA);
            var db = scope.ServiceProvider.GetRequiredService<CommunicationsDbContext>();
            var channel = await db.Channels.SingleAsync();
            var conversation = new Conversation
            {
                Id = conversationId,
                ChannelId = channel.Id,
                Channel = channel,
                Subject = "Tenant A",
                LastActivityAt = DateTimeOffset.UtcNow,
                CreatedAt = DateTimeOffset.UtcNow,
            };
            var message = new ConversationMessage
            {
                Id = Guid.NewGuid(),
                ConversationId = conversation.Id,
                Conversation = conversation,
                Direction = "inbound",
                OccurredAt = DateTimeOffset.UtcNow,
                CreatedAt = DateTimeOffset.UtcNow,
            };
            message.Attachments.Add(new MessageAttachment
            {
                Id = attachmentId,
                MessageId = message.Id,
                Message = message,
                FileName = "tenant-a.txt",
                ContentType = "text/plain",
                SizeBytes = 1,
                ContentHash = "hash-a",
                StorageKey = $"tenant-a/{attachmentId:N}",
                CreatedAt = DateTimeOffset.UtcNow,
            });
            db.Conversations.Add(conversation);
            db.ConversationMessages.Add(message);
            await db.SaveChangesAsync();
        }

        await using (var scope = factory.Services.CreateAsyncScope())
        {
            using var tenantScope = AmbientTenantContext.Enter(tenantB);
            var db = scope.ServiceProvider.GetRequiredService<CommunicationsDbContext>();
            await using var transaction = await db.Database.BeginTransactionAsync();
            Assert.False(await db.Conversations.AnyAsync(item => item.Id == conversationId));
            Assert.False(await db.MessageAttachments.AnyAsync(item => item.Id == attachmentId));
            await transaction.CommitAsync();
        }

        await using (var scope = factory.Services.CreateAsyncScope())
        {
            using var tenantScope = AmbientTenantContext.Enter(tenantA);
            var db = scope.ServiceProvider.GetRequiredService<CommunicationsDbContext>();
            await using var transaction = await db.Database.BeginTransactionAsync();
            Assert.True(await db.Conversations.IgnoreQueryFilters().AnyAsync(item => item.Id == conversationId && item.TenantId == tenantA.Value));
            Assert.True(await db.MessageAttachments.IgnoreQueryFilters().AnyAsync(item => item.Id == attachmentId && item.TenantId == tenantA.Value));
            await db.Conversations.Where(item => item.Id == conversationId).ExecuteDeleteAsync();
            await transaction.CommitAsync();
        }
    }

    [Fact]
    public async Task Outbox_worker_processes_a_row_under_that_rows_tenant()
    {
        var tenant = TestTenantContext.DefaultTenant;
        var messageId = Guid.NewGuid();
        var now = DateTimeOffset.UtcNow;

        await using (var scope = factory.Services.CreateAsyncScope())
        {
            using var tenantScope = AmbientTenantContext.Enter(tenant);
            var db = scope.ServiceProvider.GetRequiredService<CommunicationsDbContext>();
            var channel = await db.Channels.SingleAsync();
            var conversation = new Conversation
            {
                Id = Guid.NewGuid(),
                ChannelId = channel.Id,
                Channel = channel,
                Subject = "Tenant worker",
                LastActivityAt = now,
                CreatedAt = now,
            };
            var message = new ConversationMessage
            {
                Id = messageId,
                ConversationId = conversation.Id,
                Conversation = conversation,
                Direction = "outbound",
                Subject = "Tenant worker",
                TextBody = "body",
                OccurredAt = now,
                CreatedAt = now,
            };
            message.Deliveries.Add(new MessageDelivery
            {
                Id = Guid.NewGuid(),
                MessageId = message.Id,
                Message = message,
                RecipientAddress = "worker-tenant@example.test",
                RecipientType = "to",
                CreatedAt = now,
            });
            db.Conversations.Add(conversation);
            db.ConversationMessages.Add(message);
            db.OutboxJobs.Add(new OutboxJob { Id = Guid.NewGuid(), MessageId = message.Id, Message = message, CreatedAt = now, NextAttemptAt = now });
            await db.SaveChangesAsync();
        }

        await using (var scope = factory.Services.CreateAsyncScope())
        {
            var processor = scope.ServiceProvider.GetRequiredService<OutboxJobProcessor>();
            Assert.True(await processor.ProcessOneAsync(CancellationToken.None));
        }

        await using (var scope = factory.Services.CreateAsyncScope())
        {
            using var tenantScope = AmbientTenantContext.Enter(tenant);
            var db = scope.ServiceProvider.GetRequiredService<CommunicationsDbContext>();
            await using var transaction = await db.Database.BeginTransactionAsync();
            Assert.Equal("completed", await db.OutboxJobs.Where(item => item.MessageId == messageId).Select(item => item.Status).SingleAsync());
            await db.OutboxJobs.Where(item => item.MessageId == messageId).ExecuteDeleteAsync();
            await db.ConversationMessages.Where(item => item.Id == messageId).ExecuteDeleteAsync();
            await db.Conversations.Where(item => item.Id == messageId).ExecuteDeleteAsync();
            await transaction.CommitAsync();
        }
    }

}