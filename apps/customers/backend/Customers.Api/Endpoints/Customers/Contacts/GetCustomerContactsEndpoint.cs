using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Customers.Api.Database;
using Vantigo.Customers.Api.Endpoints.Customers.Contacts.Dtos;

namespace Vantigo.Customers.Api.Endpoints.Customers.Contacts;

/// <summary>
/// Lists the contacts associated with a customer, sorted by the contact's name,
/// together with their role and connection-specific contact details.
/// </summary>
internal static class GetCustomerContactsEndpoint
{
    internal static async Task<Results<Ok<Response>, NotFound>> Handler(
        int id,
        AppDbContext dbContext,
        CancellationToken cancellationToken)
    {
        var customerExists = await dbContext.Customers
            .AsNoTracking()
            .AnyAsync(c => c.Id == id, cancellationToken);

        if (!customerExists)
        {
            return TypedResults.NotFound();
        }

        var associations = await dbContext.CustomersContacts
            .AsNoTracking()
            .Where(cc => cc.CustomerId == id)
            .Include(cc => cc.Contact)
            .OrderBy(cc => cc.Contact.FirstName)
            .ThenBy(cc => cc.Contact.LastName)
            .ThenBy(cc => cc.ContactId)
            .ToListAsync(cancellationToken);

        return TypedResults.Ok(new Response
        {
            Data = associations.Select(CustomerContactResponse.FromDomain).ToList(),
        });
    }

    internal readonly record struct Response
    {
        public required List<CustomerContactResponse> Data { get; init; }
    }
}