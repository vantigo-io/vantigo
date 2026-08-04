using System.Net;
using System.Net.Http.Json;

namespace Vantigo.Customers.Api.Tests.Integration;

[Collection(CustomersApiCollection.Name)]
public sealed class TimelineEndpointsTests
{
    private readonly HttpClient _client;

    public TimelineEndpointsTests(CustomersApiFactory factory)
    {
        _client = factory.CreateAuthenticatedClient();
    }

    [Fact]
    public async Task ManualTimelineEntry_CanBeEditedAndSoftDeletedWithHistory()
    {
        var customerId = await CreateCustomer();
        var create = await _client.PostAsJsonAsync($"/api/v1/customers/{customerId}/timeline", new
        {
            eventType = "note",
            occurredOn = "2026-07-27",
            note = "First note",
            sourceUrl = "https://example.test/source",
        });

        Assert.Equal(HttpStatusCode.Created, create.StatusCode);
        var created = await create.Content.ReadFromJsonAsync<TimelineEntry>();
        Assert.Equal("unattributed", created.ActorKind);
        Assert.Equal(1, created.CurrentRevision);
        Assert.NotEqual(default, created.CreatedAt);
        Assert.NotEqual(default, created.UpdatedAt);

        var update = await _client.PutAsJsonAsync($"/api/v1/customers/{customerId}/timeline/{created.Id}", new
        {
            eventType = "interaction.call",
            occurredOn = "2026-07-27",
            occurredAt = "2026-07-27T12:00:00+02:00",
            note = "Updated note",
            expectedRevision = 1,
        });
        Assert.Equal(HttpStatusCode.OK, update.StatusCode);
        var updated = await update.Content.ReadFromJsonAsync<TimelineEntry>();
        Assert.Equal(2, updated.CurrentRevision);
        Assert.Equal(new DateTimeOffset(2026, 7, 27, 10, 0, 0, TimeSpan.Zero), updated.OccurredAt);

        var detail = await _client.GetFromJsonAsync<TimelineEntry>(
            $"/api/v1/customers/{customerId}/timeline/{created.Id}");
        Assert.Equal(updated, detail);

        var stale = await _client.PutAsJsonAsync($"/api/v1/customers/{customerId}/timeline/{created.Id}", new
        {
            eventType = "note",
            occurredOn = "2026-07-27",
            note = "stale",
            expectedRevision = 1,
        });
        Assert.Equal(HttpStatusCode.Conflict, stale.StatusCode);
        Assert.Equal("application/problem+json", stale.Content.Headers.ContentType?.MediaType);
        var staleProblem = await stale.Content.ReadFromJsonAsync<ProblemDetailsResponse>();
        Assert.Equal("Timeline revision conflict", staleProblem.Title);
        Assert.Contains("revision", staleProblem.Detail, StringComparison.OrdinalIgnoreCase);

        var deleted = await _client.DeleteAsync($"/api/v1/customers/{customerId}/timeline/{created.Id}?expectedRevision=2");
        Assert.Equal(HttpStatusCode.NoContent, deleted.StatusCode);

        var feed = await _client.GetFromJsonAsync<TimelineList>($"/api/v1/customers/{customerId}/timeline");
        Assert.DoesNotContain(feed.Data, entry => entry.Id == created.Id);

        var revisions = await _client.GetFromJsonAsync<RevisionList>(
            $"/api/v1/customers/{customerId}/timeline/{created.Id}/revisions");
        Assert.Equal(new[] { 1, 2, 3 }, revisions.Data.Select(revision => revision.Revision));
        Assert.All(revisions.Data, revision => Assert.Equal("Unattributed", revision.ActorDisplayName));
        Assert.Equal(new[] { "create", "update", "delete" }, revisions.Data.Select(revision => revision.Action));
        Assert.All(revisions.Data, revision => Assert.NotEqual(default, revision.ChangedAt));
        Assert.Equal("deleted", revisions.Data[^1].State);
    }

    [Fact]
    public async Task ManualTimelineEntry_RejectsInvalidAndFutureInput()
    {
        var customerId = await CreateCustomer();
        var response = await _client.PostAsJsonAsync($"/api/v1/customers/{customerId}/timeline", new
        {
            eventType = "not.allowed",
            occurredOn = "2999-01-01",
            note = " ",
            sourceUrl = "ftp://example.test/file",
        });

        Assert.Equal(HttpStatusCode.BadRequest, response.StatusCode);
        var problem = await response.Content.ReadFromJsonAsync<ValidationProblem>();
        Assert.All(new[] { "eventType", "occurredOn", "note", "sourceUrl" }, key => Assert.Contains(key, problem.Errors.Keys));

        var mismatchedInstant = await _client.PostAsJsonAsync($"/api/v1/customers/{customerId}/timeline", new
        {
            eventType = "note",
            occurredOn = "2026-07-27",
            occurredAt = "2026-07-28T00:00:00Z",
            note = "UTC date mismatch",
        });
        Assert.Equal(HttpStatusCode.BadRequest, mismatchedInstant.StatusCode);
        var mismatchProblem = await mismatchedInstant.Content.ReadFromJsonAsync<ValidationProblem>();
        Assert.Contains("occurredAt", mismatchProblem.Errors.Keys);
    }

