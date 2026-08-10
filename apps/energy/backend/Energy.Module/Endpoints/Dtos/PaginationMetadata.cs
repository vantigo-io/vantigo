namespace Vantigo.Energy.Endpoints.Dtos;

internal sealed record PaginationMetadata(int Page, int PageSize, int TotalCount, int TotalPages)
{
    internal static PaginationMetadata Create(int page, int pageSize, int totalCount) =>
        new(page, pageSize, totalCount, (int)Math.Ceiling(totalCount / (double)pageSize));
}