using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Customers.Database.Customers;
using Vantigo.Customers.Endpoints.Customers.Dtos;

namespace Vantigo.Customers.Endpoints.Customers;

/// <summary>
/// Retrieves a single customer by its id.
/// </summary>
internal static class GetCustomerEndpoint
{
    internal static async Task<IResult> Handler(
        int id,
        CustomersDbContext dbContext,
        CancellationToken cancellationToken)
    {
        var customer = await dbContext.Customers
            .AsNoTracking()
            .Where(c => c.Id == id)
            .Select(customer => (SafeCustomerResponse?)new SafeCustomerResponse
            {
                Id = customer.Id,
                Name = customer.Name,
                TimelineSummary = new SafeTimelineSummary
                {
                    EntryCount = dbContext.CustomerTimelineEntries.Count(entry =>
                        entry.CustomerId == customer.Id && entry.State == Domain.Timeline.TimelineState.Active),
                    LatestOccurredOn = dbContext.CustomerTimelineEntries
                        .Where(entry => entry.CustomerId == customer.Id && entry.State == Domain.Timeline.TimelineState.Active)
                        .OrderByDescending(entry => entry.OccurredOn)
                        .Select(entry => (DateOnly?)entry.OccurredOn)
                        .FirstOrDefault(),
                },
            })
            .FirstOrDefaultAsync(cancellationToken);

        return customer is null ? TypedResults.NotFound() : TypedResults.Ok(customer);
    }
}