    [Fact]
    public async Task Timeline_UsesOpaqueCursorAndStableNewestFirstOrder()
    {
        var customerId = await CreateCustomer();
        await CreateManual(customerId, "2026-07-26", "old");
        await CreateManual(customerId, "2026-07-28", "newest");
        await CreateManual(customerId, "2026-07-27", "middle");

        var first = await _client.GetFromJsonAsync<TimelineList>(
            $"/api/v1/customers/{customerId}/timeline?limit=2");
        Assert.Contains(first.Data, entry => entry.Note == "newest");
        Assert.False(string.IsNullOrWhiteSpace(first.NextCursor));
        Assert.DoesNotContain("2026-07-27", first.NextCursor);

        var second = await _client.GetFromJsonAsync<TimelineList>(
            $"/api/v1/customers/{customerId}/timeline?limit=2&cursor={Uri.EscapeDataString(first.NextCursor!)}");
        Assert.Equal(new[] { "middle", "old" }, second.Data.Where(entry => entry.Note is not null).Select(entry => entry.Note));
        Assert.Null(second.NextCursor);

        var malformed = await _client.GetAsync($"/api/v1/customers/{customerId}/timeline?cursor=bad");
        Assert.Equal(HttpStatusCode.BadRequest, malformed.StatusCode);
    }

    [Fact]
    public async Task Timeline_CursorPreservesMixedOccurredAtBoundary()
    {
        var customerId = await CreateCustomer();
        await CreateManual(customerId, "2026-07-27", "date-only");
        await CreateManual(customerId, "2026-07-27", "with-time", "2026-07-27T09:00:00Z");

        var first = await _client.GetFromJsonAsync<TimelineList>($"/api/v1/customers/{customerId}/timeline?eventType=note&limit=1");
        Assert.Contains(first.Data, entry => entry.Note == "with-time");
        var second = await _client.GetFromJsonAsync<TimelineList>(
            $"/api/v1/customers/{customerId}/timeline?eventType=note&limit=1&cursor={Uri.EscapeDataString(first.NextCursor!)}");
        Assert.Contains(second.Data, entry => entry.Note == "date-only");
    }

    [Fact]
    public async Task GeneratedTimelineEvents_CoverCustomerAndContactAssociationLifecycle()
    {
        var customerId = await CreateCustomer();
        var contact = await CreateContact();
        await _client.PutAsJsonAsync($"/api/v1/customers/{customerId}", new { name = "Timeline updated" });
        await _client.PostAsJsonAsync($"/api/v1/customers/{customerId}/contacts", new
        {
            contactId = contact.Id,
            role = "CEO",
        });
        await _client.PutAsJsonAsync($"/api/v1/customers/{customerId}/contacts/{contact.Id}", new { role = "CTO" });
        await _client.DeleteAsync($"/api/v1/customers/{customerId}/contacts/{contact.Id}");

        var timeline = await _client.GetFromJsonAsync<TimelineList>($"/api/v1/customers/{customerId}/timeline?limit=100");
        Assert.Contains(timeline.Data, entry => entry.EventType == "customer.created");
        Assert.Contains(timeline.Data, entry => entry.EventType == "customer.updated");
        Assert.Contains(timeline.Data, entry => entry.EventType == "customer.contact_attached");
        Assert.Contains(timeline.Data, entry => entry.EventType == "customer.contact_relationship_updated");
        Assert.Contains(timeline.Data, entry => entry.EventType == "customer.contact_detached");
        var attached = Assert.Single(timeline.Data, entry => entry.EventType == "customer.contact_attached");
        Assert.Contains($"\"contactId\":{contact.Id}", attached.Payload.GetRawText());
        Assert.Contains("displayName", attached.Payload.GetRawText());
        Assert.Contains("Contact linked", attached.Summary);

        var immutable = await _client.PutAsJsonAsync($"/api/v1/customers/{customerId}/timeline/{attached.Id}", new
        {
            eventType = "note",
            occurredOn = "2026-07-27",
            note = "cannot edit",
            expectedRevision = 1,
        });
        Assert.Equal(HttpStatusCode.Conflict, immutable.StatusCode);
        Assert.Equal("application/problem+json", immutable.Content.Headers.ContentType?.MediaType);
        var immutableProblem = await immutable.Content.ReadFromJsonAsync<ProblemDetailsResponse>();
        Assert.Equal("Timeline entry is immutable", immutableProblem.Title);
    }

