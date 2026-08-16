using System.Net;
using System.Net.Http.Json;
using System.Text.Json;

using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;

using Vantigo.Communications.Database.Communications;
using Vantigo.Communications.Services;
using Vantigo.Identity.Database.Accounts;

namespace Vantigo.Communications.Module.Tests.Integration;

[Collection(CommunicationsModuleCollection.Name)]
public sealed class CommunicationsEndpointsTests(CommunicationsModuleFactory factory)
{
    [Fact]
    public async Task Channel_crud_and_conversation_list_are_available()
    {
        using var client = await factory.CreateAuthenticatedClientAsync();
        var channels = await client.GetFromJsonAsync<IReadOnlyList<ChannelResponse>>("/api/v1/communications/channels");
        Assert.NotEmpty(channels!);
        var created = await client.PostAsJsonAsync("/api/v1/communications/channels", new { type = "email", address = $"{Guid.NewGuid():N}@integration.test" });
        Assert.Equal(HttpStatusCode.Created, created.StatusCode);
        Assert.Equal(HttpStatusCode.OK, (await client.GetAsync("/api/v1/communications/conversations")).StatusCode);
    }

    [Fact]
    public async Task Reply_creates_threading_metadata_and_read_state()
    {
        await factory.ResetChannelStateAsync();
        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<CommunicationsDbContext>(); var channel = await db.Channels.SingleAsync(); var participant = new Participant { Id = Guid.NewGuid(), ChannelId = channel.Id, Address = "customer@example.test", CreatedAt = DateTimeOffset.UtcNow }; var now = DateTimeOffset.UtcNow; var conversation = new Conversation { Id = Guid.NewGuid(), ChannelId = channel.Id, Subject = "Question", LastActivityAt = now, CreatedAt = now }; conversation.Participants.Add(new ConversationParticipant { ConversationId = conversation.Id, ParticipantId = participant.Id, Participant = participant }); db.Conversations.Add(conversation); db.ConversationMessages.Add(new ConversationMessage { Id = Guid.NewGuid(), ConversationId = conversation.Id, Direction = "inbound", Participant = participant, Subject = "Question", TextBody = "hello", RfcMessageId = "<inbound@example.test>", OccurredAt = now, CreatedAt = now, ChannelMetadataJson = "{\"references\":[\"<root@example.test>\"]}" }); await db.SaveChangesAsync();
        using var client = await factory.CreateAuthenticatedClientAsync(); client.DefaultRequestHeaders.Add("Idempotency-Key", $"reply-{Guid.NewGuid():N}");
        var response = await client.PostAsJsonAsync($"/api/v1/communications/conversations/{conversation.Id}/reply", new { textBody = "reply" });
        Assert.True(response.StatusCode == HttpStatusCode.Created, await response.Content.ReadAsStringAsync());
        db.ChangeTracker.Clear(); var outbound = await db.ConversationMessages.SingleAsync(item => item.ConversationId == conversation.Id && item.Direction == "outbound"); Assert.Contains("inbound@example.test", outbound.ChannelMetadataJson); Assert.True(await db.OutboxJobs.AnyAsync(item => item.MessageId == outbound.Id));
        Assert.Equal(HttpStatusCode.OK, (await client.PostAsync($"/api/v1/communications/conversations/{conversation.Id}/read", null)).StatusCode);
    }

