using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Products.Database.Products;
using Vantigo.Products.Endpoints.Products.Dtos;

namespace Vantigo.Products.Endpoints.Products;

/// <summary>
/// Gets a single product by id, including its resolved effective prices.
/// </summary>
internal static class GetProductEndpoint
{
    internal static async Task<Results<Ok<ProductResponse>, NotFound>> Handler(
        int id,
        ProductsDbContext dbContext,
        CancellationToken cancellationToken)
    {
        var product = await dbContext.Products
            .AsNoTracking()
            .Include(p => p.Prices)
            .Include(p => p.Category)
            .FirstOrDefaultAsync(p => p.Id == id, cancellationToken);

        if (product is null)
        {
            return TypedResults.NotFound();
        }

        return TypedResults.Ok(ProductResponse.FromDomain(product, DateTimeOffset.UtcNow));
    }
}