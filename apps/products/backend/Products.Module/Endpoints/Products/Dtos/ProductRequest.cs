using Vantigo.Products.Domain.Products;

namespace Vantigo.Products.Endpoints.Products.Dtos;

/// <summary>
/// The shared product fields supplied by API clients. Variants are required when creating
/// a product and are managed through the variant sub-resource after creation.
/// </summary>
internal readonly record struct ProductRequest
{
    public required string Name { get; init; }
    public required string Type { get; init; }
    public string? Status { get; init; }
    public required int TaxCategoryId { get; init; }
    public string? Description { get; init; }
    public int? CategoryId { get; init; }
    public IReadOnlyList<VariantRequest>? Variants { get; init; }

    internal Dictionary<string, string[]> Validate(bool requireVariants, bool validateVariants = true)
    {
        var errors = new Dictionary<string, string[]>();

        if (string.IsNullOrWhiteSpace(Name))
        {
            errors["name"] = ["'name' is required."];
        }
        else if (Name.Trim().Length > Product.NameMaxLength)
        {
            errors["name"] = [$"'name' must be at most {Product.NameMaxLength} characters."];
        }

        if (!Enum.TryParse<ProductType>(Type, ignoreCase: true, out _))
        {
            errors["type"] = [$"'type' must be one of 'Goods' or 'Service', but was '{Type}'."];
        }

        if (Status is not null && !Enum.TryParse<ProductStatus>(Status, ignoreCase: true, out _))
        {
            errors["status"] =
                [$"'status' must be one of 'Draft', 'Active' or 'Discontinued', but was '{Status}'."];
        }

        if (TaxCategoryId < 1)
        {
            errors["taxCategoryId"] = [$"'taxCategoryId' must be 1 or greater, but was {TaxCategoryId}."];
        }

        if (Description is { } description && description.Trim().Length > Product.DescriptionMaxLength)
        {
            errors["description"] = [$"'description' must be at most {Product.DescriptionMaxLength} characters."];
        }

        if (requireVariants && (Variants is null || Variants.Count == 0))
        {
            errors["variants"] = ["At least one variant is required."];
        }

        if (validateVariants && Variants is { } variants)
        {
            for (var index = 0; index < variants.Count; index++)
            {
                variants[index].Validate($"variants[{index}].", errors);
            }
        }

        return errors;
    }
}

/// <summary>The sellable variant fields supplied by API clients.</summary>
internal readonly record struct VariantRequest
{
    public required string Sku { get; init; }
    public string? Barcode { get; init; }
    public string? Unit { get; init; }
    public decimal? StandardCost { get; init; }
    public decimal? WeightKg { get; init; }
    public decimal? LengthCm { get; init; }
    public decimal? WidthCm { get; init; }
    public decimal? HeightCm { get; init; }
    public Dictionary<string, string>? OptionValues { get; init; }
    public IReadOnlyList<ProductPriceRequest>? Prices { get; init; }

    internal void Validate(string prefix, Dictionary<string, string[]> errors)
    {
        var sku = Sku?.Trim();
        if (string.IsNullOrWhiteSpace(sku))
        {
            errors[$"{prefix}sku"] = ["'sku' is required."];
        }
        else if (sku.Length > ProductVariant.SkuMaxLength)
        {
            errors[$"{prefix}sku"] = [$"'sku' must be at most {ProductVariant.SkuMaxLength} characters."];
        }

        var barcode = NormalizeOptional(Barcode);
        if (barcode is not null && !Gtin.IsValid(barcode))
        {
            errors[$"{prefix}barcode"] =
                ["'barcode' must be a valid GTIN-8, GTIN-12, GTIN-13 or GTIN-14: digits only with a correct check digit."];
        }

        if (Unit is not null &&
            (string.IsNullOrWhiteSpace(Unit) || Unit.Trim().Length > ProductVariant.UnitMaxLength))
        {
            errors[$"{prefix}unit"] = [$"'unit' must be a non-empty value of at most {ProductVariant.UnitMaxLength} characters."];
        }

        if (StandardCost is < 0)
        {
            errors[$"{prefix}standardCost"] = ["'standardCost' must be zero or greater."];
        }

        ValidateNonNegative(WeightKg, "weightKg", prefix, errors);
        ValidateNonNegative(LengthCm, "lengthCm", prefix, errors);
        ValidateNonNegative(WidthCm, "widthCm", prefix, errors);
        ValidateNonNegative(HeightCm, "heightCm", prefix, errors);

        if (Prices is { } prices)
        {
            for (var index = 0; index < prices.Count; index++)
            {
                prices[index].Validate($"{prefix}prices[{index}].", errors);
            }

            var candidates = prices.Select(price => price.ToDomain()).ToArray();
            for (var index = 0; index < candidates.Length; index++)
            {
                for (var other = index + 1; other < candidates.Length; other++)
                {
                    if (ProductPricing.Conflicts(candidates[index], candidates[other]))
                    {
                        errors[$"{prefix}prices[{other}]"] =
                            [$"The price overlaps another {candidates[other].Currency} price of the same kind."];
                    }
                }
            }
        }
    }

    internal ProductVariant ToDomain() => new()
    {
        Sku = Sku.Trim(),
        Barcode = NormalizeOptional(Barcode),
        Unit = NormalizeOptional(Unit) ?? ProductVariant.DefaultUnit,
        StandardCost = StandardCost,
        WeightKg = WeightKg,
        LengthCm = LengthCm,
        WidthCm = WidthCm,
        HeightCm = HeightCm,
        OptionValues = OptionValues is null ? [] : new Dictionary<string, string>(OptionValues, StringComparer.OrdinalIgnoreCase),
        Prices = Prices?.Select(price => price.ToDomain()).ToList() ?? [],
    };

    private static void ValidateNonNegative(
        decimal? value,
        string field,
        string prefix,
        Dictionary<string, string[]> errors)
    {
        if (value < 0)
        {
            errors[$"{prefix}{field}"] = [$"'{field}' must be zero or greater."];
        }
    }

    private static string? NormalizeOptional(string? value) =>
        string.IsNullOrWhiteSpace(value) ? null : value.Trim();
}