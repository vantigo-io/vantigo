using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Products.Database.Products;
using Vantigo.Products.Endpoints.Products.Dtos;

namespace Vantigo.Products.Endpoints.Products.Prices;

/// <summary>
/// Lists all price rows of a product, including expired and future campaign prices.
/// </summary>
internal static class GetProductPricesEndpoint
{
    internal static async Task<Results<Ok<IReadOnlyList<ProductPriceResponse>>, NotFound>> Handler(
        int id,
        int variantId,
        ProductsDbContext dbContext,
        CancellationToken cancellationToken)
    {
        if (!await dbContext.ProductVariants.AnyAsync(p => p.Id == variantId && p.ProductId == id, cancellationToken))
        {
            return TypedResults.NotFound();
        }

        var prices = await dbContext.ProductPrices
            .AsNoTracking()
            .Where(price => price.VariantId == variantId)
            .OrderBy(price => price.Currency)
            .ThenBy(price => price.ValidFrom)
            .ThenBy(price => price.Id)
            .ToListAsync(cancellationToken);

        return TypedResults.Ok<IReadOnlyList<ProductPriceResponse>>(
            prices.Select(ProductPriceResponse.FromDomain).ToArray());
    }
}