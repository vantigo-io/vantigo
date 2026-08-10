using System.Net;
using System.Net.Http.Headers;
using System.Net.Http.Json;

namespace Vantigo.Communications.Module.Tests.Integration;

[Collection(CommunicationsModuleCollection.Name)]
public sealed class CommunicationsEndpointsTests(CommunicationsModuleFactory factory)
{
    [Fact]
    public async Task Create_requires_service_key_or_business_session()
    {
        using var client = factory.CreateClient();
        var request = new HttpRequestMessage(HttpMethod.Post, "/api/v1/communications/messages")
        {
            Content = JsonContent.Create(ValidPayload("key-auth")),
        };

        var response = await client.SendAsync(request);
        Assert.Equal(HttpStatusCode.Unauthorized, response.StatusCode);

        request = new HttpRequestMessage(HttpMethod.Post, "/api/v1/communications/messages");
        request.Headers.Add("Idempotency-Key", "key-auth-wrong");
        request.Content = JsonContent.Create(ValidPayload("key-auth"));
        response = await client.SendAsync(request);
        Assert.Equal(HttpStatusCode.Unauthorized, response.StatusCode);
    }

    [Fact]
    public async Task Service_key_create_supports_idempotency_and_persists_external_link()
    {
        using var client = await factory.CreateAuthenticatedClientAsync();
        client.DefaultRequestHeaders.Add("Idempotency-Key", "idempotent-message");
        var payload = ValidPayload("idempotency");

        var first = await client.PostAsJsonAsync("/api/v1/communications/messages", payload);
        Assert.Equal(HttpStatusCode.Created, first.StatusCode);
        var firstBody = await first.Content.ReadFromJsonAsync<CreateResponse>();

        var replay = await client.PostAsJsonAsync("/api/v1/communications/messages", payload);
        Assert.Equal(HttpStatusCode.OK, replay.StatusCode);
        var replayBody = await replay.Content.ReadFromJsonAsync<CreateResponse>();
        Assert.Equal(firstBody!.MessageId, replayBody!.MessageId);

        var changed = await client.PostAsJsonAsync("/api/v1/communications/messages", ValidPayload("different-payload"));
        Assert.Equal(HttpStatusCode.Conflict, changed.StatusCode);

        using var authenticated = await factory.CreateAuthenticatedClientAsync();
        var detail = await authenticated.GetFromJsonAsync<MessageDetail>("/api/v1/communications/messages/" + firstBody.MessageId);
        Assert.Equal("source-123", detail!.ExternalLinks.Single().ExternalEntityId);
        Assert.Equal("00042", detail.ExternalLinks.Single().SourceInstance);
        Assert.Equal(2, detail.Deliveries.Count);

        var events = await authenticated.GetFromJsonAsync<PaginatedEvents>("/api/v1/communications/messages/" + firstBody.MessageId + "/events");
        Assert.Contains(events!.Data, item => item.EventType == "message_queued");
    }

    [Fact]
    public async Task Suppression_is_checked_before_queueing()
    {
        using var owner = await factory.CreateAuthenticatedClientAsync();
        var suppressionResponse = await owner.PostAsJsonAsync("/api/v1/communications/suppressions", new
        {
            emailAddress = "suppressed@example.test",
            reason = "requested",
        });
        Assert.Equal(HttpStatusCode.Created, suppressionResponse.StatusCode);

        using var service = await factory.CreateAuthenticatedClientAsync();
        service.DefaultRequestHeaders.Add("Idempotency-Key", "suppressed-message");
        var response = await service.PostAsJsonAsync("/api/v1/communications/messages", ValidPayload("suppressed", "suppressed@example.test"));
        Assert.Equal((HttpStatusCode)422, response.StatusCode);
    }

