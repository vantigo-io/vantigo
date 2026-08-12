using System.Net;
using System.Net.Http.Json;

namespace Vantigo.Communications.Module.Tests.Integration;

[Collection(CommunicationsModuleCollection.Name)]
public sealed class CommunicationsAuthorizationTests(CommunicationsModuleFactory factory)
{
    [Fact]
    public async Task Communications_permissions_are_narrow_and_owner_is_implicitly_allowed()
    {
        using var owner = await factory.CreateAuthenticatedClientAsync();
        Assert.Equal(HttpStatusCode.OK,
            (await owner.GetAsync("/api/v1/communications/messages")).StatusCode);
        Assert.Equal(HttpStatusCode.OK,
            (await owner.GetAsync("/api/v1/communications/mailboxes")).StatusCode);
        Assert.Equal(HttpStatusCode.OK,
            (await owner.GetAsync("/api/v1/communications/suppressions")).StatusCode);

        var noPermission = await factory.CreateUserAsync();
        using var user = await factory.CreateAuthenticatedClientAsync(noPermission.Email, noPermission.Password);
        Assert.Equal(HttpStatusCode.Forbidden,
            (await user.GetAsync("/api/v1/communications/messages")).StatusCode);

        var messagesView = await factory.CreateUserAsync("communications:messages-view");
        using var viewClient = await factory.CreateAuthenticatedClientAsync(messagesView.Email, messagesView.Password);
        Assert.Equal(HttpStatusCode.OK,
            (await viewClient.GetAsync("/api/v1/communications/messages")).StatusCode);
        Assert.Equal(HttpStatusCode.Forbidden,
            (await viewClient.PostAsJsonAsync("/api/v1/communications/messages", ValidPayload("view-cannot-send"))).StatusCode);
        Assert.Equal(HttpStatusCode.Forbidden,
            (await viewClient.PostAsync($"/api/v1/communications/messages/{Guid.NewGuid()}/archive", null)).StatusCode);

        var messagesSend = await factory.CreateUserAsync("communications:messages-send");
        using var sendClient = await factory.CreateAuthenticatedClientAsync(messagesSend.Email, messagesSend.Password);
        sendClient.DefaultRequestHeaders.Add("Idempotency-Key", $"send-{Guid.NewGuid():N}");
        Assert.Equal(HttpStatusCode.Created,
            (await sendClient.PostAsJsonAsync("/api/v1/communications/messages", ValidPayload("send-only"))).StatusCode);
        Assert.Equal(HttpStatusCode.Forbidden,
            (await sendClient.GetAsync("/api/v1/communications/messages")).StatusCode);
        Assert.Equal(HttpStatusCode.Forbidden,
            (await sendClient.PostAsync($"/api/v1/communications/messages/{Guid.NewGuid()}/archive", null)).StatusCode);

        var messagesManage = await factory.CreateUserAsync("communications:messages-manage");
        using var manageClient = await factory.CreateAuthenticatedClientAsync(messagesManage.Email, messagesManage.Password);
        Assert.Equal(HttpStatusCode.Forbidden,
            (await manageClient.PostAsync($"/api/v1/communications/messages/{Guid.NewGuid()}/archive", null)).StatusCode);
        Assert.Equal(HttpStatusCode.Forbidden,
            (await manageClient.GetAsync("/api/v1/communications/messages")).StatusCode);

        var mailboxesView = await factory.CreateUserAsync("communications:mailboxes-view");
        using var mailboxViewClient = await factory.CreateAuthenticatedClientAsync(mailboxesView.Email, mailboxesView.Password);
        Assert.Equal(HttpStatusCode.OK,
            (await mailboxViewClient.GetAsync("/api/v1/communications/mailboxes")).StatusCode);
        Assert.Equal(HttpStatusCode.Forbidden,
            (await mailboxViewClient.PutAsJsonAsync($"/api/v1/communications/mailboxes/{Guid.NewGuid()}", new { displayName = "Nope" })).StatusCode);

        var mailboxesManage = await factory.CreateUserAsync("communications:mailboxes-manage");
        using var mailboxManageClient = await factory.CreateAuthenticatedClientAsync(mailboxesManage.Email, mailboxesManage.Password);
        Assert.Equal(HttpStatusCode.Forbidden,
            (await mailboxManageClient.PutAsJsonAsync($"/api/v1/communications/mailboxes/{Guid.NewGuid()}", new { displayName = "Nope" })).StatusCode);
        Assert.Equal(HttpStatusCode.Forbidden,
            (await mailboxManageClient.GetAsync("/api/v1/communications/mailboxes")).StatusCode);

        var suppressionsView = await factory.CreateUserAsync("communications:suppressions-view");
        using var suppressionViewClient = await factory.CreateAuthenticatedClientAsync(suppressionsView.Email, suppressionsView.Password);
        Assert.Equal(HttpStatusCode.OK,
            (await suppressionViewClient.GetAsync("/api/v1/communications/suppressions")).StatusCode);
        Assert.Equal(HttpStatusCode.Forbidden,
            (await suppressionViewClient.DeleteAsync($"/api/v1/communications/suppressions/{Guid.NewGuid()}")).StatusCode);

        var suppressionsManage = await factory.CreateUserAsync("communications:suppressions-manage");
        using var suppressionManageClient = await factory.CreateAuthenticatedClientAsync(suppressionsManage.Email, suppressionsManage.Password);
        Assert.Equal(HttpStatusCode.Forbidden,
            (await suppressionManageClient.PostAsJsonAsync("/api/v1/communications/suppressions", new
            {
                emailAddress = "manage-only@example.test",
                reason = "test",
            })).StatusCode);
        Assert.Equal(HttpStatusCode.NotFound,
            (await suppressionManageClient.DeleteAsync($"/api/v1/communications/suppressions/{Guid.NewGuid()}")).StatusCode);
        Assert.Equal(HttpStatusCode.Forbidden,
            (await suppressionManageClient.GetAsync("/api/v1/communications/suppressions")).StatusCode);
    }

