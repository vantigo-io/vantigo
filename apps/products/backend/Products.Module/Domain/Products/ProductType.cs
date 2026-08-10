namespace Vantigo.Products.Domain.Products;

/// <summary>
/// The commercial classification of a product, mirroring the common "goods and
/// services" distinction. Goods are physical items handled by warehousing and
/// shipping, while services are performed and typically scheduled or booked.
/// </summary>
public enum ProductType
{
    Goods,
    Service,
}