    [Fact]
    public async Task Concurrent_identical_requests_reconcile_to_one_message()
    {
        var requests = Enumerable.Range(0, 8).Select(async index =>
        {
            using var client = await factory.CreateAuthenticatedClientAsync();
            client.DefaultRequestHeaders.Add("Idempotency-Key", "concurrent-idempotency");
            return await client.PostAsJsonAsync("/api/v1/communications/messages", ValidPayload("concurrent"));
        });

        var responses = await Task.WhenAll(requests);
        Assert.All(responses, response => Assert.True(response.StatusCode is HttpStatusCode.Created or HttpStatusCode.OK or HttpStatusCode.Conflict, response.StatusCode.ToString()));
        var successful = responses.Where(response => response.IsSuccessStatusCode).ToArray();
        Assert.NotEmpty(successful);
        var ids = (await Task.WhenAll(successful.Select(response => response.Content.ReadFromJsonAsync<CreateResponse>())))
            .Select(response => response!.MessageId).Distinct().ToArray();
        Assert.Single(ids);
    }

    [Fact]
    public async Task Owner_can_update_mailbox_display_name_and_active_state()
    {
        using var owner = await factory.CreateAuthenticatedClientAsync();
        var mailboxes = await owner.GetFromJsonAsync<IReadOnlyList<Mailbox>>("/api/v1/communications/mailboxes");
        var mailbox = mailboxes!.Single();

        var missing = await owner.PutAsJsonAsync("/api/v1/communications/mailboxes/" + Guid.NewGuid(), new { displayName = "Nope" });
        Assert.Equal(HttpStatusCode.NotFound, missing.StatusCode);

        var invalid = await owner.PutAsJsonAsync("/api/v1/communications/mailboxes/" + mailbox.Id, new { displayName = new string('x', 201) });
        Assert.Equal(HttpStatusCode.BadRequest, invalid.StatusCode);

        var updated = await owner.PutAsJsonAsync("/api/v1/communications/mailboxes/" + mailbox.Id, new { displayName = "Renamed Mailbox", isActive = false });
        Assert.Equal(HttpStatusCode.OK, updated.StatusCode);
        var body = await updated.Content.ReadFromJsonAsync<Mailbox>();
        Assert.Equal("Renamed Mailbox", body!.DisplayName);
        Assert.False(body.IsActive);
        Assert.Equal(mailbox.FromAddress, body.FromAddress);

        // Restore original state for other tests sharing the fixture.
        var restored = await owner.PutAsJsonAsync("/api/v1/communications/mailboxes/" + mailbox.Id, new { displayName = mailbox.DisplayName, isActive = true });
        Assert.Equal(HttpStatusCode.OK, restored.StatusCode);

        using var anonymous = factory.CreateClient();
        var unauthorized = await anonymous.PutAsJsonAsync("/api/v1/communications/mailboxes/" + mailbox.Id, new { displayName = "Anon" });
        Assert.True(unauthorized.StatusCode is HttpStatusCode.Unauthorized or HttpStatusCode.Redirect or HttpStatusCode.Found, unauthorized.StatusCode.ToString());
    }

    private static object ValidPayload(string subject, string? firstRecipient = null) => new
    {
        subject,
        textBody = "hello from integration test",
        to = new[] { new { email = firstRecipient ?? "person@example.test" } },
        cc = new[] { new { email = "copy@example.test" } },
        externalLinks = new[]
        {
            new
            {
                sourceSystem = "customers",
                sourceInstance = "00042",
                entityType = "account",
                externalEntityId = "source-123",
                displayLabel = "Example Account",
            },
        },
    };

    private sealed record CreateResponse(Guid MessageId, string Status, string IdempotencyKey);
    private sealed record Mailbox(Guid Id, string FromAddress, string? DisplayName, DateTimeOffset CreatedAt, bool IsActive);
    private sealed record MessageDetail(Guid Id, string Subject, string? TextBody, string? HtmlBody, DateTimeOffset CreatedAt, string? Source, IReadOnlyList<Delivery> Deliveries, IReadOnlyList<ExternalLink> ExternalLinks);
    private sealed record Delivery(Guid Id, string EmailAddress, string RecipientType, string Status, int Attempts, string? LastError, DateTimeOffset? AcceptedAt);
    private sealed record ExternalLink(Guid Id, string SourceSystem, string SourceInstance, string EntityType, string ExternalEntityId, string? DisplayLabel);
    private sealed record PaginatedEvents(IReadOnlyList<MessageEvent> Data, object Pagination);
    private sealed record MessageEvent(Guid Id, Guid? DeliveryId, string EventType, DateTimeOffset OccurredAt, string? DataJson);
}