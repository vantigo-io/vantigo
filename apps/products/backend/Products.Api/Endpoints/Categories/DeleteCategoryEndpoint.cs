using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Products.Api.Database.Products;

namespace Vantigo.Products.Api.Endpoints.Categories;

/// <summary>
/// Deletes a category. Deletion is restricted while the category still has
/// subcategories or assigned products, so nothing is orphaned implicitly.
/// </summary>
internal static class DeleteCategoryEndpoint
{
    internal static async Task<Results<NoContent, NotFound, ProblemHttpResult>> Handler(
        int id,
        ProductsDbContext dbContext,
        CancellationToken cancellationToken)
    {
        var category = await dbContext.ProductCategories
            .FirstOrDefaultAsync(c => c.Id == id, cancellationToken);

        if (category is null)
        {
            return TypedResults.NotFound();
        }

        if (await dbContext.ProductCategories.AnyAsync(c => c.ParentId == id, cancellationToken))
        {
            return TypedResults.Problem(
                title: "Category has subcategories",
                detail: "Delete or move the subcategories before deleting the category.",
                statusCode: StatusCodes.Status409Conflict);
        }

        if (await dbContext.Products.AnyAsync(p => p.CategoryId == id, cancellationToken))
        {
            return TypedResults.Problem(
                title: "Category has products",
                detail: "Reassign or uncategorise the products before deleting the category.",
                statusCode: StatusCodes.Status409Conflict);
        }

        dbContext.ProductCategories.Remove(category);
        await dbContext.SaveChangesAsync(cancellationToken);

        return TypedResults.NoContent();
    }
}