    [Fact]
    public async Task Invalid_manual_customer_update_is_rejected_without_mutating_existing_association()
    {
        await factory.ResetChannelStateAsync();
        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<CommunicationsDbContext>();
        var channel = await db.Channels.SingleAsync();
        var now = DateTimeOffset.UtcNow;
        var conversation = new Conversation
        {
            Id = Guid.NewGuid(),
            ChannelId = channel.Id,
            CustomerId = 7,
            CustomerAssociationSource = CustomerAssociationSources.Manual,
            LastActivityAt = now,
            CreatedAt = now,
        };
        db.Conversations.Add(conversation);
        await db.SaveChangesAsync();

        using var client = await factory.CreateAuthenticatedClientAsync();
        var response = await client.PatchAsJsonAsync($"/api/v1/communications/conversations/{conversation.Id}", new { customerId = 999999 });

        Assert.Equal(HttpStatusCode.UnprocessableEntity, response.StatusCode);
        db.ChangeTracker.Clear();
        var persisted = await db.Conversations.SingleAsync(item => item.Id == conversation.Id);
        Assert.Equal(7, persisted.CustomerId);
        Assert.Equal(CustomerAssociationSources.Manual, persisted.CustomerAssociationSource);
    }

    [Fact]
    public async Task Manual_customer_clear_removes_candidate_ids_and_association_source_from_responses()
    {
        await factory.ResetChannelStateAsync();
        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<CommunicationsDbContext>();
        var channel = await db.Channels.SingleAsync();
        var now = DateTimeOffset.UtcNow;
        var conversation = new Conversation
        {
            Id = Guid.NewGuid(),
            ChannelId = channel.Id,
            CustomerId = 7,
            CustomerAssociationSource = CustomerAssociationSources.Automatic,
            LastActivityAt = now,
            CreatedAt = now,
        };
        conversation.CustomerCandidates.Add(new ConversationCustomerCandidate { ConversationId = conversation.Id, CustomerId = 8, CreatedAt = now });
        conversation.CustomerCandidates.Add(new ConversationCustomerCandidate { ConversationId = conversation.Id, CustomerId = 9, CreatedAt = now });
        db.Conversations.Add(conversation);
        await db.SaveChangesAsync();

        using var client = await factory.CreateAuthenticatedClientAsync();
        using var clearContent = new StringContent("{\"customerId\":null}", System.Text.Encoding.UTF8, "application/json");
        var update = await client.PatchAsync($"/api/v1/communications/conversations/{conversation.Id}", clearContent);
        Assert.Equal(HttpStatusCode.OK, update.StatusCode);
        var detail = await client.GetFromJsonAsync<ConversationResponse>($"/api/v1/communications/conversations/{conversation.Id}");

        Assert.Null(detail!.CustomerId);
        Assert.Null(detail.CustomerAssociationSource);
        Assert.Empty(detail.CandidateCustomerIds);
    }

    [Fact]
    public async Task Mailgun_create_requires_signing_key_and_update_preserves_omitted_secret()
    {
        await factory.ResetChannelStateAsync();
        using var client = await factory.CreateAuthenticatedClientAsync();
        var address = $"mailgun-{Guid.NewGuid():N}@integration.test";
        var invalid = await client.PostAsJsonAsync("/api/v1/communications/channels", new
        {
            type = "email",
            address,
            provider = "mailgun",
            mailgun = new { domain = "example.test", region = "us", apiKey = "api-key" },
        });
        Assert.Equal(HttpStatusCode.BadRequest, invalid.StatusCode);

        var created = await client.PostAsJsonAsync("/api/v1/communications/channels", new
        {
            type = "email",
            address,
            provider = "mailgun",
            mailgun = new { domain = "example.test", region = "us", apiKey = "api-key", inboundSigningKey = "signing-key" },
        });
        Assert.Equal(HttpStatusCode.Created, created.StatusCode);
        using var createdJson = JsonDocument.Parse(await created.Content.ReadAsStringAsync());
        var channelId = createdJson.RootElement.GetProperty("id").GetGuid();

        var updated = await client.PutAsJsonAsync($"/api/v1/communications/channels/{channelId}", new
        {
            provider = "mailgun",
            mailgun = new { domain = "example.test", region = "us" },
        });
        Assert.Equal(HttpStatusCode.OK, updated.StatusCode);
        Assert.DoesNotContain("api-key", await updated.Content.ReadAsStringAsync());
    }

