using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Products.Api.Database.Products;

namespace Vantigo.Products.Api.Endpoints.Products.Prices;

/// <summary>
/// Removes a price row from a product.
/// </summary>
internal static class DeleteProductPriceEndpoint
{
    internal static async Task<Results<NoContent, NotFound>> Handler(
        int id,
        int priceId,
        ProductsDbContext dbContext,
        CancellationToken cancellationToken)
    {
        var price = await dbContext.ProductPrices
            .FirstOrDefaultAsync(p => p.Id == priceId && p.ProductId == id, cancellationToken);

        if (price is null)
        {
            return TypedResults.NotFound();
        }

        dbContext.ProductPrices.Remove(price);
        await dbContext.SaveChangesAsync(cancellationToken);

        return TypedResults.NoContent();
    }
}