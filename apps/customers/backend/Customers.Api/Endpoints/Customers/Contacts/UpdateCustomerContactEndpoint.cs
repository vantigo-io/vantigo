using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Customers.Api.Database;
using Vantigo.Customers.Api.Endpoints.Customers.Contacts.Dtos;

namespace Vantigo.Customers.Api.Endpoints.Customers.Contacts;

/// <summary>
/// Updates the role and connection-specific contact details of an existing
/// customer-contact association.
/// </summary>
internal static class UpdateCustomerContactEndpoint
{
    internal static async Task<Results<Ok<CustomerContactResponse>, NotFound, ValidationProblem>> Handler(
        int id,
        int contactId,
        CustomerContactRequest request,
        AppDbContext dbContext,
        CancellationToken cancellationToken)
    {
        var association = await dbContext.CustomersContacts
            .Include(cc => cc.Contact)
            .FirstOrDefaultAsync(cc => cc.CustomerId == id && cc.ContactId == contactId, cancellationToken);

        if (association is null)
        {
            return TypedResults.NotFound();
        }

        if (!request.TryApplyTo(association, out var errors))
        {
            return TypedResults.ValidationProblem(errors, title: "Invalid contact association");
        }

        await dbContext.SaveChangesAsync(cancellationToken);

        return TypedResults.Ok(CustomerContactResponse.FromDomain(association));
    }
}