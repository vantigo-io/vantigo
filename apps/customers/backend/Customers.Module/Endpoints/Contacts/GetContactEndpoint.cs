using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Customers.Database.Customers;
using Vantigo.Customers.Endpoints.Contacts.Dtos;

namespace Vantigo.Customers.Endpoints.Contacts;

/// <summary>
/// Retrieves a single contact by its id.
/// </summary>
internal static class GetContactEndpoint
{
    internal static async Task<Results<Ok<ContactResponse>, NotFound>> Handler(
        int id,
        CustomersDbContext dbContext,
        CancellationToken cancellationToken)
    {
        var contact = await dbContext.Contacts
            .AsNoTracking()
            .FirstOrDefaultAsync(c => c.Id == id, cancellationToken);

        if (contact is null)
        {
            return TypedResults.NotFound();
        }

        return TypedResults.Ok(ContactResponse.FromDomain(contact));
    }
}