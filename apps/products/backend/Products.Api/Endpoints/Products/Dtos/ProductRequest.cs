using System.Globalization;

using Vantigo.Products.Api.Domain.Products;

namespace Vantigo.Products.Api.Endpoints.Products.Dtos;

/// <summary>
/// The product fields supplied by API clients when creating or updating a product.
/// </summary>
internal readonly record struct ProductRequest
{
    public required string Name { get; init; }
    public required string Sku { get; init; }
    public required string Type { get; init; }
    public string? Status { get; init; }
    public string? Unit { get; init; }
    public decimal? StandardCost { get; init; }
    public required decimal VatRate { get; init; }
    public IReadOnlyList<ProductPriceRequest>? Prices { get; init; }

    internal Dictionary<string, string[]> Validate()
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

        if (string.IsNullOrWhiteSpace(Sku))
        {
            errors["sku"] = ["'sku' is required."];
        }
        else if (Sku.Trim().Length > Product.SkuMaxLength)
        {
            errors["sku"] = [$"'sku' must be at most {Product.SkuMaxLength} characters."];
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

        if (Unit is not null &&
            (string.IsNullOrWhiteSpace(Unit) || Unit.Trim().Length > Product.UnitMaxLength))
        {
            errors["unit"] = [$"'unit' must be a non-empty value of at most {Product.UnitMaxLength} characters."];
        }

        if (StandardCost is < 0)
        {
            errors["standardCost"] = ["'standardCost' must be zero or greater."];
        }

        if (VatRate is < 0 or > 1)
        {
            errors["vatRate"] =
                [$"'vatRate' must be between 0 and 1, but was {VatRate.ToString(CultureInfo.InvariantCulture)}."];
        }

        if (Prices is { } prices)
        {
            for (var index = 0; index < prices.Count; index++)
            {
                prices[index].Validate($"prices[{index}].", errors);
            }

            // Reject price combinations the effective-price rules could not
            // deterministically resolve, for instance two open-ended base prices in
            // the same currency or two overlapping campaign windows.
            var candidates = prices.Select(price => price.ToDomain()).ToArray();
            for (var index = 0; index < candidates.Length; index++)
            {
                for (var other = index + 1; other < candidates.Length; other++)
                {
                    if (ProductPricing.Conflicts(candidates[index], candidates[other]))
                    {
                        errors[$"prices[{other}]"] =
                            [$"The price overlaps another {candidates[other].Currency} price of the same kind."];
                    }
                }
            }
        }

        return errors;
    }
}