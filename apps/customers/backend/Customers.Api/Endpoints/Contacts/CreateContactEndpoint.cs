using Microsoft.AspNetCore.Http.HttpResults;

using Vantigo.Customers.Api.Database;
using Vantigo.Customers.Api.Domain.Contacts;
using Vantigo.Customers.Api.Endpoints.Contacts.Dtos;

namespace Vantigo.Customers.Api.Endpoints.Contacts;

/// <summary>
/// Creates a new contact. A contact only requires a first and last name — everything
/// else can be filled in later, and associations to customers are managed separately,
/// so a contact can exist before any customer relation does.
/// </summary>
internal static class CreateContactEndpoint
{
    internal static async Task<Results<CreatedAtRoute<ContactResponse>, ValidationProblem>> Handler(
        ContactRequest request,
        AppDbContext dbContext,
        CancellationToken cancellationToken)
    {
        if (!request.TryParse(out var parsed, out var errors))
        {
            return TypedResults.ValidationProblem(errors, title: "Invalid contact");
        }

        var contact = new Contact();
        parsed.ApplyTo(contact);

        dbContext.Contacts.Add(contact);
        await dbContext.SaveChangesAsync(cancellationToken);

        return TypedResults.CreatedAtRoute(
            ContactResponse.FromDomain(contact),
            ContactsEndpoints.GetContactRouteName,
            new { id = contact.Id });
    }
}