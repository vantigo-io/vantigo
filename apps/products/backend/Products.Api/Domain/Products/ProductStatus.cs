namespace Vantigo.Products.Api.Domain.Products;

/// <summary>
/// The lifecycle status of a product. Products are never hard-deleted because
/// other services (orders, warehouse, booking) will reference them; instead a
/// product is discontinued when it should no longer be sold.
/// </summary>
public enum ProductStatus
{
    /// <summary>The product is being prepared and is not yet sellable.</summary>
    Draft,

    /// <summary>The product is sellable. The SKU is immutable from this point on.</summary>
    Active,

    /// <summary>The product is no longer sold, but is retained for historical references.</summary>
    Discontinued,
}