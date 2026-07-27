using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Customers.Api.Database;

namespace Vantigo.Customers.Api.Endpoints.Customers.Contacts;

/// <summary>
/// Removes the association between a customer and a contact. The contact itself is
/// kept — it can still be associated with other customers or exist on its own.
/// </summary>
internal static class DetachCustomerContactEndpoint
{
    internal static async Task<Results<NoContent, NotFound>> Handler(
        int id,
        int contactId,
        AppDbContext dbContext,
        CancellationToken cancellationToken)
    {
        var deleted = await dbContext.CustomersContacts
            .Where(cc => cc.CustomerId == id && cc.ContactId == contactId)
            .ExecuteDeleteAsync(cancellationToken);

        return deleted > 0 ? TypedResults.NoContent() : TypedResults.NotFound();
    }
}