    [Fact]
    public async Task CustomerUpdatesDescribeLegalIdentityChanges()
    {
        var customerId = await CreateCustomer();
        await UpdateCustomer(customerId, new { country = "no", type = "business", id = "123456789", name = "Legal AS", source = "manual" });
        await UpdateCustomer(customerId, new { country = "no", type = "business", id = "987654321", name = "New Legal AS", source = "brreg" });
        await _client.PutAsJsonAsync($"/api/v1/customers/{customerId}", new { name = "Timeline final" });

        var feed = await _client.GetFromJsonAsync<TimelineList>($"/api/v1/customers/{customerId}/timeline?eventType=customer.updated&limit=100");
        Assert.Contains(feed.Data, entry => entry.Summary!.Contains("legal identity added"));
        Assert.Contains(feed.Data, entry => entry.Summary!.Contains("legal identity updated"));
        Assert.Contains(feed.Data, entry => entry.Summary!.Contains("legal identity removed"));
        Assert.All(feed.Data, entry => Assert.Contains("legalIdentity", entry.Payload.GetRawText()));
    }

    [Fact]
    public async Task TimelineFiltersApplyBeforeKeysetPagination()
    {
        var customerId = await CreateCustomer();
        await CreateManual(customerId, "2026-07-25", "manual old");
        await CreateManual(customerId, "2026-07-26", "manual middle");
        await CreateManual(customerId, "2026-07-27", "manual new");

        var first = await _client.GetFromJsonAsync<TimelineList>(
            $"/api/v1/customers/{customerId}/timeline?provenance=manual&eventType=note&occurredFrom=2026-07-26&occurredTo=2026-07-27&limit=1");
        Assert.Equal("manual new", first.Data.Single().Note);
        Assert.NotNull(first.NextCursor);

        var second = await _client.GetFromJsonAsync<TimelineList>(
            $"/api/v1/customers/{customerId}/timeline?provenance=manual&eventType=note&occurredFrom=2026-07-26&occurredTo=2026-07-27&limit=1&cursor={Uri.EscapeDataString(first.NextCursor!)}");
        Assert.Equal("manual middle", second.Data.Single().Note);

        var generated = await _client.GetFromJsonAsync<TimelineList>(
            $"/api/v1/customers/{customerId}/timeline?provenance=generated&eventType=customer.created&limit=10");
        Assert.All(generated.Data, entry => Assert.Equal("generated", entry.Provenance));

        var invalid = await _client.GetAsync($"/api/v1/customers/{customerId}/timeline?provenance=bogus&eventType=%20&occurredFrom=2026-07-28&occurredTo=2026-07-27");
        Assert.Equal(HttpStatusCode.BadRequest, invalid.StatusCode);
        var problem = await invalid.Content.ReadFromJsonAsync<ValidationProblem>();
        Assert.Contains("provenance", problem.Errors.Keys);
        Assert.Contains("eventType[0]", problem.Errors.Keys);
        Assert.Contains("occurredFrom", problem.Errors.Keys);
    }

    [Fact]
    public async Task TimelineCursorRejectsOtherCustomerAndDifferentFilter()
    {
        var firstCustomer = await CreateCustomer();
        var secondCustomer = await CreateCustomer();
        await CreateManual(firstCustomer, "2026-07-27", "first");
        await CreateManual(firstCustomer, "2026-07-26", "second");
        await CreateManual(secondCustomer, "2026-07-27", "other customer");

        var page = await _client.GetFromJsonAsync<TimelineList>($"/api/v1/customers/{firstCustomer}/timeline?eventType=note&limit=1");
        var cursor = Uri.EscapeDataString(page.NextCursor!);
        var otherCustomer = await _client.GetAsync($"/api/v1/customers/{secondCustomer}/timeline?eventType=note&limit=1&cursor={cursor}");
        Assert.Equal(HttpStatusCode.BadRequest, otherCustomer.StatusCode);

        var differentFilter = await _client.GetAsync($"/api/v1/customers/{firstCustomer}/timeline?eventType=other&limit=1&cursor={cursor}");
        Assert.Equal(HttpStatusCode.BadRequest, differentFilter.StatusCode);
        var problem = await differentFilter.Content.ReadFromJsonAsync<ValidationProblem>();
        Assert.Contains("cursor", problem.Errors.Keys);
    }

    private async Task UpdateCustomer(int customerId, object identity)
    {
        var response = await _client.PutAsJsonAsync($"/api/v1/customers/{customerId}", new { name = "Timeline", identity });
        Assert.Equal(HttpStatusCode.OK, response.StatusCode);
    }

