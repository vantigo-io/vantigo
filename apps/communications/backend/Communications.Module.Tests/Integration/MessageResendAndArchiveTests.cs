using System.Net;
using System.Net.Http.Json;

using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;

using Vantigo.Communications.Database.Communications;
using Vantigo.Communications.Services;

namespace Vantigo.Communications.Module.Tests.Integration;

[Collection(CommunicationsModuleCollection.Name)]
public sealed class MessageResendAndArchiveTests(CommunicationsModuleFactory factory)
{
    [Fact]
    public async Task Resend_failed_scope_requeues_only_failed_recipients_and_worker_sends_to_them()
    {
        using var scope = factory.Services.CreateScope();
        var db = scope.ServiceProvider.GetRequiredService<CommunicationsDbContext>();
        var message = await SeedMessageAsync(db, "resend failed test",
            ("accepted@example.test", "relay_accepted"),
            ("failed@example.test", "submission_failed"));
        await SeedJobAsync(db, message.Id, "failed");

        using var owner = await factory.CreateAuthenticatedClientAsync();
        var response = await owner.PostAsJsonAsync($"/api/v1/communications/messages/{message.Id}/resend", new { scope = "failed" });
        Assert.Equal(HttpStatusCode.OK, response.StatusCode);
        var body = (await response.Content.ReadFromJsonAsync<ResendResponseModel>())!;
        Assert.Equal("failed", body.Scope);
        Assert.Equal(1, body.RequeuedRecipientCount);

        db.ChangeTracker.Clear();
        Assert.Equal("queued", await DeliveryStatusAsync(db, message.Id, "failed@example.test"));
        Assert.Equal("relay_accepted", await DeliveryStatusAsync(db, message.Id, "accepted@example.test"));
        Assert.True(await db.OutboxJobs.AnyAsync(job => job.MessageId == message.Id && job.Status == "pending"));
        Assert.True(await db.MessageEvents.AnyAsync(item => item.MessageId == message.Id && item.EventType == "resend_requested"));

        var processor = scope.ServiceProvider.GetRequiredService<OutboxJobProcessor>();
        Assert.True(await processor.ProcessOneAsync(message.Id, CancellationToken.None));
        db.ChangeTracker.Clear();
        Assert.Equal("relay_accepted", await DeliveryStatusAsync(db, message.Id, "failed@example.test"));
        var envelope = Assert.Single(factory.Sender.Envelopes, item => item.Subject == "resend failed test");
        Assert.Equal(["failed@example.test"], envelope.To);
    }

    [Fact]
    public async Task Resend_all_scope_requeues_every_recipient()
    {
        using var scope = factory.Services.CreateScope();
        var db = scope.ServiceProvider.GetRequiredService<CommunicationsDbContext>();
        var message = await SeedMessageAsync(db, "resend all test",
            ("accepted-all@example.test", "relay_accepted"),
            ("failed-all@example.test", "submission_failed"));
        await SeedJobAsync(db, message.Id, "failed");

        using var owner = await factory.CreateAuthenticatedClientAsync();
        var response = await owner.PostAsJsonAsync($"/api/v1/communications/messages/{message.Id}/resend", new { scope = "all" });
        Assert.Equal(HttpStatusCode.OK, response.StatusCode);
        Assert.Equal(2, (await response.Content.ReadFromJsonAsync<ResendResponseModel>())!.RequeuedRecipientCount);

        db.ChangeTracker.Clear();
        Assert.Equal("queued", await DeliveryStatusAsync(db, message.Id, "accepted-all@example.test"));
        Assert.Equal("queued", await DeliveryStatusAsync(db, message.Id, "failed-all@example.test"));
    }

