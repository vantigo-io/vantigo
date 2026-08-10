using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Products.Database.Products;
using Vantigo.Products.Domain.Products;
using Vantigo.Products.Endpoints.TaxCategories.Dtos;

namespace Vantigo.Products.Endpoints.TaxCategories;

internal static class UpdateTaxCategoryEndpoint
{
    internal static async Task<Results<Ok<TaxCategoryResponse>, NotFound, ValidationProblem, ProblemHttpResult>> Handler(
        int id,
        TaxCategoryRequest request,
        ProductsDbContext dbContext,
        CancellationToken cancellationToken)
    {
        var errors = request.Validate();
        if (errors.Count > 0)
        {
            return TypedResults.ValidationProblem(errors, title: "Invalid tax category");
        }

        var category = await dbContext.TaxCategories.FirstOrDefaultAsync(item => item.Id == id, cancellationToken);
        if (category is null)
        {
            return TypedResults.NotFound();
        }

        var name = request.Name.Trim();
        if (await dbContext.TaxCategories.AnyAsync(
                item => item.Name == name && item.Id != id, cancellationToken))
        {
            return TypedResults.Problem(
                title: "Duplicate tax category name",
                detail: $"A tax category named '{name}' already exists.",
                statusCode: StatusCodes.Status409Conflict);
        }

        category.Name = name;
        category.Kind = Enum.Parse<TaxCategoryKind>(request.Kind, ignoreCase: true);
        category.Rate = request.Rate;
        await dbContext.SaveChangesAsync(cancellationToken);

        return TypedResults.Ok(TaxCategoryResponse.FromDomain(category));
    }
}