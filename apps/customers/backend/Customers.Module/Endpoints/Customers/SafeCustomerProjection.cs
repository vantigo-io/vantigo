using Microsoft.EntityFrameworkCore;

using Vantigo.Customers.Database.Customers;
using Vantigo.Customers.Domain.Timeline;
using Vantigo.Customers.Endpoints.Customers.Dtos;

namespace Vantigo.Customers.Endpoints.Customers;

internal static class SafeCustomerProjection
{
    internal static async Task<SafeTimelineSummary> TimelineSummaryAsync(
        CustomersDbContext dbContext,
        int customerId,
        CancellationToken cancellationToken)
    {
        var query = dbContext.CustomerTimelineEntries
            .AsNoTracking()
            .Where(entry => entry.CustomerId == customerId && entry.State == TimelineState.Active);
        return new SafeTimelineSummary
        {
            EntryCount = await query.CountAsync(cancellationToken),
            LatestOccurredOn = await query
                .OrderByDescending(entry => entry.OccurredOn)
                .Select(entry => (DateOnly?)entry.OccurredOn)
                .FirstOrDefaultAsync(cancellationToken),
        };
    }
}