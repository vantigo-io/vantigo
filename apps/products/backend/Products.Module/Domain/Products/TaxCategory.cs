namespace Vantigo.Products.Domain.Products;

using Vantigo.Tenancy.Abstractions;

/// <summary>
/// A centrally configured tax treatment that can be assigned to products.
/// </summary>
public sealed class TaxCategory : ITenantOwned
{
    /// <summary>The maximum length of a tax category name.</summary>
    public const int NameMaxLength = 100;

    /// <summary>The auto-generated identity of the tax category.</summary>
    public int Id { get; set; }

    /// <summary>The tenant that owns the tax category.</summary>
    public Guid TenantId { get; set; }

    /// <summary>The unique display name of the tax category.</summary>
    public string Name { get; set; } = string.Empty;

    /// <summary>The tax treatment kind.</summary>
    public TaxCategoryKind Kind { get; set; }

    /// <summary>The tax rate as a fraction, for example 0.25 for 25%.</summary>
    public decimal Rate { get; set; }

    /// <summary>When the tax category was created.</summary>
    public DateTimeOffset CreatedAt { get; set; }

    /// <summary>When the tax category was last updated.</summary>
    public DateTimeOffset UpdatedAt { get; set; }
}