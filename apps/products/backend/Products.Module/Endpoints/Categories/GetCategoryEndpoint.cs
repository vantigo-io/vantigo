using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Products.Database.Products;
using Vantigo.Products.Endpoints.Categories.Dtos;

namespace Vantigo.Products.Endpoints.Categories;

/// <summary>
/// Gets a single category by id.
/// </summary>
internal static class GetCategoryEndpoint
{
    internal static async Task<Results<Ok<CategoryResponse>, NotFound>> Handler(
        int id,
        ProductsDbContext dbContext,
        CancellationToken cancellationToken)
    {
        var category = await dbContext.ProductCategories
            .AsNoTracking()
            .FirstOrDefaultAsync(c => c.Id == id, cancellationToken);

        if (category is null)
        {
            return TypedResults.NotFound();
        }

        return TypedResults.Ok(CategoryResponse.FromDomain(category));
    }
}