    [Fact]
    public async Task Resend_is_rejected_for_active_jobs_missing_failures_archived_messages_and_bad_scope()
    {
        using var scope = factory.Services.CreateScope();
        var db = scope.ServiceProvider.GetRequiredService<CommunicationsDbContext>();
        using var owner = await factory.CreateAuthenticatedClientAsync();

        var pendingMessage = await SeedMessageAsync(db, "resend pending", ("pending@example.test", "queued"));
        await SeedJobAsync(db, pendingMessage.Id, "pending");
        var pending = await owner.PostAsJsonAsync($"/api/v1/communications/messages/{pendingMessage.Id}/resend", new { scope = "all" });
        Assert.Equal(HttpStatusCode.Conflict, pending.StatusCode);
        Assert.Equal("resend_in_progress", (await pending.Content.ReadFromJsonAsync<ErrorEnvelope>())!.Error.Code);

        var acceptedMessage = await SeedMessageAsync(db, "resend no failures", ("done@example.test", "relay_accepted"));
        var noFailures = await owner.PostAsJsonAsync($"/api/v1/communications/messages/{acceptedMessage.Id}/resend", new { scope = "failed" });
        Assert.Equal(HttpStatusCode.BadRequest, noFailures.StatusCode);
        Assert.Equal("no_deliveries_to_resend", (await noFailures.Content.ReadFromJsonAsync<ErrorEnvelope>())!.Error.Code);

        var badScope = await owner.PostAsJsonAsync($"/api/v1/communications/messages/{acceptedMessage.Id}/resend", new { scope = "everything" });
        Assert.Equal(HttpStatusCode.BadRequest, badScope.StatusCode);
        Assert.Equal("invalid_scope", (await badScope.Content.ReadFromJsonAsync<ErrorEnvelope>())!.Error.Code);

        var archivedMessage = await SeedMessageAsync(db, "resend archived", ("archived@example.test", "submission_failed"));
        Assert.Equal(HttpStatusCode.OK, (await owner.PostAsync($"/api/v1/communications/messages/{archivedMessage.Id}/archive", null)).StatusCode);
        var archived = await owner.PostAsJsonAsync($"/api/v1/communications/messages/{archivedMessage.Id}/resend", new { scope = "failed" });
        Assert.Equal(HttpStatusCode.Conflict, archived.StatusCode);
        Assert.Equal("message_archived", (await archived.Content.ReadFromJsonAsync<ErrorEnvelope>())!.Error.Code);

        var missing = await owner.PostAsJsonAsync($"/api/v1/communications/messages/{Guid.NewGuid()}/resend", new { scope = "all" });
        Assert.Equal(HttpStatusCode.NotFound, missing.StatusCode);
    }

    [Fact]
    public async Task Archive_cancels_pending_send_hides_message_from_default_list_and_unarchive_restores_it()
    {
        using var scope = factory.Services.CreateScope();
        var db = scope.ServiceProvider.GetRequiredService<CommunicationsDbContext>();
        var message = await SeedMessageAsync(db, "archive test", ("queued-archive@example.test", "queued"));
        await SeedJobAsync(db, message.Id, "pending");

        using var owner = await factory.CreateAuthenticatedClientAsync();
        var archive = await owner.PostAsync($"/api/v1/communications/messages/{message.Id}/archive", null);
        Assert.Equal(HttpStatusCode.OK, archive.StatusCode);
        var detail = (await archive.Content.ReadFromJsonAsync<MessageDetailModel>())!;
        Assert.NotNull(detail.ArchivedAt);

        db.ChangeTracker.Clear();
        Assert.Equal("cancelled", await db.OutboxJobs.Where(job => job.MessageId == message.Id).Select(job => job.Status).SingleAsync());
        Assert.Equal("cancelled", await DeliveryStatusAsync(db, message.Id, "queued-archive@example.test"));
        Assert.True(await db.MessageEvents.AnyAsync(item => item.MessageId == message.Id && item.EventType == "archived"));

        // Archiving again is idempotent.
        Assert.Equal(HttpStatusCode.OK, (await owner.PostAsync($"/api/v1/communications/messages/{message.Id}/archive", null)).StatusCode);

        var visible = (await owner.GetFromJsonAsync<PaginatedMessages>("/api/v1/communications/messages?pageSize=100"))!;
        Assert.DoesNotContain(visible.Data, item => item.Id == message.Id);
        var all = (await owner.GetFromJsonAsync<PaginatedMessages>("/api/v1/communications/messages?pageSize=100&includeArchived=true"))!;
        var listed = Assert.Single(all.Data, item => item.Id == message.Id);
        Assert.NotNull(listed.ArchivedAt);

        var unarchive = await owner.PostAsync($"/api/v1/communications/messages/{message.Id}/unarchive", null);
        Assert.Equal(HttpStatusCode.OK, unarchive.StatusCode);
        Assert.Null((await unarchive.Content.ReadFromJsonAsync<MessageDetailModel>())!.ArchivedAt);
        var restored = (await owner.GetFromJsonAsync<PaginatedMessages>("/api/v1/communications/messages?pageSize=100"))!;
        Assert.Contains(restored.Data, item => item.Id == message.Id);
        db.ChangeTracker.Clear();
        Assert.True(await db.MessageEvents.AnyAsync(item => item.MessageId == message.Id && item.EventType == "unarchived"));
    }

