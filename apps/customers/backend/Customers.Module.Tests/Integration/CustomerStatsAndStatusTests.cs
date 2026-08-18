using System.Net;
using System.Net.Http.Json;

namespace Vantigo.Customers.Module.Tests.Integration;

[Collection(CustomersApiCollection.Name)]
public sealed class CustomerStatsAndStatusTests(CustomersApiFactory factory)
{
    [Fact]
    public async Task Stats_ReturnsGlobalCounts_WithIdentityFiguresForPermittedCaller()
    {
        using var client = factory.CreateAuthenticatedClient();

        await client.PostAsJsonAsync("/api/v1/customers", new
        {
            name = "Stats Business",
            identity = new { country = "no", type = "business", id = "913456789", name = "Stats AS", source = "manual" },
        });
        await client.PostAsJsonAsync("/api/v1/customers", new
        {
            name = "Stats Person",
            identity = new { country = "se", type = "person", id = "19770101-1234", name = "Stats Person", source = "manual" },
        });
        await client.PostAsJsonAsync("/api/v1/customers", new { name = "Stats Unknown" });

        var stats = await client.GetFromJsonAsync<Stats>("/api/v1/customers/stats");

        // The shared database may contain customers from other tests, so the counts are
        // asserted as lower bounds and for internal consistency rather than exact values.
        Assert.True(stats.TotalCount >= 3);
        Assert.True(stats.ActiveCount >= 3);
        Assert.True(stats.NewLast30DaysCount >= 3);
        Assert.NotNull(stats.BusinessCount);
        Assert.NotNull(stats.PersonCount);
        Assert.NotNull(stats.MissingIdentityCount);
        Assert.NotNull(stats.DistinctCountryCount);
        Assert.True(stats.BusinessCount >= 1);
        Assert.True(stats.PersonCount >= 1);
        Assert.True(stats.MissingIdentityCount >= 1);
        Assert.True(stats.DistinctCountryCount >= 2);
        Assert.Equal(stats.TotalCount, stats.BusinessCount + stats.PersonCount + stats.MissingIdentityCount);
    }

    [Fact]
    public async Task Stats_OmitsIdentityFigures_WithoutLegalIdentityViewPermission()
    {
        using var owner = factory.CreateAuthenticatedClient();
        await owner.PostAsJsonAsync("/api/v1/customers", new { name = "Stats Viewer Customer" });

        using var viewer = await factory.CreateUserClientAsync("StatsViewer", ["customers:view"]);
        var stats = await viewer.GetFromJsonAsync<Stats>("/api/v1/customers/stats");

        Assert.True(stats.TotalCount >= 1);
        Assert.Null(stats.BusinessCount);
        Assert.Null(stats.PersonCount);
        Assert.Null(stats.MissingIdentityCount);
        Assert.Null(stats.DistinctCountryCount);
    }

    [Fact]
    public async Task Customer_DefaultsToActive_AndCarriesTimestamps()
    {
        using var client = factory.CreateAuthenticatedClient();
        var created = await client.PostAsJsonAsync("/api/v1/customers", new { name = "Status Default" });
        var id = (await created.Content.ReadFromJsonAsync<CreatedCustomer>())!.Id;

        var customer = await client.GetFromJsonAsync<Customer>($"/api/v1/customers/{id}");
        Assert.Equal("active", customer.Status);
        Assert.NotEqual(default, customer.CreatedAt);
        Assert.Equal(customer.CreatedAt, customer.UpdatedAt);
    }

    [Fact]
    public async Task UpdatingStatus_PersistsAndBumpsUpdatedAt_AndRecordsTimelineEvent()
    {
        using var client = factory.CreateAuthenticatedClient();
        var created = await client.PostAsJsonAsync("/api/v1/customers", new { name = "Status Flip" });
        var id = (await created.Content.ReadFromJsonAsync<CreatedCustomer>())!.Id;
        var before = await client.GetFromJsonAsync<Customer>($"/api/v1/customers/{id}");

        var update = await client.PutAsJsonAsync($"/api/v1/customers/{id}", new
        {
            name = "Status Flip",
            status = "disabled",
        });
        Assert.Equal(HttpStatusCode.OK, update.StatusCode);

        var after = await client.GetFromJsonAsync<Customer>($"/api/v1/customers/{id}");
        Assert.Equal("disabled", after.Status);
        Assert.True(after.UpdatedAt > before.UpdatedAt);
        Assert.Equal(before.CreatedAt, after.CreatedAt);

        var timeline = await client.GetAsync($"/api/v1/customers/{id}/timeline");
        var timelineJson = await timeline.Content.ReadAsStringAsync();
        Assert.Contains("customer.status_changed", timelineJson);
    }

    [Fact]
    public async Task UpdatingWithInvalidStatus_ReturnsValidationProblem()
    {
        using var client = factory.CreateAuthenticatedClient();
        var created = await client.PostAsJsonAsync("/api/v1/customers", new { name = "Status Invalid" });
        var id = (await created.Content.ReadFromJsonAsync<CreatedCustomer>())!.Id;

        var update = await client.PutAsJsonAsync($"/api/v1/customers/{id}", new
        {
            name = "Status Invalid",
            status = "archived",
        });

        Assert.Equal(HttpStatusCode.BadRequest, update.StatusCode);
        var problem = await update.Content.ReadFromJsonAsync<ValidationProblem>();
        Assert.Contains("status", problem!.Errors.Keys);
    }

    private sealed record CreatedCustomer(int Id, long CustomerNumber);

    private sealed record ValidationProblem(Dictionary<string, string[]> Errors);

    private readonly record struct Customer(
        int Id,
        string Name,
        string Status,
        DateTimeOffset CreatedAt,
        DateTimeOffset UpdatedAt);

    private readonly record struct Stats(
        int TotalCount,
        int ActiveCount,
        int NewLast30DaysCount,
        int? BusinessCount,
        int? PersonCount,
        int? MissingIdentityCount,
        int? DistinctCountryCount);
}