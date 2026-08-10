using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Products.Database.Products;
using Vantigo.Products.Endpoints.TaxCategories.Dtos;

namespace Vantigo.Products.Endpoints.TaxCategories;

internal static class CreateTaxCategoryEndpoint
{
    internal static async Task<Results<CreatedAtRoute<TaxCategoryResponse>, ValidationProblem, ProblemHttpResult>> Handler(
        TaxCategoryRequest request,
        ProductsDbContext dbContext,
        CancellationToken cancellationToken)
    {
        var errors = request.Validate();
        if (errors.Count > 0)
        {
            return TypedResults.ValidationProblem(errors, title: "Invalid tax category");
        }

        var name = request.Name.Trim();
        if (await dbContext.TaxCategories.AnyAsync(category => category.Name == name, cancellationToken))
        {
            return TypedResults.Problem(
                title: "Duplicate tax category name",
                detail: $"A tax category named '{name}' already exists.",
                statusCode: StatusCodes.Status409Conflict);
        }

        var category = request.ToDomain();
        dbContext.TaxCategories.Add(category);
        await dbContext.SaveChangesAsync(cancellationToken);

        return TypedResults.CreatedAtRoute(
            TaxCategoryResponse.FromDomain(category),
            TaxCategoriesEndpoints.GetTaxCategoryRouteName,
            new { id = category.Id });
    }
}