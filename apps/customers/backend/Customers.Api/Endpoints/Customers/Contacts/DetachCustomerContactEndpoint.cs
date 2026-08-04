using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Customers.Api.Database.Customers;
using Vantigo.Customers.Api.Services;

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
        CustomersDbContext dbContext,
        ICustomerTimelineRecorder timelineRecorder,
        CancellationToken cancellationToken)
    {
        var association = await dbContext.CustomersContacts
            .Include(cc => cc.Contact)
            .FirstOrDefaultAsync(cc => cc.CustomerId == id && cc.ContactId == contactId, cancellationToken);

        if (association is null)
        {
            return TypedResults.NotFound();
        }

        dbContext.CustomersContacts.Remove(association);
        timelineRecorder.RecordContactDetached(association);
        await dbContext.SaveChangesAsync(cancellationToken);

        return TypedResults.NoContent();
    }
}
