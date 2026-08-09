using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Products.Api.Database.Products;
using Vantigo.Products.Api.Endpoints.Products.Dtos;

namespace Vantigo.Products.Api.Endpoints.Products.Prices;

/// <summary>
/// Lists all price rows of a product, including expired and future campaign prices.
/// </summary>
internal static class GetProductPricesEndpoint
{
    internal static async Task<Results<Ok<IReadOnlyList<ProductPriceResponse>>, NotFound>> Handler(
        int id,
        ProductsDbContext dbContext,
        CancellationToken cancellationToken)
    {
        if (!await dbContext.Products.AnyAsync(p => p.Id == id, cancellationToken))
        {
            return TypedResults.NotFound();
        }

        var prices = await dbContext.ProductPrices
            .AsNoTracking()
            .Where(price => price.ProductId == id)
            .OrderBy(price => price.Currency)
            .ThenBy(price => price.ValidFrom)
            .ThenBy(price => price.Id)
            .ToListAsync(cancellationToken);

        return TypedResults.Ok<IReadOnlyList<ProductPriceResponse>>(
            prices.Select(ProductPriceResponse.FromDomain).ToArray());
    }
}