using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Customers.Api.Database;

namespace Vantigo.Customers.Api.Endpoints.Contacts;

/// <summary>
/// Deletes a contact. Any associations to customers are removed along with it.
/// </summary>
internal static class DeleteContactEndpoint
{
    internal static async Task<Results<NoContent, NotFound>> Handler(
        int id,
        AppDbContext dbContext,
        CancellationToken cancellationToken)
    {
        var deleted = await dbContext.Contacts
            .Where(c => c.Id == id)
            .ExecuteDeleteAsync(cancellationToken);

        return deleted > 0 ? TypedResults.NoContent() : TypedResults.NotFound();
    }
}