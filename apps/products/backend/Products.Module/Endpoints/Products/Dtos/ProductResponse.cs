using Vantigo.Products.Domain.Products;

namespace Vantigo.Products.Endpoints.Products.Dtos;

/// <summary>
/// The shared representation of a product returned by the product endpoints. The
/// effective prices are resolved at response time; consumers that record transactions
/// must snapshot the effective price rather than referencing price rows.
/// </summary>
internal readonly record struct ProductResponse
{
    public required int Id { get; init; }
    public required string Name { get; init; }
    public required string Sku { get; init; }
    public required string Type { get; init; }
    public required string Status { get; init; }
    public required string Unit { get; init; }
    public decimal? StandardCost { get; init; }
    public required decimal VatRate { get; init; }
    public string? Description { get; init; }
    public ProductCategoryResponse? Category { get; init; }
    public string? Barcode { get; init; }
    public decimal? WeightKg { get; init; }
    public decimal? LengthCm { get; init; }
    public decimal? WidthCm { get; init; }
    public decimal? HeightCm { get; init; }
    public required IReadOnlyList<ProductPriceResponse> EffectivePrices { get; init; }
    public required DateTimeOffset CreatedAt { get; init; }
    public required DateTimeOffset UpdatedAt { get; init; }

    /// <summary>
    /// Maps a domain <see cref="Product"/> (with prices and category loaded) to its
    /// API representation.
    /// </summary>
    internal static ProductResponse FromDomain(Product product, DateTimeOffset moment) => new()
    {
        Id = product.Id,
        Name = product.Name,
        Sku = product.Sku,
        Type = product.Type.ToString(),
        Status = product.Status.ToString(),
        Unit = product.Unit,
        StandardCost = product.StandardCost,
        VatRate = product.VatRate,
        Description = product.Description,
        Category = product.Category is { } category
            ? new ProductCategoryResponse { Id = category.Id, Name = category.Name }
            : null,
        Barcode = product.Barcode,
        WeightKg = product.WeightKg,
        LengthCm = product.LengthCm,
        WidthCm = product.WidthCm,
        HeightCm = product.HeightCm,
        EffectivePrices = ProductPricing.GetEffectivePrices(product.Prices, moment)
            .Select(ProductPriceResponse.FromDomain)
            .ToArray(),
        CreatedAt = product.CreatedAt,
        UpdatedAt = product.UpdatedAt,
    };
}

/// <summary>
/// The compact category reference embedded in product responses.
/// </summary>
internal readonly record struct ProductCategoryResponse
{
    public required int Id { get; init; }
    public required string Name { get; init; }
}