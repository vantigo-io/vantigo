using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Products.Api.Database.Products;
using Vantigo.Products.Api.Domain.Products;
using Vantigo.Products.Api.Endpoints.Categories.Dtos;

namespace Vantigo.Products.Api.Endpoints.Categories;

/// <summary>
/// Renames and/or re-parents a category. Re-parenting is rejected when it would
/// create a cycle, determined by walking up the ancestor chain of the new parent.
/// </summary>
internal static class UpdateCategoryEndpoint
{
    internal static async Task<Results<Ok<CategoryResponse>, NotFound, ValidationProblem, ProblemHttpResult>> Handler(
        int id,
        CategoryRequest request,
        ProductsDbContext dbContext,
        CancellationToken cancellationToken)
    {
        var errors = request.Validate();
        if (errors.Count > 0)
        {
            return TypedResults.ValidationProblem(errors, title: "Invalid category");
        }

        var category = await dbContext.ProductCategories
            .FirstOrDefaultAsync(c => c.Id == id, cancellationToken);

        if (category is null)
        {
            return TypedResults.NotFound();
        }

        if (request.ParentId == id)
        {
            return TypedResults.ValidationProblem(
                new Dictionary<string, string[]> { ["parentId"] = ["A category cannot be its own parent."] },
                title: "Invalid category");
        }

        if (request.ParentId is { } parentId)
        {
            var parentByCategoryId = await dbContext.ProductCategories
                .AsNoTracking()
                .ToDictionaryAsync(c => c.Id, c => c.ParentId, cancellationToken);

            if (!parentByCategoryId.ContainsKey(parentId))
            {
                return TypedResults.ValidationProblem(
                    new Dictionary<string, string[]> { ["parentId"] = [$"Category {parentId} does not exist."] },
                    title: "Invalid category");
            }

            if (ProductCategoryHierarchy.WouldCreateCycle(id, parentId, parentByCategoryId))
            {
                return TypedResults.Problem(
                    title: "Category cycle",
                    detail: "The category cannot be moved under one of its own descendants.",
                    statusCode: StatusCodes.Status409Conflict);
            }
        }

        var name = request.Name.Trim();
        if (await dbContext.ProductCategories.AnyAsync(
                c => c.ParentId == request.ParentId && c.Name == name && c.Id != id,
                cancellationToken))
        {
            return TypedResults.Problem(
                title: "Duplicate category name",
                detail: $"A category named '{name}' already exists under the same parent.",
                statusCode: StatusCodes.Status409Conflict);
        }

        category.Name = name;
        category.ParentId = request.ParentId;
        await dbContext.SaveChangesAsync(cancellationToken);

        return TypedResults.Ok(CategoryResponse.FromDomain(category));
    }
}