    [Fact]
    public async Task SemanticallyUnchangedCustomerAndRelationshipUpdatesDoNotCreateEvents()
    {
        var customerId = await CreateCustomer();
        var contact = await CreateContact();
        await Attach(customerId, contact.Id);

        var before = await _client.GetFromJsonAsync<TimelineList>($"/api/v1/customers/{customerId}/timeline?limit=100");
        var customer = await _client.GetFromJsonAsync<CustomerName>($"/api/v1/customers/{customerId}");
        var customerUpdate = await _client.PutAsJsonAsync($"/api/v1/customers/{customerId}", new { name = customer.Name });
        Assert.Equal(HttpStatusCode.OK, customerUpdate.StatusCode);
        var relationshipUpdate = await _client.PutAsJsonAsync($"/api/v1/customers/{customerId}/contacts/{contact.Id}", new { role = "CEO" });
        Assert.Equal(HttpStatusCode.OK, relationshipUpdate.StatusCode);

        var after = await _client.GetFromJsonAsync<TimelineList>($"/api/v1/customers/{customerId}/timeline?limit=100");
        Assert.Equal(before.Data.Count, after.Data.Count);
        Assert.Equal(
            before.Data.Select(entry => entry.EventType).Order(),
            after.Data.Select(entry => entry.EventType).Order());
    }

    [Fact]
    public async Task DeleteContact_EmitsRemovalForEveryAssociatedCustomer()
    {
        var contact = await CreateContact();
        var firstCustomer = await CreateCustomer();
        var secondCustomer = await CreateCustomer();
        await Attach(firstCustomer, contact.Id);
        await Attach(secondCustomer, contact.Id);

        var deleted = await _client.DeleteAsync($"/api/v1/contacts/{contact.Id}");
        Assert.Equal(HttpStatusCode.NoContent, deleted.StatusCode);

        var firstTimeline = await _client.GetFromJsonAsync<TimelineList>($"/api/v1/customers/{firstCustomer}/timeline");
        var secondTimeline = await _client.GetFromJsonAsync<TimelineList>($"/api/v1/customers/{secondCustomer}/timeline");
        var firstRemoved = Assert.Single(firstTimeline.Data, entry => entry.EventType == "customer.contact_removed");
        var secondRemoved = Assert.Single(secondTimeline.Data, entry => entry.EventType == "customer.contact_removed");
        Assert.Contains($"\"contactId\":{contact.Id}", firstRemoved.Payload.GetRawText());
        Assert.Contains($"\"contactId\":{contact.Id}", secondRemoved.Payload.GetRawText());
        Assert.Contains("displayName", firstRemoved.Payload.GetRawText());
        Assert.Contains("Contact removed", firstRemoved.Summary);
    }

    private async Task<int> CreateCustomer()
    {
        var response = await _client.PostAsJsonAsync("/api/v1/customers", new { name = $"Timeline {Guid.NewGuid()}" });
        var created = await response.Content.ReadFromJsonAsync<CreatedCustomer>();
        return created.Id;
    }

    private async Task<Contact> CreateContact()
    {
        var response = await _client.PostAsJsonAsync("/api/v1/contacts", new
        {
            firstName = "Timeline",
            lastName = Guid.NewGuid().ToString("N"),
        });
        return await response.Content.ReadFromJsonAsync<Contact>();
    }

    private async Task<TimelineEntry> CreateManual(int customerId, string occurredOn, string note, string? occurredAt = null)
    {
        var response = await _client.PostAsJsonAsync($"/api/v1/customers/{customerId}/timeline", new
        {
            eventType = "note",
            occurredOn,
            note,
            occurredAt,
        });
        Assert.Equal(HttpStatusCode.Created, response.StatusCode);
        return await response.Content.ReadFromJsonAsync<TimelineEntry>();
    }

    private async Task Attach(int customerId, int contactId)
    {
        await _client.PostAsJsonAsync($"/api/v1/customers/{customerId}/contacts", new { contactId, role = "CEO" });
    }

    private readonly record struct CreatedCustomer(int Id);
    private readonly record struct CustomerName(string Name);
    private readonly record struct Contact(int Id);
    private readonly record struct ValidationProblem(Dictionary<string, string[]> Errors);
    private readonly record struct ProblemDetailsResponse(string? Title, string? Detail);
    private readonly record struct TimelineList(List<TimelineEntry> Data, string? NextCursor);
    private readonly record struct TimelineEntry(
        int Id,
        string EventType,
        string Provenance,
        string? Note,
        string ActorKind,
        int CurrentRevision,
        DateTimeOffset? OccurredAt,
        System.Text.Json.JsonElement Payload,
        string? Summary,
        DateTimeOffset CreatedAt,
        DateTimeOffset UpdatedAt);
    private readonly record struct RevisionList(List<TimelineRevision> Data);
    private readonly record struct TimelineRevision(int Revision, string ActorDisplayName, string State, string Action, DateTimeOffset ChangedAt);
}