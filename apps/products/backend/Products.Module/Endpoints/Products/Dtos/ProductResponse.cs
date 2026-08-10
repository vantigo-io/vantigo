using Vantigo.Products.Domain.Products;

namespace Vantigo.Products.Endpoints.Products.Dtos;

/// <summary>
/// A product response containing shared identity and variants. For a single-variant
/// product, the sole variant is also flattened into the top-level sellable fields for
/// an inline UX. Those fields are null or empty for multi-variant products.
/// </summary>
internal readonly record struct ProductResponse
{
    public required int Id { get; init; }
    public required string Name { get; init; }
    public string? Sku { get; init; }
    public required string Type { get; init; }
    public required string Status { get; init; }
    public string? Unit { get; init; }
    public decimal? StandardCost { get; init; }
    public required ProductTaxCategoryResponse TaxCategory { get; init; }
    public string? Description { get; init; }
    public ProductCategoryResponse? Category { get; init; }
    public string? Barcode { get; init; }
    public decimal? WeightKg { get; init; }
    public decimal? LengthCm { get; init; }
    public decimal? WidthCm { get; init; }
    public decimal? HeightCm { get; init; }
    public required IReadOnlyList<ProductPriceResponse> EffectivePrices { get; init; }
    public required IReadOnlyList<ProductVariantResponse> Variants { get; init; }
    public required DateTimeOffset CreatedAt { get; init; }
    public required DateTimeOffset UpdatedAt { get; init; }

    /// <summary>Maps a product with variants and category loaded to its API representation.</summary>
    internal static ProductResponse FromDomain(Product product, DateTimeOffset moment)
    {
        var variants = product.Variants
            .Select(variant => ProductVariantResponse.FromDomain(variant, moment))
            .ToArray();
        ProductVariantResponse? soleVariant = variants.Length == 1 ? variants[0] : null;

        return new ProductResponse
        {
            Id = product.Id,
            Name = product.Name,
            Sku = soleVariant?.Sku,
            Type = product.Type.ToString(),
            Status = product.Status.ToString(),
            Unit = soleVariant?.Unit,
            StandardCost = soleVariant?.StandardCost,
            TaxCategory = ProductTaxCategoryResponse.FromDomain(product.TaxCategory!),
            Description = product.Description,
            Category = product.Category is { } category
                ? new ProductCategoryResponse { Id = category.Id, Name = category.Name }
                : null,
            Barcode = soleVariant?.Barcode,
            WeightKg = soleVariant?.WeightKg,
            LengthCm = soleVariant?.LengthCm,
            WidthCm = soleVariant?.WidthCm,
            HeightCm = soleVariant?.HeightCm,
            EffectivePrices = soleVariant?.EffectivePrices ?? [],
            Variants = variants,
            CreatedAt = product.CreatedAt,
            UpdatedAt = product.UpdatedAt,
        };
    }
}

/// <summary>A sellable variant returned by the API.</summary>
internal readonly record struct ProductVariantResponse
{
    public required int Id { get; init; }
    public required string Sku { get; init; }
    public string? Barcode { get; init; }
    public required string Unit { get; init; }
    public decimal? StandardCost { get; init; }
    public decimal? WeightKg { get; init; }
    public decimal? LengthCm { get; init; }
    public decimal? WidthCm { get; init; }
    public decimal? HeightCm { get; init; }
    public required IReadOnlyDictionary<string, string> OptionValues { get; init; }
    public required IReadOnlyList<ProductPriceResponse> EffectivePrices { get; init; }
    public required DateTimeOffset CreatedAt { get; init; }
    public required DateTimeOffset UpdatedAt { get; init; }

    internal static ProductVariantResponse FromDomain(ProductVariant variant, DateTimeOffset moment) => new()
    {
        Id = variant.Id,
        Sku = variant.Sku,
        Barcode = variant.Barcode,
        Unit = variant.Unit,
        StandardCost = variant.StandardCost,
        WeightKg = variant.WeightKg,
        LengthCm = variant.LengthCm,
        WidthCm = variant.WidthCm,
        HeightCm = variant.HeightCm,
        OptionValues = new Dictionary<string, string>(variant.OptionValues, StringComparer.OrdinalIgnoreCase),
        EffectivePrices = ProductPricing.GetEffectivePrices(variant.Prices, moment)
            .Select(ProductPriceResponse.FromDomain)
            .ToArray(),
        CreatedAt = variant.CreatedAt,
        UpdatedAt = variant.UpdatedAt,
    };
}

/// <summary>The compact category reference embedded in product responses.</summary>
internal readonly record struct ProductCategoryResponse
{
    public required int Id { get; init; }
    public required string Name { get; init; }
}

/// <summary>The compact tax category reference embedded in product responses.</summary>
internal readonly record struct ProductTaxCategoryResponse
{
    public required int Id { get; init; }
    public required string Name { get; init; }
    public required string Kind { get; init; }
    public required decimal Rate { get; init; }

    internal static ProductTaxCategoryResponse FromDomain(TaxCategory category) => new()
    {
        Id = category.Id,
        Name = category.Name,
        Kind = category.Kind.ToString(),
        Rate = category.Rate,
    };
}