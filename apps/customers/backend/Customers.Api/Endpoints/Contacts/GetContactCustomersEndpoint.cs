using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Customers.Api.Database;

namespace Vantigo.Customers.Api.Endpoints.Contacts;

/// <summary>
/// Lists the customers a contact is associated with, sorted by customer name,
/// together with the contact's role and connection-specific contact details for
/// each association.
/// </summary>
internal static class GetContactCustomersEndpoint
{
    internal static async Task<Results<Ok<Response>, NotFound>> Handler(
        int id,
        AppDbContext dbContext,
        CancellationToken cancellationToken)
    {
        var contactExists = await dbContext.Contacts
            .AsNoTracking()
            .AnyAsync(c => c.Id == id, cancellationToken);

        if (!contactExists)
        {
            return TypedResults.NotFound();
        }

        var associations = await dbContext.CustomersContacts
            .AsNoTracking()
            .Where(cc => cc.ContactId == id)
            .OrderBy(cc => cc.Customer.Name)
            .ThenBy(cc => cc.CustomerId)
            .Select(cc => new ContactCustomerResponse
            {
                Customer = new CustomerReference
                {
                    Id = cc.CustomerId,
                    Name = cc.Customer.Name,
                },
                Role = cc.Role,
                Phone = cc.Phone != null ? (string)cc.Phone.Value : null,
                Email = cc.Email != null ? (string)cc.Email.Value : null,
            })
            .ToListAsync(cancellationToken);

        return TypedResults.Ok(new Response { Data = associations });
    }

    internal readonly record struct Response
    {
        public required List<ContactCustomerResponse> Data { get; init; }
    }

    internal readonly record struct ContactCustomerResponse
    {
        public required CustomerReference Customer { get; init; }
        public required string Role { get; init; }
        public string? Phone { get; init; }
        public string? Email { get; init; }
    }

    internal readonly record struct CustomerReference
    {
        public required int Id { get; init; }
        public required string Name { get; init; }
    }
}
