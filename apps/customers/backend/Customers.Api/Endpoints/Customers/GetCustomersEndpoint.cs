using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Customers.Api.Database.Customers;
using Vantigo.Customers.Api.Domain.Customers;
using Vantigo.Customers.Api.Endpoints.Customers.Dtos;
using Vantigo.Customers.Api.Endpoints.Dtos;

namespace Vantigo.Customers.Api.Endpoints.Customers;

/// <summary>
/// Lists customers with pagination, sorting and free-text search.
/// </summary>
internal static class GetCustomersEndpoint
{
    private const int DefaultPageSize = 25;
    private const int MaxPageSize = 100;

    internal static async Task<Results<Ok<PaginatedResponse<CustomerResponse>>, ProblemHttpResult>> Handler(
        [AsParameters] Request request,
        CustomersDbContext dbContext,
        CancellationToken cancellationToken)
    {
        if (Validate(request) is { } problem)
        {
            return problem;
        }

        var page = request.Page ?? 1;
        var pageSize = request.PageSize ?? DefaultPageSize;

        var query = dbContext.Customers.AsNoTracking();

        if (!string.IsNullOrWhiteSpace(request.Search))
        {
            var pattern = $"%{EscapeLikePattern(request.Search.Trim())}%";

            query = query.Where(c =>
                EF.Functions.ILike(c.Name, pattern) ||
                (c.Identity != null && (
                    EF.Functions.ILike((string)c.Identity.Value.Name, pattern) ||
                    EF.Functions.ILike((string)c.Identity.Value.Id, pattern))));
        }

        var totalCount = await query.CountAsync(cancellationToken);

        var customers = await ApplySorting(query, request)
            .Skip((page - 1) * pageSize)
            .Take(pageSize)
            .ToListAsync(cancellationToken);

        return TypedResults.Ok(PaginatedResponse<CustomerResponse>.Create(
            customers.Select(CustomerResponse.FromDomain).ToList(),
            page,
            pageSize,
            totalCount));
    }

    private static ProblemHttpResult? Validate(Request request)
    {
        var errors = new List<string>();

        if (request.Page is < 1)
        {
            errors.Add($"'page' must be 1 or greater, but was {request.Page}.");
        }

        if (request.PageSize is < 1 or > MaxPageSize)
        {
            errors.Add($"'pageSize' must be between 1 and {MaxPageSize}, but was {request.PageSize}.");
        }

        if (request.SortBy is not (null or SortFields.Id or SortFields.Name))
        {
            errors.Add($"'sortBy' must be one of '{SortFields.Id}' or '{SortFields.Name}', but was '{request.SortBy}'.");
        }

        if (request.SortDirection is not (null or SortDirections.Ascending or SortDirections.Descending))
        {
            errors.Add($"'sortDirection' must be one of '{SortDirections.Ascending}' or '{SortDirections.Descending}', but was '{request.SortDirection}'.");
        }

        if (errors.Count == 0)
        {
            return null;
        }

        return TypedResults.Problem(
            title: "Invalid query parameters",
            detail: string.Join(" ", errors),
            statusCode: StatusCodes.Status400BadRequest);
    }

    private static IOrderedQueryable<Customer> ApplySorting(IQueryable<Customer> query, Request request)
    {
        var descending = request.SortDirection == SortDirections.Descending;

        return (request.SortBy, descending) switch
        {
            (SortFields.Name, false) => query.OrderBy(c => c.Name).ThenBy(c => c.Id),
            (SortFields.Name, true) => query.OrderByDescending(c => c.Name).ThenByDescending(c => c.Id),
            (_, true) => query.OrderByDescending(c => c.Id),
            _ => query.OrderBy(c => c.Id),
        };
    }

    private static string EscapeLikePattern(string value) => value
        .Replace(@"\", @"\\")
        .Replace("%", @"\%")
        .Replace("_", @"\_");

    internal readonly record struct Request
    {
        /// <summary>The 1-based page to retrieve. Defaults to 1.</summary>
        public int? Page { get; init; }

        /// <summary>The number of customers per page, between 1 and 100. Defaults to 25.</summary>
        public int? PageSize { get; init; }

        /// <summary>The field to sort by, either "id" or "name". Defaults to "id".</summary>
        public string? SortBy { get; init; }

        /// <summary>The sort direction, either "asc" or "desc". Defaults to "asc".</summary>
        public string? SortDirection { get; init; }

        /// <summary>Case-insensitive free-text search matching the customer name, legal name and legal id.</summary>
        public string? Search { get; init; }
    }

    internal static class SortFields
    {
        internal const string Id = "id";
        internal const string Name = "name";
    }

    internal static class SortDirections
    {
        internal const string Ascending = "asc";
        internal const string Descending = "desc";
    }
}