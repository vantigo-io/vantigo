using System.Net;
using System.Net.Http.Json;
using System.Security.Cryptography;
using System.Text;
using System.Text.Json;

using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;

using Vantigo.Communications.Database.Communications;
using Vantigo.Communications.Services;

namespace Vantigo.Communications.Module.Tests.Integration;

[Collection(CommunicationsModuleCollection.Name)]
public sealed class MailgunInboundIntegrationTests(CommunicationsModuleFactory factory)
{
    [Fact]
    public async Task Active_reservation_without_job_is_retryable_instead_of_acknowledged()
    {
        var token = $"active-orphan-{Guid.NewGuid():N}";
        var channel = await CreateMailgunChannelAsync(token, "active-orphan");
        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<CommunicationsDbContext>();
        db.InboundReceipts.Add(new InboundReceipt
        {
            Id = Guid.NewGuid(),
            ChannelId = channel.Id,
            Provider = "mailgun",
            ProviderEventId = token,
            Status = "reserved",
            ReservationExpiresAt = DateTimeOffset.UtcNow.AddMinutes(5),
            ReceivedAt = DateTimeOffset.UtcNow,
        });
        await db.SaveChangesAsync();

        using var client = factory.CreateClient();
        var response = await PostInboundAsync(client, channel.Id, channel.SigningKey, token);

        Assert.Equal(HttpStatusCode.ServiceUnavailable, response.StatusCode);
    }

    [Fact]
    public async Task Expired_orphan_reservation_is_reclaimed_and_queued()
    {
        var token = $"expired-orphan-{Guid.NewGuid():N}";
        var channel = await CreateMailgunChannelAsync(token, "expired-orphan");
        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<CommunicationsDbContext>();
        var receiptId = Guid.NewGuid();
        db.InboundReceipts.Add(new InboundReceipt
        {
            Id = receiptId,
            ChannelId = channel.Id,
            Provider = "mailgun",
            ProviderEventId = token,
            Status = "reserved",
            ReservationExpiresAt = DateTimeOffset.UtcNow.AddMinutes(-1),
            ReceivedAt = DateTimeOffset.UtcNow,
        });
        db.AttachmentCleanupRecords.Add(new AttachmentCleanupRecord
        {
            Id = Guid.NewGuid(),
            StorageKey = $"inbound/{channel.Id:N}/{receiptId:N}.eml",
            Status = "staged",
            NextAttemptAt = DateTimeOffset.UtcNow,
            CreatedAt = DateTimeOffset.UtcNow,
        });
        await db.SaveChangesAsync();

        using var client = factory.CreateClient();
        var response = await PostInboundAsync(client, channel.Id, channel.SigningKey, token);

        Assert.Equal(HttpStatusCode.OK, response.StatusCode);
        db.ChangeTracker.Clear();
        var queued = await db.InboundEmailJobs.SingleAsync(item => item.ChannelId == channel.Id && item.InboundReceipt!.ProviderEventId == token);
        Assert.Equal("pending", queued.Status);
    }

    [Fact]
    public async Task Inbound_reservation_persists_relative_key_and_stores_under_communications_scope()
    {
        var token = $"inbound-storage-{Guid.NewGuid():N}";
        var channel = await CreateMailgunChannelAsync(token, "inbound-storage");
        using var client = factory.CreateClient();

        var response = await PostInboundAsync(client, channel.Id, channel.SigningKey, token);

        Assert.Equal(HttpStatusCode.OK, response.StatusCode);
        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<CommunicationsDbContext>();
        var job = await db.InboundEmailJobs.SingleAsync(item => item.ChannelId == channel.Id);
        Assert.StartsWith("inbound/", job.RawMimeStorageKey);
        Assert.DoesNotContain("communications/", job.RawMimeStorageKey);
        Assert.Contains($"communications/{job.RawMimeStorageKey}", factory.ObjectStore.PhysicalKeys);
    }

