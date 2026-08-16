namespace Vantigo.Products.Domain.Products;

using Vantigo.Tenancy.Abstractions;

/// <summary>
/// The sellable identity of a product. A variant owns the SKU, barcode, logistics
/// fields, option values and sales prices used by downstream commerce operations.
/// </summary>
public sealed class ProductVariant : ITenantOwned
{
    /// <summary>The maximum length of a stock keeping unit.</summary>
    public const int SkuMaxLength = 64;

    /// <summary>The maximum length of a selling unit.</summary>
    public const int UnitMaxLength = 20;

    /// <summary>The default selling unit.</summary>
    public const string DefaultUnit = "pcs";

    /// <summary>The auto-generated identity of the variant.</summary>
    public int Id { get; set; }

    /// <summary>The tenant that owns the variant.</summary>
    public Guid TenantId { get; set; }

    /// <summary>The product that owns this variant.</summary>
    public int ProductId { get; set; }

    /// <summary>The stock keeping unit, unique among this tenant's variants.</summary>
    public string Sku { get; set; } = string.Empty;

    /// <summary>The GTIN barcode, unique among this tenant's variants when set.</summary>
    public string? Barcode { get; set; }

    /// <summary>The unit this variant is sold in, for instance pcs or hour.</summary>
    public string Unit { get; set; } = DefaultUnit;

    /// <summary>An indicative cost in the company base currency excluding VAT.</summary>
    public decimal? StandardCost { get; set; }

    /// <summary>The gross weight of one unit in kilograms.</summary>
    public decimal? WeightKg { get; set; }

    /// <summary>The length of one unit in centimetres.</summary>
    public decimal? LengthCm { get; set; }

    /// <summary>The width of one unit in centimetres.</summary>
    public decimal? WidthCm { get; set; }

    /// <summary>The height of one unit in centimetres.</summary>
    public decimal? HeightCm { get; set; }

    /// <summary>The option values distinguishing this variant, such as Color=Red.</summary>
    public Dictionary<string, string> OptionValues { get; set; } = [];

    /// <summary>The sales prices of this variant, excluding VAT.</summary>
    public List<ProductPrice> Prices { get; set; } = [];

    /// <summary>When the variant was created.</summary>
    public DateTimeOffset CreatedAt { get; set; }

    /// <summary>When the variant was last updated.</summary>
    public DateTimeOffset UpdatedAt { get; set; }
}