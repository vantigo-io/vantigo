using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Customers.Api.Database.Customers;
using Vantigo.Customers.Api.Endpoints.Contacts.Dtos;

namespace Vantigo.Customers.Api.Endpoints.Contacts;

/// <summary>
/// Updates an existing contact. The request carries the desired final state of all
/// contact fields: optional fields that are omitted or blank are cleared.
/// </summary>
internal static class UpdateContactEndpoint
{
    internal static async Task<Results<Ok<ContactResponse>, NotFound, ValidationProblem>> Handler(
        int id,
        ContactRequest request,
        CustomersDbContext dbContext,
        CancellationToken cancellationToken)
    {
        if (!request.TryParse(out var parsed, out var errors))
        {
            return TypedResults.ValidationProblem(errors, title: "Invalid contact");
        }

        var contact = await dbContext.Contacts
            .FirstOrDefaultAsync(c => c.Id == id, cancellationToken);

        if (contact is null)
        {
            return TypedResults.NotFound();
        }

        parsed.ApplyTo(contact);
        await dbContext.SaveChangesAsync(cancellationToken);

        return TypedResults.Ok(ContactResponse.FromDomain(contact));
    }
}