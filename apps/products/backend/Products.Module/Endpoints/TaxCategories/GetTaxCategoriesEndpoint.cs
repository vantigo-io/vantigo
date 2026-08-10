using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Products.Database.Products;
using Vantigo.Products.Endpoints.TaxCategories.Dtos;

namespace Vantigo.Products.Endpoints.TaxCategories;

internal static class GetTaxCategoriesEndpoint
{
    internal static async Task<Ok<IReadOnlyList<TaxCategoryResponse>>> Handler(
        ProductsDbContext dbContext,
        CancellationToken cancellationToken)
    {
        var categories = await dbContext.TaxCategories
            .AsNoTracking()
            .OrderBy(category => category.Name)
            .Select(category => TaxCategoryResponse.FromDomain(category))
            .ToListAsync(cancellationToken);

        return TypedResults.Ok<IReadOnlyList<TaxCategoryResponse>>(categories);
    }
}