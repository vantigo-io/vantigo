using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Customers.Database.Customers;
using Vantigo.Customers.Domain.Customers.Common;
using Vantigo.Customers.Services;

namespace Vantigo.Customers.Endpoints.Customers;

/// <summary>
/// Archives a customer instead of hard-deleting it: Communications and Energy
/// keep historical references to customer ids, so the row must survive for
/// those views, reports, and the audit timeline. Archiving hides the customer
/// from default listings while it stays resolvable by id; the operation is
/// idempotent, and the status change is recorded on the timeline.
/// </summary>
internal static class DeleteCustomerEndpoint
{
    internal static async Task<Results<NoContent, NotFound>> Handler(
        int id,
        CustomersDbContext dbContext,
        ICustomerTimelineRecorder timelineRecorder,
        CancellationToken cancellationToken)
    {
        var customer = await dbContext.Customers.FirstOrDefaultAsync(item => item.Id == id, cancellationToken);
        if (customer is null)
        {
            return TypedResults.NotFound();
        }

        if (customer.Status != (CustomerStatus)CustomerStatus.Archived)
        {
            var previousStatus = customer.Status;
            customer.Status = CustomerStatus.Archived;
            customer.UpdatedAt = DateTimeOffset.UtcNow;
            timelineRecorder.RecordCustomerStatusChanged(customer, previousStatus);
            await dbContext.SaveChangesAsync(cancellationToken);
        }

        return TypedResults.NoContent();
    }
}