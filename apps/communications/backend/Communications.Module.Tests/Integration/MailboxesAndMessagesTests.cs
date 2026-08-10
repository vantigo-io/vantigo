using System.Net;
using System.Net.Http.Json;

namespace Vantigo.Communications.Module.Tests.Integration;

[Collection(CommunicationsModuleCollection.Name)]
public sealed class MailboxesAndMessagesTests(CommunicationsModuleFactory factory)
{
    [Fact]
    public async Task Creating_a_second_mailbox_succeeds_and_duplicate_address_is_rejected()
    {
        await factory.ResetMailboxStateAsync();
        try
        {
            using var owner = await factory.CreateAuthenticatedClientAsync();
            var created = await owner.PostAsJsonAsync("/api/v1/communications/mailboxes", new
            {
                fromAddress = "second@integration.test",
                displayName = "Second mailbox",
            });

            Assert.Equal(HttpStatusCode.Created, created.StatusCode);
            var duplicate = await owner.PostAsJsonAsync("/api/v1/communications/mailboxes", new
            {
                fromAddress = "second@integration.test",
                displayName = "Duplicate mailbox",
            });

            Assert.Equal(HttpStatusCode.Conflict, duplicate.StatusCode);
            Assert.Equal("mailbox_address_exists", (await duplicate.Content.ReadFromJsonAsync<ErrorEnvelope>())!.Error.Code);
        }
        finally
        {
            await factory.ResetMailboxStateAsync();
        }
    }

    [Fact]
    public async Task Default_mailbox_cannot_be_demoted_or_deactivated_without_a_replacement()
    {
        await factory.ResetMailboxStateAsync();
        try
        {
            using var owner = await factory.CreateAuthenticatedClientAsync();
            var bootstrap = (await owner.GetFromJsonAsync<IReadOnlyList<MailboxResponseModel>>("/api/v1/communications/mailboxes"))!.Single();
            var created = await owner.PostAsJsonAsync("/api/v1/communications/mailboxes", new
            {
                fromAddress = "default-switch@integration.test",
                isDefault = true,
            });
            var second = (await created.Content.ReadFromJsonAsync<MailboxResponseModel>())!;

            Assert.Equal(HttpStatusCode.Created, created.StatusCode);
            Assert.True(second.IsDefault);
            Assert.False((await owner.GetFromJsonAsync<MailboxResponseModel>("/api/v1/communications/mailboxes/" + bootstrap.Id))!.IsDefault);

            var demote = await owner.PutAsJsonAsync("/api/v1/communications/mailboxes/" + second.Id, new { isDefault = false });
            Assert.Equal(HttpStatusCode.Conflict, demote.StatusCode);
            Assert.Equal("mailbox_default_required", (await demote.Content.ReadFromJsonAsync<ErrorEnvelope>())!.Error.Code);

            var promoteBootstrap = await owner.PutAsJsonAsync("/api/v1/communications/mailboxes/" + bootstrap.Id, new { isDefault = true });
            Assert.Equal(HttpStatusCode.OK, promoteBootstrap.StatusCode);
            var deactivate = await owner.PutAsJsonAsync("/api/v1/communications/mailboxes/" + bootstrap.Id, new { isActive = false });
            Assert.Equal(HttpStatusCode.Conflict, deactivate.StatusCode);
            Assert.Equal("mailbox_default_required", (await deactivate.Content.ReadFromJsonAsync<ErrorEnvelope>())!.Error.Code);
        }
        finally
        {
            await factory.ResetMailboxStateAsync();
        }
    }