    [Fact]
    public async Task Staged_attachment_status_is_scoped_and_cache_safe()
    {
        await factory.ResetChannelStateAsync();
        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<CommunicationsDbContext>();
        var accounts = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        var owner = await accounts.Users.SingleAsync(user => user.Email == "owner@integration.test");
        var channel = await db.Channels.SingleAsync();
        var now = DateTimeOffset.UtcNow;
        var conversation = new Conversation { Id = Guid.NewGuid(), ChannelId = channel.Id, Subject = "Attachment status", LastActivityAt = now, CreatedAt = now };
        var otherConversation = new Conversation { Id = Guid.NewGuid(), ChannelId = channel.Id, Subject = "Other", LastActivityAt = now, CreatedAt = now };
        db.Conversations.AddRange(conversation, otherConversation);
        var upload = new AttachmentUpload
        {
            Id = Guid.NewGuid(),
            ConversationId = conversation.Id,
            UploadedByUserId = owner.Id,
            IdempotencyKey = $"status-{Guid.NewGuid():N}",
            FileName = "invoice.pdf",
            ContentType = "application/pdf",
            SizeBytes = 42,
            ContentHash = "hash",
            StorageKey = "opaque/not-in-response",
            ScanStatus = "pending",
            NextScanAt = now,
            ExpiresAt = now.AddHours(1),
            CreatedAt = now,
        };
        db.AttachmentUploads.Add(upload);
        await db.SaveChangesAsync();

        using var client = await factory.CreateAuthenticatedClientAsync();
        var response = await client.GetAsync($"/api/v1/communications/conversations/{conversation.Id}/attachments/{upload.Id}");
        Assert.Equal(HttpStatusCode.OK, response.StatusCode);
        Assert.True(response.Headers.CacheControl?.NoStore);
        Assert.True(response.Headers.CacheControl?.NoCache);
        var body = await response.Content.ReadAsStringAsync();
        Assert.Contains("\"scanStatus\":\"pending\"", body);
        Assert.Contains("\"ready\":false", body);
        Assert.DoesNotContain(upload.StorageKey, body);

        Assert.Equal(HttpStatusCode.NotFound, (await client.GetAsync($"/api/v1/communications/conversations/{otherConversation.Id}/attachments/{upload.Id}")).StatusCode);
        using var anonymous = factory.CreateClient();
        Assert.Equal(HttpStatusCode.Unauthorized, (await anonymous.GetAsync($"/api/v1/communications/conversations/{conversation.Id}/attachments/{upload.Id}")).StatusCode);

        foreach (var status in new[] { "scanning", "clean", "quarantined", "failed" })
        {
            await db.AttachmentUploads.Where(item => item.Id == upload.Id).ExecuteUpdateAsync(setters => setters.SetProperty(item => item.ScanStatus, status));
            var current = await client.GetFromJsonAsync<AttachmentUploadStatusResponse>($"/api/v1/communications/conversations/{conversation.Id}/attachments/{upload.Id}");
            Assert.Equal(status, current!.ScanStatus);
            Assert.Equal(status == "clean", current.Ready);
        }
    }

