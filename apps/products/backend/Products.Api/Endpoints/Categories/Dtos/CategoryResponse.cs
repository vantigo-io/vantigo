using Vantigo.Products.Api.Domain.Products;

namespace Vantigo.Products.Api.Endpoints.Categories.Dtos;

/// <summary>
/// The shared representation of a category returned by the category endpoints. The
/// endpoints return the flat adjacency list; clients build the tree from parentId.
/// </summary>
internal readonly record struct CategoryResponse
{
    public required int Id { get; init; }
    public required string Name { get; init; }
    public int? ParentId { get; init; }

    internal static CategoryResponse FromDomain(ProductCategory category) => new()
    {
        Id = category.Id,
        Name = category.Name,
        ParentId = category.ParentId,
    };
}