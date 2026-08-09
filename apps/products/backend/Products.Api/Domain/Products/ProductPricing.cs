namespace Vantigo.Products.Api.Domain.Products;

/// <summary>
/// Resolution rules for the effective sales price of a product. The applicable price
/// in a currency is the row whose validity window contains the moment; a bounded
/// (campaign) price beats the open-ended base price, and the latest starting window
/// wins ties.
/// </summary>
public static class ProductPricing
{
    /// <summary>
    /// Resolves the effective price per currency at the given moment.
    /// </summary>
    public static IReadOnlyList<ProductPrice> GetEffectivePrices(
        IEnumerable<ProductPrice> prices,
        DateTimeOffset moment)
    {
        return prices
            .Where(price => price.IsValidAt(moment))
            .GroupBy(price => price.Currency, StringComparer.OrdinalIgnoreCase)
            .Select(group => group
                .OrderByDescending(price => price.IsBounded)
                .ThenByDescending(price => price.ValidFrom ?? DateTimeOffset.MinValue)
                .ThenByDescending(price => price.Id)
                .First())
            .OrderBy(price => price.Currency, StringComparer.OrdinalIgnoreCase)
            .ToArray();
    }

    /// <summary>
    /// Resolves the effective price in a single currency at the given moment, or null
    /// when no price is valid.
    /// </summary>
    public static ProductPrice? GetEffectivePrice(
        IEnumerable<ProductPrice> prices,
        string currency,
        DateTimeOffset moment)
    {
        return GetEffectivePrices(
                prices.Where(price => string.Equals(price.Currency, currency, StringComparison.OrdinalIgnoreCase)),
                moment)
            .FirstOrDefault();
    }

    /// <summary>
    /// Determines whether a candidate price conflicts with an existing price. Two prices
    /// in the same currency conflict when their windows overlap and they are of the same
    /// kind (both open-ended base prices, or both bounded campaign prices), because the
    /// resolution rules could not deterministically choose between them.
    /// </summary>
    public static bool Conflicts(ProductPrice candidate, ProductPrice existing)
    {
        if (!string.Equals(candidate.Currency, existing.Currency, StringComparison.OrdinalIgnoreCase))
        {
            return false;
        }

        if (candidate.IsBounded != existing.IsBounded)
        {
            return false;
        }

        return candidate.Overlaps(existing);
    }
}