using System.Security.Claims;

using Microsoft.AspNetCore.Authorization;
using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Customers.Authorization;
using Vantigo.Customers.Database.Customers;
using Vantigo.Customers.Domain.Customers.Common;

namespace Vantigo.Customers.Endpoints.Customers;

/// <summary>
/// Returns tenant-wide customer key figures for the customers overview page. The counts are
/// global and intentionally unaffected by search or pagination. Identity-derived figures are
/// only included for callers that are allowed to view legal identities.
/// </summary>
internal static class GetCustomerStatsEndpoint
{
    internal static async Task<Ok<Response>> Handler(
        ClaimsPrincipal principal,
        IAuthorizationService authorization,
        CustomersDbContext dbContext,
        CancellationToken cancellationToken)
    {
        // Archived customers are excluded from every key figure: the overview
        // reports the working customer base, and archival is this module's
        // delete semantics.
        var customers = dbContext.Customers.AsNoTracking()
            .Where(c => c.Status != (CustomerStatus)CustomerStatus.Archived);

        var totalCount = await customers.CountAsync(cancellationToken);
        var activeCount = await customers.CountAsync(
            c => c.Status == (CustomerStatus)CustomerStatus.Active, cancellationToken);

        var newSince = DateTimeOffset.UtcNow.AddDays(-30);
        var newLast30DaysCount = await customers.CountAsync(
            c => c.CreatedAt >= newSince, cancellationToken);

        var includeIdentity = await CustomerAuthorization.HasPermissionAsync(
            authorization, principal, CustomerPermissions.LegalIdentityView);

        int? businessCount = null;
        int? personCount = null;
        int? missingIdentityCount = null;
        int? distinctCountryCount = null;

        if (includeIdentity)
        {
            businessCount = await customers.CountAsync(
                c => c.Identity != null && c.Identity.Value.Type == (LegalType)LegalType.Business,
                cancellationToken);
            personCount = await customers.CountAsync(
                c => c.Identity != null && c.Identity.Value.Type == (LegalType)LegalType.Person,
                cancellationToken);
            missingIdentityCount = await customers.CountAsync(
                c => c.Identity == null, cancellationToken);
            distinctCountryCount = await customers
                .Where(c => c.Identity != null)
                .Select(c => c.Identity!.Value.Country)
                .Distinct()
                .CountAsync(cancellationToken);
        }

        return TypedResults.Ok(new Response
        {
            TotalCount = totalCount,
            ActiveCount = activeCount,
            NewLast30DaysCount = newLast30DaysCount,
            BusinessCount = businessCount,
            PersonCount = personCount,
            MissingIdentityCount = missingIdentityCount,
            DistinctCountryCount = distinctCountryCount,
        });
    }

    internal readonly record struct Response
    {
        public required int TotalCount { get; init; }
        public required int ActiveCount { get; init; }
        public required int NewLast30DaysCount { get; init; }

        /// <summary>Null when the caller lacks the legal-identity view permission.</summary>
        public int? BusinessCount { get; init; }

        /// <summary>Null when the caller lacks the legal-identity view permission.</summary>
        public int? PersonCount { get; init; }

        /// <summary>Null when the caller lacks the legal-identity view permission.</summary>
        public int? MissingIdentityCount { get; init; }

        /// <summary>Null when the caller lacks the legal-identity view permission.</summary>
        public int? DistinctCountryCount { get; init; }
    }
}