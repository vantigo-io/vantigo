using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Products.Database.Products;

namespace Vantigo.Products.Endpoints.TaxCategories;

internal static class DeleteTaxCategoryEndpoint
{
    internal static async Task<Results<NoContent, NotFound, ProblemHttpResult>> Handler(
        int id,
        ProductsDbContext dbContext,
        CancellationToken cancellationToken)
    {
        var category = await dbContext.TaxCategories.FirstOrDefaultAsync(item => item.Id == id, cancellationToken);
        if (category is null)
        {
            return TypedResults.NotFound();
        }

        if (await dbContext.Products.AnyAsync(product => product.TaxCategoryId == id, cancellationToken))
        {
            return TypedResults.Problem(
                title: "Tax category has products",
                detail: "Reassign the products before deleting the tax category.",
                statusCode: StatusCodes.Status409Conflict);
        }

        dbContext.TaxCategories.Remove(category);
        await dbContext.SaveChangesAsync(cancellationToken);
        return TypedResults.NoContent();
    }
}