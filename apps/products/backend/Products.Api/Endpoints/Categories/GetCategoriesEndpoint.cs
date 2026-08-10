using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Products.Api.Database.Products;
using Vantigo.Products.Api.Endpoints.Categories.Dtos;

namespace Vantigo.Products.Api.Endpoints.Categories;

/// <summary>
/// Lists all categories as a flat adjacency list ordered by name, each with the
/// number of directly assigned products. Clients build the tree from the parentId
/// references; the catalog is small enough that pagination is unnecessary.
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

        var productCounts = await dbContext.Products
            .Where(p => p.CategoryId != null)
            .GroupBy(p => p.CategoryId!.Value)
            .Select(group => new { CategoryId = group.Key, Count = group.Count() })
            .ToDictionaryAsync(item => item.CategoryId, item => item.Count, cancellationToken);

        IReadOnlyList<CategoryResponse> response = categories
            .Select(category => CategoryResponse.FromDomain(
                category,
                productCounts.GetValueOrDefault(category.Id)))
            .ToArray();

        return TypedResults.Ok(response);
    }
}