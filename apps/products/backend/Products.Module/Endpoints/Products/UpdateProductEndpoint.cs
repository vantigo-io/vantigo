using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Products.Database.Products;
using Vantigo.Products.Domain.Products;
using Vantigo.Products.Endpoints.Products.Dtos;

namespace Vantigo.Products.Endpoints.Products;

/// <summary>Updates only the shared product identity.</summary>
internal static class UpdateProductEndpoint
{
    internal static async Task<Results<Ok<ProductResponse>, NotFound, ValidationProblem, ProblemHttpResult>> Handler(
        int id,
        ProductRequest request,
        ProductsDbContext dbContext,
        CancellationToken cancellationToken)
    {
        var errors = request.Validate(requireVariants: false, validateVariants: false);
        if (errors.Count > 0)
        {
            return TypedResults.ValidationProblem(errors, title: "Invalid product");
        }

        var product = await dbContext.Products
            .Include(p => p.Variants)
                .ThenInclude(variant => variant.Prices)
            .Include(p => p.Category)
            .Include(p => p.TaxCategory)
            .FirstOrDefaultAsync(p => p.Id == id, cancellationToken);
        if (product is null)
        {
            return TypedResults.NotFound();
        }

        if (request.CategoryId is { } categoryId &&
            !await dbContext.ProductCategories.AnyAsync(c => c.Id == categoryId, cancellationToken))
        {
            return TypedResults.ValidationProblem(
                new Dictionary<string, string[]> { ["categoryId"] = [$"Category {categoryId} does not exist."] },
                title: "Invalid product");
        }

        var taxCategory = await dbContext.TaxCategories
            .FirstOrDefaultAsync(category => category.Id == request.TaxCategoryId, cancellationToken);
        if (taxCategory is null)
        {
            return TypedResults.ValidationProblem(
                new Dictionary<string, string[]> { ["taxCategoryId"] = [$"Tax category {request.TaxCategoryId} does not exist."] },
                title: "Invalid product");
        }

        product.Name = request.Name.Trim();
        product.Type = Enum.Parse<ProductType>(request.Type, ignoreCase: true);
        if (request.Status is { } status)
        {
            product.Status = Enum.Parse<ProductStatus>(status, ignoreCase: true);
        }

        product.TaxCategoryId = request.TaxCategoryId;
        product.TaxCategory = taxCategory;
        product.Description = NormalizeOptional(request.Description);
        product.CategoryId = request.CategoryId;
        await dbContext.SaveChangesAsync(cancellationToken);

        return TypedResults.Ok(ProductResponse.FromDomain(product, DateTimeOffset.UtcNow));
    }

    private static string? NormalizeOptional(string? value) =>
        string.IsNullOrWhiteSpace(value) ? null : value.Trim();
}