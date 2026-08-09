namespace Vantigo.Products.Api.Domain.Products;

/// <summary>
/// A product is a distinct sellable unit within the Vantigo Products system. Both
/// physical goods and services are products; a multi-pack of an item is its own
/// product with its own SKU, because downstream services (orders, warehouse,
/// booking) price, pick and account for it independently.
/// </summary>
public sealed class Product
{
    public const int NameMaxLength = 200;
    public const int SkuMaxLength = 64;
    public const int UnitMaxLength = 20;
    public const string DefaultUnit = "pcs";

    /// <summary>
    /// The id is the primary method of identifying a product within the system. It is an
    /// auto incrementable value set by the database once a product has been persisted.
    /// Other services should also snapshot the SKU, which is the stable business key.
    /// </summary>
    public int Id { get; set; }

    /// <summary>The display name of the product.</summary>
    public string Name { get; set; } = string.Empty;

    /// <summary>
    /// The stock keeping unit uniquely identifying this sellable unit across the whole
    /// company. The SKU is immutable once the product has been activated, because other
    /// services key on it.
    /// </summary>
    public string Sku { get; set; } = string.Empty;

    /// <summary>Whether the product is a physical good or a performed service.</summary>
    public ProductType Type { get; set; }

    /// <summary>The lifecycle status. Products are archived (discontinued), never deleted.</summary>
    public ProductStatus Status { get; set; } = ProductStatus.Draft;

    /// <summary>
    /// The unit the product is sold in, for instance "pcs" for goods or "hour" for
    /// services. Quantities in future services are expressed in this unit.
    /// </summary>
    public string Unit { get; set; } = DefaultUnit;

    /// <summary>
    /// An indicative cost used for margin estimates, expressed in the company base
    /// currency excluding VAT. Actual cost valuation belongs to the future
    /// warehouse/procurement domain.
    /// </summary>
    public decimal? StandardCost { get; set; }

    /// <summary>The VAT rate applied when the product is sold, for instance 0.25 for 25%.</summary>
    public decimal VatRate { get; set; }

    /// <summary>The sales prices of the product, excluding VAT.</summary>
    public List<ProductPrice> Prices { get; set; } = [];

    public DateTimeOffset CreatedAt { get; set; }
    public DateTimeOffset UpdatedAt { get; set; }
}