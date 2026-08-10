using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Products.Api.Database.Products;
using Vantigo.Products.Api.Domain.Products;
using Vantigo.Products.Api.Endpoints.Categories.Dtos;

namespace Vantigo.Products.Api.Endpoints.Categories;

/// <summary>
/// Creates a new category, either as a root (no parent) or as a subcategory of an
/// existing category. Sibling names must be unique.
/// </summary>
internal static class CreateCategoryEndpoint
{
    internal static async Task<Results<CreatedAtRoute<CategoryResponse>, ValidationProblem, ProblemHttpResult>> Handler(
        CategoryRequest request,
        ProductsDbContext dbContext,
        CancellationToken cancellationToken)
    {
        var errors = request.Validate();
        if (errors.Count > 0)
        {
            return TypedResults.ValidationProblem(errors, title: "Invalid category");
        }

        if (request.ParentId is { } parentId &&
            !await dbContext.ProductCategories.AnyAsync(c => c.Id == parentId, cancellationToken))
        {
            return TypedResults.ValidationProblem(
                new Dictionary<string, string[]> { ["parentId"] = [$"Category {parentId} does not exist."] },
                title: "Invalid category");
        }

        var name = request.Name.Trim();
        if (await dbContext.ProductCategories.AnyAsync(
                c => c.ParentId == request.ParentId && c.Name == name,
                cancellationToken))
        {
            return TypedResults.Problem(
                title: "Duplicate category name",
                detail: $"A category named '{name}' already exists under the same parent.",
                statusCode: StatusCodes.Status409Conflict);
        }

        var category = new ProductCategory
        {
            Name = name,
            ParentId = request.ParentId,
        };

        dbContext.ProductCategories.Add(category);
        await dbContext.SaveChangesAsync(cancellationToken);

        return TypedResults.CreatedAtRoute(
            CategoryResponse.FromDomain(category),
            CategoriesEndpoints.GetCategoryRouteName,
            new { id = category.Id });
    }
}