    [Fact]
    public async Task Raw_mime_put_before_ownership_is_reclaimed_by_cleanup_worker()
    {
        await factory.ResetChannelStateAsync();
        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<CommunicationsDbContext>();
        var channel = await db.Channels.SingleAsync();
        var receiptId = Guid.NewGuid();
        var key = $"inbound/{channel.Id:N}/{receiptId:N}.eml";
        var now = DateTimeOffset.UtcNow;
        db.InboundReceipts.Add(new InboundReceipt
        {
            Id = receiptId,
            ChannelId = channel.Id,
            Provider = "mailgun",
            ProviderEventId = $"crash-{Guid.NewGuid():N}",
            Status = "reserved",
            ReservationExpiresAt = now.AddMinutes(-1),
            ReceivedAt = now,
        });
        db.AttachmentCleanupRecords.Add(new AttachmentCleanupRecord
        {
            Id = Guid.NewGuid(),
            StorageKey = key,
            Status = "staged",
            NextAttemptAt = now,
            ReservationExpiresAt = now.AddMinutes(-1),
            CreatedAt = now,
        });
        await db.SaveChangesAsync();
        await factory.CommunicationsStore.PutAsync(key, new MemoryStream("raw mime"u8.ToArray()), "message/rfc822");

        var cleaned = await scope.ServiceProvider.GetRequiredService<AttachmentCleanupService>()
            .CleanupBatchAsync(DateTimeOffset.UtcNow, CancellationToken.None);

        Assert.Equal(1, cleaned);
        Assert.False(await factory.CommunicationsStore.ExistsAsync(key));
    }

    [Fact]
    public async Task Ai_interaction_audit_row_can_be_written_after_empty_database_migration()
    {
        var channel = await CreateMailgunChannelAsync($"ai-{Guid.NewGuid():N}", "ai");
        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<CommunicationsDbContext>();
        var now = DateTimeOffset.UtcNow;
        var conversation = new Conversation { Id = Guid.NewGuid(), ChannelId = channel.Id, Subject = "AI audit", LastActivityAt = now, CreatedAt = now };
        db.Conversations.Add(conversation);
        db.AiInteractions.Add(new AiInteraction
        {
            Id = Guid.NewGuid(),
            ConversationId = conversation.Id,
            Operation = "draft",
            Provider = "openai",
            Model = "test",
            ContextDigest = new string('a', 64),
            ContextVersion = "v1",
            ResultSummary = "draft_generated",
            CreatedAt = now,
        });
        await db.SaveChangesAsync();
        Assert.True(await db.AiInteractions.AnyAsync(item => item.ConversationId == conversation.Id));
    }

    private async Task<(Guid Id, string SigningKey)> CreateMailgunChannelAsync(string token, string suffix)
    {
        var signingKey = $"signing-key-{Guid.NewGuid():N}";
        var apiKey = $"api-key-{Guid.NewGuid():N}";
        await using var scope = factory.Services.CreateAsyncScope();
        var protector = scope.ServiceProvider.GetRequiredService<MailboxCredentialProtector>();
        var db = scope.ServiceProvider.GetRequiredService<CommunicationsDbContext>();
        var channel = new Channel
        {
            Id = Guid.NewGuid(),
            Type = "email",
            Address = $"{suffix}-{Guid.NewGuid():N}@integration.test",
            Provider = "mailgun",
            IsActive = true,
            CreatedAt = DateTimeOffset.UtcNow,
        };
        channel.Credential = new ChannelCredential
        {
            Id = Guid.NewGuid(),
            ChannelId = channel.Id,
            SettingsJson = JsonSerializer.Serialize(new MailgunProviderSettings("example.test", "us"), SmtpDeliveryProvider.JsonOptions),
            SecretCiphertext = protector.Protect(JsonSerializer.Serialize(new MailgunCredentialSecrets(apiKey, signingKey), SmtpDeliveryProvider.JsonOptions)),
            CreatedAt = DateTimeOffset.UtcNow,
        };
        db.Channels.Add(channel);
        await db.SaveChangesAsync();
        return (channel.Id, signingKey);
    }

    private static async Task<HttpResponseMessage> PostInboundAsync(HttpClient client, Guid channelId, string signingKey, string token, string? cc = null)
    {
        var timestamp = DateTimeOffset.UtcNow.ToUnixTimeSeconds().ToString(System.Globalization.CultureInfo.InvariantCulture);
        using var hmac = new HMACSHA256(Encoding.UTF8.GetBytes(signingKey));
        var signature = Convert.ToHexString(hmac.ComputeHash(Encoding.ASCII.GetBytes(timestamp + token))).ToLowerInvariant();
        var values = new Dictionary<string, string>
        {
            ["timestamp"] = timestamp,
            ["token"] = token,
            ["signature"] = signature,
            ["sender"] = "sender@example.test",
            ["from"] = "Sender <sender@example.test>",
            ["recipient"] = "inbound@example.test",
            ["subject"] = "Inbound subject",
            ["body-plain"] = "Inbound body",
        };
        if (cc is not null) values["cc"] = cc;
        return await client.PostAsync($"/api/v1/communications/inbound/mailgun/{channelId}", new FormUrlEncodedContent(values));
    }
}