    [Fact]
    public async Task Explicit_mailbox_is_used_and_invalid_or_reused_mailbox_requests_are_rejected()
    {
        await factory.ResetMailboxStateAsync();
        try
        {
            using var owner = await factory.CreateAuthenticatedClientAsync();
            var first = await CreateMailboxAsync(owner, "explicit-one@integration.test");
            var second = await CreateMailboxAsync(owner, "explicit-two@integration.test");
            using var service = await CreateServiceClientAsync();

            var firstMessage = await CreateMessageAsync(service, ValidPayload(first.Id), "explicit-mailbox-key");
            Assert.Equal(HttpStatusCode.Created, firstMessage.StatusCode);
            var created = (await firstMessage.Content.ReadFromJsonAsync<CreateMessageResponse>())!;
            var detail = await owner.GetFromJsonAsync<MessageDetailModel>("/api/v1/communications/messages/" + created.MessageId);
            Assert.Equal(first.Id, detail!.MailboxId);
            Assert.Equal(first.Id, detail.Mailbox.Id);
            Assert.Equal(first.FromAddress, detail.Mailbox.FromAddress);

            var unknown = await CreateMessageAsync(service, ValidPayload(Guid.NewGuid()), "unknown-mailbox-key");
            Assert.Equal((HttpStatusCode)422, unknown.StatusCode);
            Assert.Equal("mailbox_invalid", (await unknown.Content.ReadFromJsonAsync<ErrorEnvelope>())!.Error.Code);

            var inactiveUpdate = await owner.PutAsJsonAsync("/api/v1/communications/mailboxes/" + second.Id, new { isActive = false });
            Assert.Equal(HttpStatusCode.OK, inactiveUpdate.StatusCode);
            var inactive = await CreateMessageAsync(service, ValidPayload(second.Id), "inactive-mailbox-key");
            Assert.Equal((HttpStatusCode)422, inactive.StatusCode);
            Assert.Equal("mailbox_invalid", (await inactive.Content.ReadFromJsonAsync<ErrorEnvelope>())!.Error.Code);

            var reused = await CreateMessageAsync(service, ValidPayload(second.Id), "explicit-mailbox-key");
            Assert.Equal(HttpStatusCode.Conflict, reused.StatusCode);
            Assert.Equal("idempotency_key_reused", (await reused.Content.ReadFromJsonAsync<ErrorEnvelope>())!.Error.Code);
        }
        finally
        {
            await factory.ResetMailboxStateAsync();
        }
    }

    [Fact]
    public async Task Message_list_mailbox_filter_returns_only_matching_messages()
    {
        await factory.ResetMailboxStateAsync();
        try
        {
            using var owner = await factory.CreateAuthenticatedClientAsync();
            var first = await CreateMailboxAsync(owner, "filter-one@integration.test");
            var second = await CreateMailboxAsync(owner, "filter-two@integration.test");
            using var service = await CreateServiceClientAsync();

            Assert.Equal(HttpStatusCode.Created, (await CreateMessageAsync(service, ValidPayload(first.Id), "filter-one-key")).StatusCode);
            Assert.Equal(HttpStatusCode.Created, (await CreateMessageAsync(service, ValidPayload(second.Id), "filter-two-key")).StatusCode);

            var filtered = await owner.GetFromJsonAsync<PaginatedMessages>("/api/v1/communications/messages?mailboxId=" + first.Id);
            Assert.Single(filtered!.Data);
            Assert.Equal(first.Id, filtered.Data[0].Mailbox.Id);
            Assert.Equal(1, filtered.Pagination.TotalCount);
        }
        finally
        {
            await factory.ResetMailboxStateAsync();
        }
    }

    [Fact]
    public async Task Mailbox_verification_failure_returns_the_422_error_contract()
    {
        await factory.ResetMailboxStateAsync();
        try
        {
            using var owner = await factory.CreateAuthenticatedClientAsync();
            var response = await owner.PostAsJsonAsync("/api/v1/communications/mailboxes", new
            {
                fromAddress = "mailgun-verify@integration.test",
                provider = "mailgun",
                mailgun = new { domain = "integration.test", region = "us", apiKey = "invalid-test-key" },
            });
            Assert.Equal(HttpStatusCode.Created, response.StatusCode);
            var mailbox = (await response.Content.ReadFromJsonAsync<MailboxResponseModel>())!;

            var verification = await owner.PostAsync("/api/v1/communications/mailboxes/" + mailbox.Id + "/verify", null);
            Assert.Equal((HttpStatusCode)422, verification.StatusCode);
            var error = (await verification.Content.ReadFromJsonAsync<ErrorEnvelope>())!.Error;
            Assert.Equal("verification_failed", error.Code);
            Assert.Contains("Mailgun returned 502", error.Message);
        }
        finally
        {
            await factory.ResetMailboxStateAsync();
        }
    }

    private Task<HttpClient> CreateServiceClientAsync()
    {
        return factory.CreateAuthenticatedClientAsync();
    }

    private static async Task<MailboxResponseModel> CreateMailboxAsync(HttpClient owner, string address)
    {
        var response = await owner.PostAsJsonAsync("/api/v1/communications/mailboxes", new { fromAddress = address });
        Assert.Equal(HttpStatusCode.Created, response.StatusCode);
        return (await response.Content.ReadFromJsonAsync<MailboxResponseModel>())!;
    }

