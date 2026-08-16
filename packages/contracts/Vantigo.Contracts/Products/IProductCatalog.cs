namespace Vantigo.Contracts.Products;

/// <summary>
/// The safe, read-only product catalog exposed to other modules.
/// Implementations must return only products that are currently sellable.
/// </summary>
public interface IProductCatalog
{
    /// <summary>
    /// Searches the sellable catalog using a normalized, case-insensitive query.
    /// Implementations clamp <paramref name="take" /> to the inclusive range 1-10.
    /// </summary>
    Task<IReadOnlyList<ProductCatalogSearchResult>> SearchAsync(
        string query,
        int take,
        CancellationToken cancellationToken = default);

    /// <summary>Gets a sellable product by id, or null when it does not exist or is not sellable.</summary>
    Task<ProductCatalogProduct?> GetByIdAsync(
        int productId,
        CancellationToken cancellationToken = default);
}

/// <summary>A compact product returned by catalog search.</summary>
public sealed record ProductCatalogSearchResult(
    int Id,
    string Name,
    string? Description,
    string Type,
    string Status,
    ProductCatalogCategory? Category,
    IReadOnlyList<ProductCatalogVariant> Variants);

/// <summary>
/// The safe cross-module projection of a currently sellable product. It deliberately omits
/// tax configuration, inventory, standard cost, timestamps, and the underlying entity.
/// </summary>
public sealed record ProductCatalogProduct(
    int Id,
    string Name,
    string? Description,
    string Type,
    string Status,
    ProductCatalogCategory? Category,
    IReadOnlyList<ProductCatalogVariant> Variants);

/// <summary>A compact category reference in a catalog product.</summary>
public sealed record ProductCatalogCategory(int Id, string Name);

/// <summary>A sellable product variant with externally useful identifying and price data.</summary>
public sealed record ProductCatalogVariant(
    int Id,
    string Sku,
    string? Barcode,
    string Unit,
    IReadOnlyDictionary<string, string> OptionValues,
    IReadOnlyList<ProductCatalogPrice> Prices);

/// <summary>An effective sales price, excluding VAT, for one currency.</summary>
public sealed record ProductCatalogPrice(string Currency, decimal Amount);