    private static async Task<EmailMessage> SeedMessageAsync(CommunicationsDbContext db, string subject, params (string Email, string Status)[] deliveries)
    {
        var mailbox = await db.SharedMailboxes.SingleAsync(item => item.FromAddress == CommunicationsModuleFactory.BootstrapMailboxAddress);
        var now = DateTimeOffset.UtcNow;
        var message = new EmailMessage
        {
            Id = Guid.NewGuid(),
            MailboxId = mailbox.Id,
            Subject = subject,
            TextBody = "body",
            CreatedAt = now,
        };
        foreach (var (email, status) in deliveries)
            message.Deliveries.Add(new RecipientDelivery
            {
                Id = Guid.NewGuid(),
                MessageId = message.Id,
                EmailAddress = email,
                RecipientType = "to",
                Status = status,
                CreatedAt = now,
            });
        db.EmailMessages.Add(message);
        await db.SaveChangesAsync();
        return message;
    }

    private static async Task SeedJobAsync(CommunicationsDbContext db, Guid messageId, string status)
    {
        db.OutboxJobs.Add(new OutboxJob
        {
            Id = Guid.NewGuid(),
            MessageId = messageId,
            Status = status,
            CreatedAt = DateTimeOffset.UtcNow,
            NextAttemptAt = DateTimeOffset.UtcNow,
        });
        await db.SaveChangesAsync();
    }

    private static Task<string> DeliveryStatusAsync(CommunicationsDbContext db, Guid messageId, string email) =>
        db.RecipientDeliveries.Where(item => item.MessageId == messageId && item.EmailAddress == email)
            .Select(item => item.Status).SingleAsync();

    private sealed record ErrorEnvelope(ErrorBody Error);
    private sealed record ErrorBody(string Code, string Message);
    private sealed record ResendResponseModel(Guid MessageId, string Status, string Scope, int RequeuedRecipientCount);
    private sealed record MessageDetailModel(Guid Id, Guid MailboxId, string Subject, string? TextBody, string? HtmlBody,
        DateTimeOffset CreatedAt, string? Source, DateTimeOffset? ArchivedAt);
    private sealed record MessageListModel(Guid Id, string Subject, DateTimeOffset CreatedAt, int RecipientCount,
        string Status, string? Source, DateTimeOffset? ArchivedAt, MailboxSummaryModel Mailbox);
    private sealed record MailboxSummaryModel(Guid Id, string FromAddress, string? DisplayName);
    private sealed record PaginatedMessages(IReadOnlyList<MessageListModel> Data, PaginationModel Pagination);
    private sealed record PaginationModel(int Page, int PageSize, int TotalCount, int TotalPages, bool HasNextPage, bool HasPreviousPage);
}