    private static Task<HttpResponseMessage> CreateMessageAsync(HttpClient client, object payload, string idempotencyKey)
    {
        var request = new HttpRequestMessage(HttpMethod.Post, "/api/v1/communications/messages")
        {
            Content = JsonContent.Create(payload),
        };
        request.Headers.Add("Idempotency-Key", idempotencyKey);
        return client.SendAsync(request);
    }

    [Fact]
    public async Task Mailbox_credentials_can_be_added_and_replaced()
    {
        await factory.ResetMailboxStateAsync();
        try
        {
            using var owner = await factory.CreateAuthenticatedClientAsync();
            var created = await owner.PostAsJsonAsync("/api/v1/communications/mailboxes", new
            {
                fromAddress = "credential-replace@integration.test",
                provider = "smtp",
                smtp = new { host = "smtp.example.test", port = 587, useSsl = false, username = "user", password = "secret" },
            });
            Assert.Equal(HttpStatusCode.Created, created.StatusCode);
            var mailbox = (await created.Content.ReadFromJsonAsync<MailboxResponseModel>())!;
            Assert.True(mailbox.HasCredentials);

            // Replace the whole credential config with a different provider.
            var replaced = await owner.PutAsJsonAsync("/api/v1/communications/mailboxes/" + mailbox.Id, new
            {
                provider = "mailgun",
                mailgun = new { domain = "mg.example.test", region = "eu", apiKey = "key-123" },
            });
            Assert.Equal(HttpStatusCode.OK, replaced.StatusCode);
            var updated = (await replaced.Content.ReadFromJsonAsync<MailboxResponseModel>())!;
            Assert.Equal("mailgun", updated.Provider);
            Assert.True(updated.HasCredentials);
            Assert.Equal("mg.example.test", updated.Settings!.Domain);

            // Also from credential-less state (bootstrap mailbox) to mailgun.
            var bootstrap = (await owner.GetFromJsonAsync<IReadOnlyList<MailboxResponseModel>>("/api/v1/communications/mailboxes"))!
                .Single(item => item.Id != mailbox.Id);
            Assert.False(bootstrap.HasCredentials);
            var upgraded = await owner.PutAsJsonAsync("/api/v1/communications/mailboxes/" + bootstrap.Id, new
            {
                provider = "mailgun",
                mailgun = new { domain = "mg2.example.test", region = "us", apiKey = "key-456" },
            });
            Assert.Equal(HttpStatusCode.OK, upgraded.StatusCode);
            Assert.True((await upgraded.Content.ReadFromJsonAsync<MailboxResponseModel>())!.HasCredentials);

            // Restore bootstrap mailbox to credential-less smtp for other tests.
            var restore = await owner.PutAsJsonAsync("/api/v1/communications/mailboxes/" + bootstrap.Id, new { provider = "smtp" });
            Assert.Equal(HttpStatusCode.OK, restore.StatusCode);
        }
        finally
        {
            await factory.ResetMailboxStateAsync();
        }
    }

    private static object ValidPayload(Guid mailboxId) => new
    {
        subject = "mailbox test",
        textBody = "body",
        mailboxId,
        to = new[] { new { email = "recipient@example.test" } },
    };

    private sealed record ErrorEnvelope(ErrorBody Error);
    private sealed record ErrorBody(string Code, string Message);
    private sealed record CreateMessageResponse(Guid MessageId, string Status, string IdempotencyKey);
    private sealed record MailboxResponseModel(Guid Id, string FromAddress, string? DisplayName, DateTimeOffset CreatedAt,
        bool IsActive, string Provider, bool IsDefault, bool HasCredentials, MailboxSettingsModel? Settings);
    private sealed record MailboxSettingsModel(string? Host, int? Port, bool? UseSsl, string? Username, string? Domain, string? Region);
    private sealed record MailboxSummaryModel(Guid Id, string FromAddress, string? DisplayName);
    private sealed record MessageDetailModel(Guid Id, Guid MailboxId, string Subject, string? TextBody, string? HtmlBody,
        DateTimeOffset CreatedAt, string? Source, IReadOnlyList<object> Deliveries, IReadOnlyList<object> ExternalLinks, MailboxSummaryModel Mailbox);
    private sealed record MessageListModel(Guid Id, string Subject, DateTimeOffset CreatedAt, int RecipientCount, string Status, string? Source, MailboxSummaryModel Mailbox);
    private sealed record PaginatedMessages(IReadOnlyList<MessageListModel> Data, PaginationModel Pagination);
    private sealed record PaginationModel(int Page, int PageSize, int TotalCount, int TotalPages, bool HasNextPage, bool HasPreviousPage);
}