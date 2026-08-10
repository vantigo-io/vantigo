using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Products.Database.Products;
using Vantigo.Products.Endpoints.TaxCategories.Dtos;

namespace Vantigo.Products.Endpoints.TaxCategories;

internal static class GetTaxCategoryEndpoint
{
    internal static async Task<Results<Ok<TaxCategoryResponse>, NotFound>> Handler(
        int id,
        ProductsDbContext dbContext,
        CancellationToken cancellationToken)
    {
        var category = await dbContext.TaxCategories
            .AsNoTracking()
            .FirstOrDefaultAsync(item => item.Id == id, cancellationToken);

        return category is null
            ? TypedResults.NotFound()
            : TypedResults.Ok(TaxCategoryResponse.FromDomain(category));
    }
}