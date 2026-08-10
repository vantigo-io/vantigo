namespace Vantigo.Products.Api.Domain.Products;

/// <summary>
/// A category groups products for navigation and reporting. Categories form a
/// multi-level hierarchy through the parent reference (adjacency list); a category
/// with a parent is a subcategory. Each product belongs to at most one category —
/// cross-cutting grouping is a future tags/collections concern.
/// </summary>
public sealed class ProductCategory
{
    public const int NameMaxLength = 200;

    /// <summary>
    /// The id is the primary method of identifying a category within the system. It is an
    /// auto incrementable value set by the database once a category has been persisted.
    /// </summary>
    public int Id { get; set; }

    /// <summary>The display name of the category, unique among its siblings.</summary>
    public string Name { get; set; } = string.Empty;

    /// <summary>The parent category; null means the category is a root.</summary>
    public int? ParentId { get; set; }
}