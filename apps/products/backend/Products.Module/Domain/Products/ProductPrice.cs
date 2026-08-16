namespace Vantigo.Products.Domain.Products;

using Vantigo.Tenancy.Abstractions;

/// <summary>
/// A sales price for a product in a specific currency, excluding VAT. A price can be
/// limited to a validity window, which is how campaign and sale prices are expressed:
/// the everyday base price is open-ended, and a bounded campaign row temporarily takes
/// precedence over it. Consumers must snapshot the effective price at transaction time
/// rather than referencing price rows.
/// </summary>
public sealed class ProductPrice : ITenantOwned
{
    /// <summary>The length of an ISO 4217 currency code.</summary>
    public const int CurrencyLength = 3;

    /// <summary>The auto-generated identity of the price row.</summary>
    public int Id { get; set; }

    /// <summary>The tenant that owns the price row.</summary>
    public Guid TenantId { get; set; }

    /// <summary>The variant that owns this price row.</summary>
    public int VariantId { get; set; }

    /// <summary>The ISO 4217 currency code, for instance "NOK".</summary>
    public string Currency { get; set; } = string.Empty;

    /// <summary>The price amount in the given currency, excluding VAT.</summary>
    public decimal Amount { get; set; }

    /// <summary>When the price becomes valid. Null means valid from the beginning of time.</summary>
    public DateTimeOffset? ValidFrom { get; set; }

    /// <summary>When the price stops being valid (exclusive). Null means open-ended.</summary>
    public DateTimeOffset? ValidTo { get; set; }

    /// <summary>Whether the price has any validity bound, i.e. is a campaign/sale price.</summary>
    public bool IsBounded => ValidFrom is not null || ValidTo is not null;

    /// <summary>Whether the price is valid at the given point in time.</summary>
    public bool IsValidAt(DateTimeOffset moment) =>
        (ValidFrom is null || ValidFrom <= moment) &&
        (ValidTo is null || moment < ValidTo);

    /// <summary>Whether this price's validity window overlaps another price's window.</summary>
    public bool Overlaps(ProductPrice other)
    {
        var thisFrom = ValidFrom ?? DateTimeOffset.MinValue;
        var thisTo = ValidTo ?? DateTimeOffset.MaxValue;
        var otherFrom = other.ValidFrom ?? DateTimeOffset.MinValue;
        var otherTo = other.ValidTo ?? DateTimeOffset.MaxValue;
        return thisFrom < otherTo && otherFrom < thisTo;
    }
}