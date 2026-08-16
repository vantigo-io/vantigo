using Microsoft.EntityFrameworkCore;

using Vantigo.Contracts.Products;
using Vantigo.Products.Database.Products;
using Vantigo.Products.Domain.Products;
using Vantigo.Tenancy.Abstractions;

namespace Vantigo.Products.Services;

/// <summary>
/// Provides the Products module's safe cross-module read model without exposing its
/// DbContext or domain entities.
/// </summary>
internal sealed class ProductCatalog(ProductsDbContext dbContext, ITenantContext tenantContext) : IProductCatalog
{
    private const int MaxTake = 10;
    private const int DescriptionMaxLength = 4000;

    public async Task<IReadOnlyList<ProductCatalogSearchResult>> SearchAsync(
        string query,
        int take,
        CancellationToken cancellationToken = default)
    {
        ArgumentException.ThrowIfNullOrWhiteSpace(query);
        var normalizedQuery = query.Trim();
        var escapedQuery = EscapeLikePattern(normalizedQuery);
        var containsPattern = $"%{escapedQuery}%";
        var prefixPattern = $"{escapedQuery}%";
        var tenantId = tenantContext.Current.Value;

        var products = await dbContext.Products
            .IgnoreQueryFilters()
            .AsNoTracking()
            .Where(product =>
                product.TenantId == tenantId &&
                product.Status == ProductStatus.Active &&
                product.Variants.Any() &&
                (EF.Functions.ILike(product.Name, containsPattern) ||
                 (product.Description != null && EF.Functions.ILike(product.Description, containsPattern)) ||
                 (product.Category != null && EF.Functions.ILike(product.Category.Name, containsPattern)) ||
                 product.Variants.Any(variant =>
                     EF.Functions.ILike(variant.Sku, containsPattern) ||
                     (variant.Barcode != null && EF.Functions.ILike(variant.Barcode, containsPattern)))))
            .OrderByDescending(product => EF.Functions.ILike(product.Name, escapedQuery))
            .ThenByDescending(product => EF.Functions.ILike(product.Name, prefixPattern))
            .ThenBy(product => product.Name)
            .ThenBy(product => product.Id)
            .Take(Math.Clamp(take, 1, MaxTake))
            .Include(product => product.Variants)
                .ThenInclude(variant => variant.Prices)
            .Include(product => product.Category)
            .ToListAsync(cancellationToken);

        var moment = DateTimeOffset.UtcNow;
        return products
            .Select(product => new ProductCatalogSearchResult(
                product.Id,
                product.Name,
                NormalizeDescription(product.Description),
                product.Type.ToString(),
                product.Status.ToString(),
                ToCategory(product.Category),
                ToVariants(product.Variants, moment)))
            .ToArray();
    }

    public async Task<ProductCatalogProduct?> GetByIdAsync(
        int productId,
        CancellationToken cancellationToken = default)
    {
        var tenantId = tenantContext.Current.Value;
        var product = await dbContext.Products
            .IgnoreQueryFilters()
            .AsNoTracking()
            .Where(candidate =>
                candidate.TenantId == tenantId &&
                candidate.Id == productId &&
                candidate.Status == ProductStatus.Active &&
                candidate.Variants.Any(variant => variant.TenantId == tenantId))
            .Include(candidate => candidate.Variants)
                .ThenInclude(variant => variant.Prices)
            .Include(candidate => candidate.Category)
            .SingleOrDefaultAsync(cancellationToken);

        if (product is null)
        {
            return null;
        }

        var moment = DateTimeOffset.UtcNow;
        return new ProductCatalogProduct(
            product.Id,
            product.Name,
            NormalizeDescription(product.Description),
            product.Type.ToString(),
            product.Status.ToString(),
            ToCategory(product.Category),
            ToVariants(product.Variants, moment));
    }

    private static ProductCatalogCategory? ToCategory(ProductCategory? category) =>
        category is null ? null : new ProductCatalogCategory(category.Id, category.Name);

    private static IReadOnlyList<ProductCatalogVariant> ToVariants(
        IEnumerable<ProductVariant> variants,
        DateTimeOffset moment) =>
        variants
            .OrderBy(variant => variant.Id)
            .Select(variant => new ProductCatalogVariant(
                variant.Id,
                variant.Sku,
                variant.Barcode,
                variant.Unit,
                new Dictionary<string, string>(variant.OptionValues, StringComparer.OrdinalIgnoreCase),
                ProductPricing.GetEffectivePrices(variant.Prices, moment)
                    .Select(price => new ProductCatalogPrice(price.Currency, price.Amount))
                    .ToArray()))
            .ToArray();

    private static string? NormalizeDescription(string? description) =>
        description is null
            ? null
            : description[..Math.Min(description.Length, DescriptionMaxLength)];

    private static string EscapeLikePattern(string value) => value
        .Replace(@"\", @"\\")
        .Replace("%", @"\%")
        .Replace("_", @"\_");
}