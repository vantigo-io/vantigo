using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Customers.Api.Database.Customers;
using Vantigo.Customers.Api.Services;

namespace Vantigo.Customers.Api.Endpoints.Contacts;

/// <summary>
/// Deletes a contact. Any associations to customers are removed along with it.
/// </summary>
internal static class DeleteContactEndpoint
{
    internal static async Task<Results<NoContent, NotFound>> Handler(
        int id,
        CustomersDbContext dbContext,
        ICustomerTimelineRecorder timelineRecorder,
        CancellationToken cancellationToken)
    {
        await using var transaction = await dbContext.Database.BeginTransactionAsync(cancellationToken);
        var contact = await dbContext.Contacts
            .FromSqlInterpolated($"SELECT * FROM contacts WHERE id = {id} FOR UPDATE")
            .FirstOrDefaultAsync(cancellationToken);
        if (contact is null)
        {
            return TypedResults.NotFound();
        }

        var associations = await dbContext.CustomersContacts
            .Where(cc => cc.ContactId == id)
            .Include(cc => cc.Contact)
            .ToListAsync(cancellationToken);
        foreach (var association in associations)
        {
            timelineRecorder.RecordContactRemoved(association);
        }

        dbContext.Contacts.Remove(contact);
        await dbContext.SaveChangesAsync(cancellationToken);
        await transaction.CommitAsync(cancellationToken);

        return TypedResults.NoContent();
    }
}