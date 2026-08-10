using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Products.Database.Products;

namespace Vantigo.Products.Endpoints.Products.Variants;

/// <summary>Removes a variant unless it is the product's last variant.</summary>
internal static class DeleteProductVariantEndpoint
{
    internal static async Task<Results<NoContent, NotFound, ProblemHttpResult>> Handler(
        int id, int variantId, ProductsDbContext dbContext, CancellationToken cancellationToken)
    {
        var variant = await dbContext.ProductVariants.FirstOrDefaultAsync(item => item.Id == variantId && item.ProductId == id, cancellationToken);
        if (variant is null)
        {
            return TypedResults.NotFound();
        }

        if (!await dbContext.ProductVariants.AnyAsync(item => item.ProductId == id && item.Id != variantId, cancellationToken))
        {
            return TypedResults.Problem(title: "Last variant", detail: "A product must have at least one variant.", statusCode: StatusCodes.Status409Conflict);
        }

        dbContext.ProductVariants.Remove(variant);
        await dbContext.SaveChangesAsync(cancellationToken);
        return TypedResults.NoContent();
    }
}