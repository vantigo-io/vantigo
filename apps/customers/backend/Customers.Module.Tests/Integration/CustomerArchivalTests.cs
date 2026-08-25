using System.Net;
using System.Net.Http.Json;
using System.Text.Json;

using Microsoft.Extensions.DependencyInjection;

using Vantigo.Contracts;
using Vantigo.Customers.Database.Customers;
using Vantigo.Customers.Domain.Customers;
using Vantigo.Tenancy;
using Vantigo.Tenancy.Abstractions;
using Vantigo.Tenancy.EntityFramework;

namespace Vantigo.Customers.Module.Tests.Integration;

/// <summary>
/// Customers are archived, never hard-deleted: Communications and Energy keep
/// historical references to customer ids, so the row must stay resolvable for
/// those views while disappearing from day-to-day listings.
/// </summary>
[Collection(CustomersApiCollection.Name)]
public sealed class CustomerArchivalTests
{
    private readonly CustomersApiFactory _factory;
    private readonly HttpClient _client;

    public CustomerArchivalTests(CustomersApiFactory factory)
    {
        _factory = factory;
        _client = factory.CreateAuthenticatedClient();
    }

    [Fact]
    public async Task Delete_archives_the_customer_and_keeps_it_resolvable_by_id()
    {
        var id = await CreateCustomerAsync("Archive candidate");

        var delete = await _client.DeleteAsync($"/api/v1/customers/{id}");
        Assert.Equal(HttpStatusCode.NoContent, delete.StatusCode);

        var customer = await _client.GetFromJsonAsync<JsonElement>($"/api/v1/customers/{id}");
        Assert.Equal("archived", customer.GetProperty("status").GetString());

        // Archiving is idempotent.
        var again = await _client.DeleteAsync($"/api/v1/customers/{id}");
        Assert.Equal(HttpStatusCode.NoContent, again.StatusCode);
    }

    [Fact]
    public async Task Archived_customers_are_hidden_from_the_default_listing_but_included_on_request()
    {
        var name = $"Archived listing probe {Guid.NewGuid():N}";
        var id = await CreateCustomerAsync(name);
        Assert.Equal(HttpStatusCode.NoContent, (await _client.DeleteAsync($"/api/v1/customers/{id}")).StatusCode);

        var defaultList = await _client.GetFromJsonAsync<JsonElement>($"/api/v1/customers?search={Uri.EscapeDataString(name)}");
        Assert.Equal(0, defaultList.GetProperty("data").GetArrayLength());

        var withArchived = await _client.GetFromJsonAsync<JsonElement>(
            $"/api/v1/customers?search={Uri.EscapeDataString(name)}&includeArchived=true");
        Assert.Equal(1, withArchived.GetProperty("data").GetArrayLength());
        Assert.Equal("archived", withArchived.GetProperty("data")[0].GetProperty("status").GetString());
    }

    [Fact]
    public async Task An_archived_customer_can_be_reactivated_through_update()
    {
        var id = await CreateCustomerAsync("Unarchive candidate");
        Assert.Equal(HttpStatusCode.NoContent, (await _client.DeleteAsync($"/api/v1/customers/{id}")).StatusCode);

        var update = await _client.PutAsJsonAsync($"/api/v1/customers/{id}",
            new { name = "Unarchive candidate", status = "active" });
        Assert.Equal(HttpStatusCode.OK, update.StatusCode);

        var customer = await _client.GetFromJsonAsync<JsonElement>($"/api/v1/customers/{id}");
        Assert.Equal("active", customer.GetProperty("status").GetString());
    }

    [Fact]
    public async Task The_customer_directory_resolves_archived_customers_flagged_as_archived()
    {
        var tenant = new TenantId(Guid.NewGuid());
        int id;
        await using (var scope = _factory.Services.CreateAsyncScope())
        using (AmbientTenantContext.Enter(tenant))
        {
            var db = scope.ServiceProvider.GetRequiredService<CustomersDbContext>();
            var customer = new Customer
            {
                Name = "Directory archived",
                Status = Vantigo.Customers.Domain.Customers.Common.CustomerStatus.Archived,
                CustomerNumber = await scope.ServiceProvider.GetRequiredService<ITenantCounterService>()
                    .NextAsync(db, "customer-number"),
            };
            db.Customers.Add(customer);
            await db.SaveChangesAsync();
            id = customer.Id;
        }

        await using (var scope = _factory.Services.CreateAsyncScope())
        using (AmbientTenantContext.Enter(tenant))
        {
            var directory = scope.ServiceProvider.GetRequiredService<ICustomerDirectory>();
            var entry = await directory.FindCustomerAsync(id);

            Assert.NotNull(entry);
            Assert.Equal("Directory archived", entry.Name);
            Assert.True(entry.Archived);
        }
    }

    private async Task<int> CreateCustomerAsync(string name)
    {
        var response = await _client.PostAsJsonAsync("/api/v1/customers", new { name });
        Assert.Equal(HttpStatusCode.Created, response.StatusCode);
        var created = await response.Content.ReadFromJsonAsync<JsonElement>();
        return created.GetProperty("id").GetInt32();
    }
}