    [Fact]
    public async Task Clean_attachment_download_is_application_stream_and_requires_clean_status()
    {
        await factory.ResetChannelStateAsync();
        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<CommunicationsDbContext>();
        var channel = await db.Channels.SingleAsync();
        var now = DateTimeOffset.UtcNow;
        var conversation = new Conversation { Id = Guid.NewGuid(), ChannelId = channel.Id, Subject = "Download", LastActivityAt = now, CreatedAt = now };
        var message = new ConversationMessage { Id = Guid.NewGuid(), ConversationId = conversation.Id, Direction = "inbound", TextBody = "body", OccurredAt = now, CreatedAt = now };
        var attachment = new MessageAttachment
        {
            Id = Guid.NewGuid(),
            MessageId = message.Id,
            FileName = "../invoice.pdf",
            ContentType = "application/pdf; charset=utf-8",
            SizeBytes = 11,
            ContentHash = "hash",
            StorageKey = $"attachments/{message.Id:N}/{Guid.NewGuid():N}",
            ScanStatus = "clean",
            CreatedAt = now,
        };
        message.Attachments.Add(attachment);
        conversation.Messages.Add(message);
        db.Conversations.Add(conversation);
        await db.SaveChangesAsync();
        await factory.CommunicationsStore.PutAsync(attachment.StorageKey, new MemoryStream("attachment!"u8.ToArray()), attachment.ContentType);

        using var client = await factory.CreateAuthenticatedClientAsync();
        using var response = await client.GetAsync($"/api/v1/communications/attachments/{attachment.Id}/download");
        Assert.Equal(HttpStatusCode.OK, response.StatusCode);
        Assert.False(response.Headers.Location is not null);
        Assert.Equal("application/pdf", response.Content.Headers.ContentType?.MediaType);
        Assert.Equal("attachment", response.Content.Headers.ContentDisposition?.DispositionType);
        Assert.Equal("invoice.pdf", response.Content.Headers.ContentDisposition?.FileNameStar ?? response.Content.Headers.ContentDisposition?.FileName?.Trim('"'));
        Assert.Equal(11, response.Content.Headers.ContentLength);
        Assert.Equal("attachment!", await response.Content.ReadAsStringAsync());

        await db.MessageAttachments.Where(item => item.Id == attachment.Id).ExecuteUpdateAsync(setters => setters.SetProperty(item => item.ScanStatus, "pending"));
        Assert.Equal(HttpStatusCode.NotFound, (await client.GetAsync($"/api/v1/communications/attachments/{attachment.Id}/download")).StatusCode);
        await db.MessageAttachments.Where(item => item.Id == attachment.Id).ExecuteUpdateAsync(setters => setters.SetProperty(item => item.ScanStatus, "quarantined"));
        Assert.Equal(HttpStatusCode.NotFound, (await client.GetAsync($"/api/v1/communications/attachments/{attachment.Id}/download")).StatusCode);

        using var anonymous = factory.CreateClient();
        Assert.Equal(HttpStatusCode.Unauthorized, (await anonymous.GetAsync($"/api/v1/communications/attachments/{attachment.Id}/download")).StatusCode);
    }

    [Fact]
    public async Task Reply_rejects_when_latest_sender_is_suppressed()
    {
        await factory.ResetChannelStateAsync();
        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<CommunicationsDbContext>();
        var (conversation, sender) = await CreateReplyConversationAsync(db, []);
        db.Suppressions.Add(new Suppression { Id = Guid.NewGuid(), NormalizedEmailAddress = EmailSuppression.Normalize(sender.Address), CreatedAt = DateTimeOffset.UtcNow });
        await db.SaveChangesAsync();

        using var client = await factory.CreateAuthenticatedClientAsync();
        client.DefaultRequestHeaders.Add("Idempotency-Key", $"suppressed-primary-{Guid.NewGuid():N}");
        var response = await client.PostAsJsonAsync($"/api/v1/communications/conversations/{conversation.Id}/reply", new { textBody = "blocked" });
        Assert.Equal(HttpStatusCode.UnprocessableEntity, response.StatusCode);
        Assert.False(await db.ConversationMessages.AnyAsync(item => item.ConversationId == conversation.Id && item.Direction == "outbound"));
    }

