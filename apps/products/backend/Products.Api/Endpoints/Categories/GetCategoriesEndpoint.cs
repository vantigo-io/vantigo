using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Products.Api.Database.Products;
using Vantigo.Products.Api.Endpoints.Categories.Dtos;

namespace Vantigo.Products.Api.Endpoints.Categories;

/// <summary>
/// Lists all categories as a flat adjacency list ordered by name. Clients build the
/// tree from the parentId references; the catalog is small enough that pagination
/// is unnecessary.
/// </summary>
internal static class GetCategoriesEndpoint
{
    internal static async Task<Ok<IReadOnlyList<CategoryResponse>>> Handler(
        ProductsDbContext dbContext,
        CancellationToken cancellationToken)
    {
        var categories = await dbContext.ProductCategories
            .AsNoTracking()
            .OrderBy(c => c.Name)
            .ThenBy(c => c.Id)
            .ToListAsync(cancellationToken);

        IReadOnlyList<CategoryResponse> response = categories
            .Select(CategoryResponse.FromDomain)
            .ToArray();

        return TypedResults.Ok(response);
    }
}