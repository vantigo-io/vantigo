using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Products.Database.Products;
using Vantigo.Products.Domain.Products;

namespace Vantigo.Products.Endpoints.Products;

/// <summary>
/// Archives a product by marking it as discontinued. Products are never hard-deleted
/// because other services reference them; the archive operation is idempotent.
/// </summary>
internal static class ArchiveProductEndpoint
{
    internal static async Task<Results<NoContent, NotFound>> Handler(
        int id,
        ProductsDbContext dbContext,
        CancellationToken cancellationToken)
    {
        var product = await dbContext.Products
            .FirstOrDefaultAsync(p => p.Id == id, cancellationToken);

        if (product is null)
        {
            return TypedResults.NotFound();
        }

        if (product.Status != ProductStatus.Discontinued)
        {
            product.Status = ProductStatus.Discontinued;
            await dbContext.SaveChangesAsync(cancellationToken);
        }

        return TypedResults.NoContent();
    }
}