    [Fact]
    public async Task Reply_all_rejects_when_any_latest_cc_is_suppressed()
    {
        await factory.ResetChannelStateAsync();
        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<CommunicationsDbContext>();
        var cc = $"cc-{Guid.NewGuid():N}@example.test";
        var (conversation, _) = await CreateReplyConversationAsync(db, [cc]);
        db.Suppressions.Add(new Suppression { Id = Guid.NewGuid(), NormalizedEmailAddress = EmailSuppression.Normalize(cc), CreatedAt = DateTimeOffset.UtcNow });
        await db.SaveChangesAsync();

        using var client = await factory.CreateAuthenticatedClientAsync();
        client.DefaultRequestHeaders.Add("Idempotency-Key", $"suppressed-cc-{Guid.NewGuid():N}");
        var response = await client.PostAsJsonAsync($"/api/v1/communications/conversations/{conversation.Id}/reply", new { textBody = "blocked", replyMode = "reply_all" });
        Assert.Equal(HttpStatusCode.UnprocessableEntity, response.StatusCode);
        Assert.False(await db.ConversationMessages.AnyAsync(item => item.ConversationId == conversation.Id && item.Direction == "outbound"));
    }

    [Fact]
    public async Task Reply_all_deduplicates_cc_and_queues_all_unsuppressed_destinations()
    {
        await factory.ResetChannelStateAsync();
        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<CommunicationsDbContext>();
        var cc = $"cc-{Guid.NewGuid():N}@example.test";
        var (conversation, _) = await CreateReplyConversationAsync(db, [cc, cc]);

        using var client = await factory.CreateAuthenticatedClientAsync();
        client.DefaultRequestHeaders.Add("Idempotency-Key", $"reply-all-{Guid.NewGuid():N}");
        var response = await client.PostAsJsonAsync($"/api/v1/communications/conversations/{conversation.Id}/reply", new { textBody = "sent", replyMode = "reply_all" });
        Assert.Equal(HttpStatusCode.Created, response.StatusCode);
        db.ChangeTracker.Clear();
        var outbound = await db.ConversationMessages.Include(item => item.Deliveries).SingleAsync(item => item.ConversationId == conversation.Id && item.Direction == "outbound");
        Assert.Equal(2, outbound.Deliveries.Count);
        Assert.Single(outbound.Deliveries, delivery => delivery.RecipientType == "to");
        Assert.Single(outbound.Deliveries, delivery => delivery.RecipientType == "cc");
    }

    private static async Task<(Conversation Conversation, Participant Sender)> CreateReplyConversationAsync(CommunicationsDbContext db, IReadOnlyList<string> cc)
    {
        var channel = await db.Channels.SingleAsync();
        var sender = new Participant { Id = Guid.NewGuid(), ChannelId = channel.Id, Address = $"sender-{Guid.NewGuid():N}@example.test", CreatedAt = DateTimeOffset.UtcNow };
        var now = DateTimeOffset.UtcNow;
        var conversation = new Conversation { Id = Guid.NewGuid(), ChannelId = channel.Id, Subject = "Reply", LastActivityAt = now, CreatedAt = now };
        conversation.Participants.Add(new ConversationParticipant { ConversationId = conversation.Id, ParticipantId = sender.Id, Participant = sender, Role = "sender" });
        db.Conversations.Add(conversation);
        db.ConversationMessages.Add(new ConversationMessage
        {
            Id = Guid.NewGuid(),
            ConversationId = conversation.Id,
            Direction = "inbound",
            Participant = sender,
            Subject = "Reply",
            TextBody = "hello",
            RfcMessageId = "<reply@example.test>",
            OccurredAt = now,
            CreatedAt = now,
            ChannelMetadataJson = JsonSerializer.Serialize(new EmailThreadMetadata(Cc: cc), SmtpDeliveryProvider.JsonOptions),
        });
        await db.SaveChangesAsync();
        return (conversation, sender);
    }

    private sealed record ChannelResponse(Guid Id, string Type, string Address);
    private sealed record ConversationResponse(int? CustomerId, string? CustomerAssociationSource, IReadOnlyList<int> CandidateCustomerIds);
    private sealed record AttachmentUploadStatusResponse(Guid Id, string FileName, string ContentType, long SizeBytes, string ScanStatus, bool IsInline, DateTimeOffset ExpiresAt, bool Ready);
}