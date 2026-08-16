namespace Vantigo.Communications.Endpoints;

internal sealed record PaginationMetadata
{
    public required int Page { get; init; }
    public required int PageSize { get; init; }
    public required int TotalCount { get; init; }
    public required int TotalPages { get; init; }
    public required bool HasNextPage { get; init; }
    public required bool HasPreviousPage { get; init; }

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

internal sealed record PaginatedResponse<T>
{
    public required IReadOnlyList<T> Data { get; init; }
    public required PaginationMetadata Pagination { get; init; }
    internal static PaginatedResponse<T> Create(IReadOnlyList<T> data, int page, int pageSize, int totalCount) => new()
    {
        Data = data,
        Pagination = PaginationMetadata.Create(page, pageSize, totalCount),
    };
}