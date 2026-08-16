using System.Text.Json;

using Microsoft.Extensions.DependencyInjection;

using Vantigo.Contracts;
using Vantigo.Customers.Database.Customers;
using Vantigo.Customers.Domain.Contacts;
using Vantigo.Customers.Domain.Contacts.Common;
using Vantigo.Customers.Domain.Customers;
using Vantigo.Tenancy;
using Vantigo.Tenancy.Abstractions;
using Vantigo.Tenancy.EntityFramework;

namespace Vantigo.Customers.Module.Tests.Integration;

[Collection(CustomersApiCollection.Name)]
public sealed class CustomerDirectoryEmailResolutionTests
{
    private static readonly TenantId Tenant = new(new Guid("10000000-0000-0000-0000-000000000001"));
    private readonly CustomersApiFactory _factory;

    public CustomerDirectoryEmailResolutionTests(CustomersApiFactory factory) => _factory = factory;

    [Fact]
    public async Task FindByEmailAsync_MatchesCanonicalContactEmailAndReturnsAllCustomers()
    {
        var email = UniqueEmail();
        var contactId = await CreateContactAsync(email);
        var firstCustomerId = await CreateCustomerWithContactAsync(contactId);
        var secondCustomerId = await CreateCustomerWithContactAsync(contactId);

        var result = await FindAsync($"  {email.ToUpperInvariant()} ");

        var match = Assert.Single(result!.Matches);
        Assert.Equal(contactId, match.ContactId);
        Assert.Equal(new[] { firstCustomerId, secondCustomerId }.Order(), match.CandidateCustomerIds);
        Assert.False(result.IsAmbiguous);
    }

    [Fact]
    public async Task FindByEmailAsync_MatchesCustomerSpecificEmailWithoutExposingEmail()
    {
        var relationshipEmail = UniqueEmail();
        var contactId = await CreateContactAsync(null);
        var customerId = await CreateCustomerWithContactAsync(contactId, relationshipEmail);
        var unrelatedCustomerId = await CreateCustomerWithContactAsync(contactId);

        var result = await FindAsync($" {relationshipEmail.ToUpperInvariant()} ");

        var match = Assert.Single(result!.Matches);
        Assert.Equal(contactId, match.ContactId);
        Assert.Equal(new[] { customerId }, match.CandidateCustomerIds);
        Assert.DoesNotContain(unrelatedCustomerId, match.CandidateCustomerIds);
        Assert.DoesNotContain(relationshipEmail, JsonSerializer.Serialize(result));
    }

    [Fact]
    public async Task FindByEmailAsync_ReportsAmbiguityWhenDifferentContactsMatch()
    {
        var email = UniqueEmail();
        var firstContactId = await CreateContactAsync(email);
        var secondContactId = await CreateContactAsync(email);
        var firstCustomerId = await CreateCustomerWithContactAsync(firstContactId);
        var secondCustomerId = await CreateCustomerWithContactAsync(secondContactId);

        var result = await FindAsync(email);

        Assert.NotNull(result);
        Assert.True(result.IsAmbiguous);
        Assert.Equal(
            new[] { firstContactId, secondContactId },
            result.Matches.Select(match => match.ContactId));
        Assert.Equal(new[] { firstCustomerId }, result.Matches[0].CandidateCustomerIds);
        Assert.Equal(new[] { secondCustomerId }, result.Matches[1].CandidateCustomerIds);
    }

    [Fact]
    public async Task FindByEmailAsync_ReturnsNullForUnknownEmail()
    {
        var result = await FindAsync(UniqueEmail());

        Assert.Null(result);
    }

    [Fact]
    public async Task FindByEmailAsync_HonorsCancellation()
    {
        using var cancellation = new CancellationTokenSource();
        cancellation.Cancel();

        await Assert.ThrowsAnyAsync<OperationCanceledException>(() => FindAsync(UniqueEmail(), cancellation.Token));
    }

    private async Task<ContactEmailResolution?> FindAsync(
        string email,
        CancellationToken cancellationToken = default)
    {
        await using var scope = _factory.Services.CreateAsyncScope();
        using var tenantScope = AmbientTenantContext.Enter(Tenant);
        var directory = scope.ServiceProvider.GetRequiredService<ICustomerDirectory>();
        return await directory.FindByEmailAsync(email, cancellationToken);
    }

    private async Task<int> CreateContactAsync(string? email)
    {
        await using var scope = _factory.Services.CreateAsyncScope();
        using var tenantScope = AmbientTenantContext.Enter(Tenant);
        var db = scope.ServiceProvider.GetRequiredService<CustomersDbContext>();
        var contact = new Contact
        {
            FirstName = "Directory",
            LastName = "Lookup",
            Email = email is null ? (EmailAddress?)null : new EmailAddress(email),
        };

        db.Contacts.Add(contact);
        await db.SaveChangesAsync();
        return contact.Id;
    }

    private async Task<int> CreateCustomerWithContactAsync(int contactId, string? relationshipEmail = null)
    {
        await using var scope = _factory.Services.CreateAsyncScope();
        using var tenantScope = AmbientTenantContext.Enter(Tenant);
        var db = scope.ServiceProvider.GetRequiredService<CustomersDbContext>();
        var customer = new Customer
        {
            Name = $"Directory Lookup {Guid.NewGuid():N}",
            CustomerNumber = await scope.ServiceProvider.GetRequiredService<ITenantCounterService>()
                .NextAsync(db, "customer-number"),
        };
        var association = new CustomerContact
        {
            ContactId = contactId,
            Role = "Contact",
            Email = relationshipEmail is null ? (EmailAddress?)null : new EmailAddress(relationshipEmail),
            Customer = customer,
        };

        db.Customers.Add(customer);
        db.CustomersContacts.Add(association);
        await db.SaveChangesAsync();
        return customer.Id;
    }

    private static string UniqueEmail() => $"directory-{Guid.NewGuid():N}@example.test";
}