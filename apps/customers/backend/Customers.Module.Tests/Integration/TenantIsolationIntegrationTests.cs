using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;

using Vantigo.Customers.Database.Customers;
using Vantigo.Customers.Domain.Contacts;
using Vantigo.Customers.Domain.Customers;
using Vantigo.Tenancy;
using Vantigo.Tenancy.Abstractions;
using Vantigo.Tenancy.EntityFramework;

namespace Vantigo.Customers.Module.Tests.Integration;

[Collection(CustomersApiCollection.Name)]
public sealed class TenantIsolationIntegrationTests
{
    private readonly CustomersApiFactory _factory;

    public TenantIsolationIntegrationTests(CustomersApiFactory factory) => _factory = factory;

    [Fact]
    public async Task AddedRowsAreStampedWithTheActiveTenant()
    {
        var tenant = NewTenant();
        await using var scope = _factory.Services.CreateAsyncScope();
        using var tenantScope = AmbientTenantContext.Enter(tenant);
        var db = scope.ServiceProvider.GetRequiredService<CustomersDbContext>();
        var counter = scope.ServiceProvider.GetRequiredService<ITenantCounterService>();

        var customer = new Customer
        {
            Name = "Stamped customer",
            CustomerNumber = await counter.NextAsync(db, "customer-number"),
        };
        var contact = new Contact { FirstName = "Stamped", LastName = "Contact" };
        db.Customers.Add(customer);
        db.Contacts.Add(contact);
        await db.SaveChangesAsync();

        Assert.Equal(tenant.Value, customer.TenantId);
        Assert.Equal(tenant.Value, contact.TenantId);
        Assert.Equal(tenant.Value, await db.Customers.AsNoTracking()
            .Where(item => item.Id == customer.Id)
            .Select(item => item.TenantId)
            .SingleAsync());
    }

    [Fact]
    public async Task TenantQueriesDoNotSeeRowsFromAnotherTenant()
    {
        var firstTenant = NewTenant();
        var secondTenant = NewTenant();
        await CreateCustomerAsync(firstTenant, "First tenant");
        var secondCustomerId = (await CreateCustomerAsync(secondTenant, "Second tenant")).Id;

        await using var scope = _factory.Services.CreateAsyncScope();
        using var firstScope = AmbientTenantContext.Enter(firstTenant);
        var db = scope.ServiceProvider.GetRequiredService<CustomersDbContext>();

        var visibleIds = await db.Customers.AsNoTracking()
            .Select(item => item.Id)
            .ToListAsync();
        Assert.DoesNotContain(secondCustomerId, visibleIds);
    }

    [Fact]
    public async Task CustomerNumbersIncrementPerTenantIndependently()
    {
        var firstTenant = NewTenant();
        var secondTenant = NewTenant();
        var first = await CreateCustomerAsync(firstTenant, "First one");
        var second = await CreateCustomerAsync(firstTenant, "First two");
        var otherTenant = await CreateCustomerAsync(secondTenant, "Other one");

        Assert.Equal(1, first.CustomerNumber);
        Assert.Equal(2, second.CustomerNumber);
        Assert.Equal(1, otherTenant.CustomerNumber);
        Assert.NotEqual(first.CustomerNumber, second.CustomerNumber);
    }

    private async Task<Customer> CreateCustomerAsync(TenantId tenant, string name)
    {
        await using var scope = _factory.Services.CreateAsyncScope();
        using var tenantScope = AmbientTenantContext.Enter(tenant);
        var db = scope.ServiceProvider.GetRequiredService<CustomersDbContext>();
        var customer = new Customer
        {
            Name = name,
            CustomerNumber = await scope.ServiceProvider.GetRequiredService<ITenantCounterService>()
                .NextAsync(db, "customer-number"),
        };
        db.Customers.Add(customer);
        await db.SaveChangesAsync();
        return customer;
    }

    private static TenantId NewTenant() => new(Guid.NewGuid());
}