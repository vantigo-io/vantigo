using System.Security.Claims;

using Microsoft.AspNetCore.Authorization;
using Microsoft.EntityFrameworkCore;

using Vantigo.Customers.Authorization;
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
        ClaimsPrincipal principal,
        IAuthorizationService authorization,
        CustomersDbContext dbContext,
        CancellationToken cancellationToken)
    {
        var row = await dbContext.Customers
            .AsNoTracking()
            .Where(c => c.Id == id)
            .Select(customer => new
            {
                customer.Id,
                customer.CustomerNumber,
                customer.Name,
                customer.Status,
                customer.CreatedAt,
                customer.UpdatedAt,
                customer.Identity,
                EntryCount = dbContext.CustomerTimelineEntries.Count(entry =>
                    entry.CustomerId == customer.Id && entry.State == Domain.Timeline.TimelineState.Active),
                LatestOccurredOn = dbContext.CustomerTimelineEntries
                    .Where(entry => entry.CustomerId == customer.Id && entry.State == Domain.Timeline.TimelineState.Active)
                    .OrderByDescending(entry => entry.OccurredOn)
                    .Select(entry => (DateOnly?)entry.OccurredOn)
                    .FirstOrDefault(),
            })
            .FirstOrDefaultAsync(cancellationToken);

        if (row is null)
        {
            return TypedResults.NotFound();
        }

        var includeIdentity = await CustomerAuthorization.HasPermissionAsync(
            authorization, principal, CustomerPermissions.LegalIdentityView);

        return TypedResults.Ok(new SafeCustomerResponse
        {
            Id = row.Id,
            CustomerNumber = row.CustomerNumber,
            Name = row.Name,
            Status = row.Status,
            CreatedAt = row.CreatedAt,
            UpdatedAt = row.UpdatedAt,
            Identity = includeIdentity && row.Identity is { } identity
                ? new SafeCustomerIdentity
                {
                    Country = identity.Country,
                    Type = identity.Type,
                    Id = identity.Id,
                }
                : null,
            TimelineSummary = new SafeTimelineSummary
            {
                EntryCount = row.EntryCount,
                LatestOccurredOn = row.LatestOccurredOn,
            },
        });
    }
}