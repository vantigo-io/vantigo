namespace Vantigo.Energy.Endpoints.Dtos;

internal sealed record PaginatedResponse<T>(IReadOnlyList<T> Data, PaginationMetadata Pagination)
{
    internal static PaginatedResponse<T> Create(IReadOnlyList<T> data, int page, int pageSize, int totalCount) =>
        new(data, PaginationMetadata.Create(page, pageSize, totalCount));
}