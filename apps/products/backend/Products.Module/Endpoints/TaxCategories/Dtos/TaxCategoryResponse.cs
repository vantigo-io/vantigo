using Vantigo.Products.Domain.Products;

namespace Vantigo.Products.Endpoints.TaxCategories.Dtos;

/// <summary>The tax category representation returned by the API.</summary>
internal readonly record struct TaxCategoryResponse
{
    public required int Id { get; init; }
    public required string Name { get; init; }
    public required string Kind { get; init; }
    public required decimal Rate { get; init; }

    internal static TaxCategoryResponse FromDomain(TaxCategory category) => new()
    {
        Id = category.Id,
        Name = category.Name,
        Kind = category.Kind.ToString(),
        Rate = category.Rate,
    };
}