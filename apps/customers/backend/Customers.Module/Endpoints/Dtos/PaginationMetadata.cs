namespace Vantigo.Customers.Endpoints.Dtos;

/// <summary>
/// Pagination metadata that accompanies every paginated list response.
/// </summary>
internal sealed record PaginationMetadata
{
    public required int Page { get; init; }
    public required int PageSize { get; init; }
    public required int TotalCount { get; init; }
    public required int TotalPages { get; init; }
    public required bool HasNextPage { get; init; }
    public required bool HasPreviousPage { get; init; }

    /// <summary>
    /// Derives the pagination metadata from the page, page size and total count.
    /// </summary>
    internal static PaginationMetadata Create(int page, int pageSize, int totalCount)
    {
        var totalPages = (int)Math.Ceiling(totalCount / (double)pageSize);

        return new PaginationMetadata
        {
            Page = page,
            PageSize = pageSize,
            TotalCount = totalCount,
            TotalPages = totalPages,
            HasNextPage = page < totalPages,
            HasPreviousPage = page > 1 && totalCount > 0,
        };
    }
}