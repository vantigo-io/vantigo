namespace Vantigo.Products.Domain.Products;

using Vantigo.Tenancy.Abstractions;

/// <summary>
/// The shared identity of an item or service in the Vantigo Products system. Sellable
/// identity, including SKU, logistics fields and prices, belongs to one or more variants.
/// </summary>
public sealed class Product : ITenantOwned
{
    /// <summary>The maximum length of a product name.</summary>
    public const int NameMaxLength = 200;

    /// <summary>The maximum length of a product description.</summary>
    public const int DescriptionMaxLength = 4000;

    /// <summary>
    /// The auto-generated identity of the product.
    /// </summary>
    public int Id { get; set; }

    /// <summary>The tenant that owns the product.</summary>
    public Guid TenantId { get; set; }

    /// <summary>The display name shared by all variants.</summary>
    public string Name { get; set; } = string.Empty;

    /// <summary>A free-form plain-text description shared by all variants.</summary>
    public string? Description { get; set; }

    /// <summary>
    /// The category the product belongs to; null means uncategorised. A product has at
    /// most one category — cross-cutting grouping is a future tags/collections concern.
    /// </summary>
    public int? CategoryId { get; set; }

    /// <summary>The category the product belongs to, loaded on demand.</summary>
    public ProductCategory? Category { get; set; }

    /// <summary>Whether the product is a physical good or a performed service.</summary>
    public ProductType Type { get; set; }

    /// <summary>The lifecycle status. Products are archived (discontinued), never deleted.</summary>
    public ProductStatus Status { get; set; } = ProductStatus.Draft;

    /// <summary>The centrally configured tax category applied when the product is sold.</summary>
    public int TaxCategoryId { get; set; }

    /// <summary>The tax category applied when the product is sold, loaded on demand.</summary>
    public TaxCategory? TaxCategory { get; set; }

    /// <summary>The sellable variants of this product. Every product must have at least one.</summary>
    public List<ProductVariant> Variants { get; set; } = [];

    /// <summary>When the product was created.</summary>
    public DateTimeOffset CreatedAt { get; set; }

    /// <summary>When the product was last updated.</summary>
    public DateTimeOffset UpdatedAt { get; set; }
}