    [Fact]
    public async Task Disabled_user_is_denied_even_with_a_communications_permission()
    {
        var disabled = await factory.CreateUserAsync("communications:messages-view");
        using var client = await factory.CreateAuthenticatedClientAsync(disabled.Email, disabled.Password);
        await factory.DisableUserAsync(disabled.Id);

        Assert.True((await client.GetAsync("/api/v1/communications/messages")).StatusCode is
            HttpStatusCode.Unauthorized or HttpStatusCode.Forbidden);
    }

    [Fact]
    public async Task Message_manage_only_cannot_receive_archive_or_unarchive_message_content()
    {
        using var owner = await factory.CreateAuthenticatedClientAsync();
        var create = new HttpRequestMessage(HttpMethod.Post, "/api/v1/communications/messages")
        {
            Content = JsonContent.Create(new
            {
                subject = "authorization-sensitive-subject",
                textBody = "authorization-sensitive-body",
                to = new[] { new { email = "authorization-sensitive@example.test" } },
            }),
        };
        create.Headers.Add("Idempotency-Key", $"authorization-sensitive-{Guid.NewGuid():N}");
        var created = await owner.SendAsync(create);
        Assert.Equal(HttpStatusCode.Created, created.StatusCode);
        var messageId = (await created.Content.ReadFromJsonAsync<CreateResponse>())!.MessageId;

        var manageOnly = await factory.CreateUserAsync("communications:messages-manage");
        using var manager = await factory.CreateAuthenticatedClientAsync(manageOnly.Email, manageOnly.Password);

        var archive = await manager.PostAsync($"/api/v1/communications/messages/{messageId}/archive", null);
        Assert.Equal(HttpStatusCode.Forbidden, archive.StatusCode);
        var archiveBody = await archive.Content.ReadAsStringAsync();
        Assert.DoesNotContain("authorization-sensitive-body", archiveBody, StringComparison.Ordinal);
        Assert.DoesNotContain("authorization-sensitive@example.test", archiveBody, StringComparison.Ordinal);

        Assert.Equal(HttpStatusCode.OK,
            (await owner.PostAsync($"/api/v1/communications/messages/{messageId}/archive", null)).StatusCode);

        var unarchive = await manager.PostAsync($"/api/v1/communications/messages/{messageId}/unarchive", null);
        Assert.Equal(HttpStatusCode.Forbidden, unarchive.StatusCode);
        var unarchiveBody = await unarchive.Content.ReadAsStringAsync();
        Assert.DoesNotContain("authorization-sensitive-body", unarchiveBody, StringComparison.Ordinal);
        Assert.DoesNotContain("authorization-sensitive@example.test", unarchiveBody, StringComparison.Ordinal);

        var ownerUnarchive = await owner.PostAsync($"/api/v1/communications/messages/{messageId}/unarchive", null);
        Assert.Equal(HttpStatusCode.OK, ownerUnarchive.StatusCode);
        var ownerDetail = await ownerUnarchive.Content.ReadFromJsonAsync<MessageDetailResponse>();
        Assert.Equal("authorization-sensitive-body", ownerDetail!.TextBody);
        Assert.Contains(ownerDetail.Deliveries, delivery => delivery.EmailAddress == "authorization-sensitive@example.test");
    }

    private static object ValidPayload(string subject) => new
    {
        subject,
        textBody = "authorization matrix",
        to = new[] { new { email = "authorization@example.test" } },
    };

    private sealed record CreateResponse(Guid MessageId, string Status, string IdempotencyKey);
    private sealed record MessageDetailResponse(Guid Id, Guid MailboxId, string Subject, string? TextBody, string? HtmlBody,
        DateTimeOffset CreatedAt, string? Source, DateTimeOffset? ArchivedAt, IReadOnlyList<DeliveryResponse> Deliveries,
        IReadOnlyList<object> ExternalLinks, object Mailbox);
    private sealed record DeliveryResponse(Guid Id, string EmailAddress, string RecipientType, string Status, int Attempts,
        string? LastError, DateTimeOffset? AcceptedAt);
}