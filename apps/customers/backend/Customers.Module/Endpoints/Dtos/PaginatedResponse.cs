namespace Vantigo.Customers.Endpoints.Dtos;

/// <summary>
/// The standard envelope for paginated list responses. Every list endpoint returns
/// its items under <see cref="Data"/> together with <see cref="Pagination"/> metadata,
/// which lets API clients handle all paginated resources with a single generic type.
/// </summary>
internal sealed record PaginatedResponse<T>
{
    public required IReadOnlyList<T> Data { get; init; }
    public required PaginationMetadata Pagination { get; init; }

    /// <summary>
    /// Creates a paginated response for a single page of items, deriving the
    /// pagination metadata from the page, page size and total count.
    /// </summary>
    internal static PaginatedResponse<T> Create(IReadOnlyList<T> data, int page, int pageSize, int totalCount) => new()
    {
        Data = data,
        Pagination = PaginationMetadata.Create(page, pageSize, totalCount),
    };
}