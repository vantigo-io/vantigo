using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Products.Database.Products;
using Vantigo.Products.Endpoints.Products.Dtos;

namespace Vantigo.Products.Endpoints.Products.Variants;

/// <summary>Lists all variants belonging to a product.</summary>
internal static class GetProductVariantsEndpoint
{
    internal static async Task<Results<Ok<IReadOnlyList<ProductVariantResponse>>, NotFound>> Handler(
        int id, ProductsDbContext dbContext, CancellationToken cancellationToken)
    {
        if (!await dbContext.Products.AnyAsync(product => product.Id == id, cancellationToken))
        {
            return TypedResults.NotFound();
        }

        var variants = await dbContext.ProductVariants.AsNoTracking()
            .Include(variant => variant.Prices)
            .Where(variant => variant.ProductId == id)
            .OrderBy(variant => variant.Id)
            .ToListAsync(cancellationToken);
        var moment = DateTimeOffset.UtcNow;
        return TypedResults.Ok<IReadOnlyList<ProductVariantResponse>>(
            variants.Select(variant => ProductVariantResponse.FromDomain(variant, moment)).ToArray());
    }
}