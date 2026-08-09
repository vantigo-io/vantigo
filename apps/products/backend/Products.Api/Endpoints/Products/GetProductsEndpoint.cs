using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Products.Api.Database.Products;
using Vantigo.Products.Api.Domain.Products;
using Vantigo.Products.Api.Endpoints.Dtos;
using Vantigo.Products.Api.Endpoints.Products.Dtos;

namespace Vantigo.Products.Api.Endpoints.Products;

/// <summary>
/// Lists products with pagination, sorting, free-text search and status filtering.
/// </summary>
internal static class GetProductsEndpoint
{
    private const int DefaultPageSize = 25;
    private const int MaxPageSize = 100;

    internal static async Task<Results<Ok<PaginatedResponse<ProductResponse>>, ProblemHttpResult>> Handler(
        [AsParameters] Request request,
        ProductsDbContext dbContext,
        CancellationToken cancellationToken)
    {
        if (Validate(request) is { } problem)
        {
            return problem;
        }

        var page = request.Page ?? 1;
        var pageSize = request.PageSize ?? DefaultPageSize;

        var query = dbContext.Products.AsNoTracking();

        if (!string.IsNullOrWhiteSpace(request.Search))
        {
            var pattern = $"%{EscapeLikePattern(request.Search.Trim())}%";

            query = query.Where(p =>
                EF.Functions.ILike(p.Name, pattern) ||
                EF.Functions.ILike(p.Sku, pattern));
        }

        if (!string.IsNullOrWhiteSpace(request.Status) &&
            Enum.TryParse<ProductStatus>(request.Status, ignoreCase: true, out var status))
        {
            query = query.Where(p => p.Status == status);
        }

        var totalCount = await query.CountAsync(cancellationToken);

        var products = await ApplySorting(query, request)
            .Include(p => p.Prices)
            .Skip((page - 1) * pageSize)
            .Take(pageSize)
            .ToListAsync(cancellationToken);

        var now = DateTimeOffset.UtcNow;
        return TypedResults.Ok(PaginatedResponse<ProductResponse>.Create(
            products.Select(product => ProductResponse.FromDomain(product, now)).ToList(),
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

        if (request.SortBy is not (null or SortFields.Id or SortFields.Name or SortFields.Sku))
        {
            errors.Add($"'sortBy' must be one of '{SortFields.Id}', '{SortFields.Name}' or '{SortFields.Sku}', but was '{request.SortBy}'.");
        }

        if (request.SortDirection is not (null or SortDirections.Ascending or SortDirections.Descending))
        {
            errors.Add($"'sortDirection' must be one of '{SortDirections.Ascending}' or '{SortDirections.Descending}', but was '{request.SortDirection}'.");
        }

        if (request.Status is not null && !Enum.TryParse<ProductStatus>(request.Status, ignoreCase: true, out _))
        {
            errors.Add($"'status' must be one of 'Draft', 'Active' or 'Discontinued', but was '{request.Status}'.");
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

    private static IOrderedQueryable<Product> ApplySorting(IQueryable<Product> query, Request request)
    {
        var descending = request.SortDirection == SortDirections.Descending;

        return (request.SortBy, descending) switch
        {
            (SortFields.Name, false) => query.OrderBy(p => p.Name).ThenBy(p => p.Id),
            (SortFields.Name, true) => query.OrderByDescending(p => p.Name).ThenByDescending(p => p.Id),
            (SortFields.Sku, false) => query.OrderBy(p => p.Sku).ThenBy(p => p.Id),
            (SortFields.Sku, true) => query.OrderByDescending(p => p.Sku).ThenByDescending(p => p.Id),
            (_, true) => query.OrderByDescending(p => p.Id),
            _ => query.OrderBy(p => p.Id),
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

        /// <summary>The number of products per page, between 1 and 100. Defaults to 25.</summary>
        public int? PageSize { get; init; }

        /// <summary>The field to sort by, either "id", "name" or "sku". Defaults to "id".</summary>
        public string? SortBy { get; init; }

        /// <summary>The sort direction, either "asc" or "desc". Defaults to "asc".</summary>
        public string? SortDirection { get; init; }

        /// <summary>Case-insensitive free-text search matching the product name and SKU.</summary>
        public string? Search { get; init; }

        /// <summary>Filter by lifecycle status: "Draft", "Active" or "Discontinued".</summary>
        public string? Status { get; init; }
    }

    internal static class SortFields
    {
        internal const string Id = "id";
        internal const string Name = "name";
        internal const string Sku = "sku";
    }

    internal static class SortDirections
    {
        internal const string Ascending = "asc";
        internal const string Descending = "desc";
    }
}