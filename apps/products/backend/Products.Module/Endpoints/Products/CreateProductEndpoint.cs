using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Products.Database.Products;
using Vantigo.Products.Domain.Products;
using Vantigo.Products.Endpoints.Products.Dtos;

namespace Vantigo.Products.Endpoints.Products;

/// <summary>Creates a new product with at least one sellable variant.</summary>
internal static class CreateProductEndpoint
{
    internal static async Task<Results<CreatedAtRoute<ProductResponse>, ValidationProblem, ProblemHttpResult>> Handler(
        ProductRequest request,
        ProductsDbContext dbContext,
        CancellationToken cancellationToken)
    {
        var errors = request.Validate(requireVariants: true);
        if (errors.Count > 0)
        {
            return TypedResults.ValidationProblem(errors, title: "Invalid product");
        }

        var variants = request.Variants!.Select(variant => variant.ToDomain()).ToList();
        var duplicateSku = variants.GroupBy(variant => variant.Sku, StringComparer.Ordinal)
            .FirstOrDefault(group => group.Count() > 1)?.Key;
        if (duplicateSku is not null || await dbContext.ProductVariants.AnyAsync(
                variant => variants.Select(item => item.Sku).Contains(variant.Sku), cancellationToken))
        {
            return TypedResults.Problem(
                title: "Duplicate SKU",
                detail: $"A variant with SKU '{duplicateSku ?? variants[0].Sku}' already exists.",
                statusCode: StatusCodes.Status409Conflict);
        }

        var barcodes = variants.Where(variant => variant.Barcode is not null)
            .Select(variant => variant.Barcode!).ToArray();
        if (barcodes.Distinct(StringComparer.Ordinal).Count() != barcodes.Length ||
            await dbContext.ProductVariants.AnyAsync(
                variant => variant.Barcode != null && barcodes.Contains(variant.Barcode), cancellationToken))
        {
            return TypedResults.Problem(
                title: "Duplicate barcode",
                detail: "A variant with that barcode already exists.",
                statusCode: StatusCodes.Status409Conflict);
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

        var product = new Product
        {
            Name = request.Name.Trim(),
            Type = Enum.Parse<ProductType>(request.Type, ignoreCase: true),
            Status = request.Status is { } status
                ? Enum.Parse<ProductStatus>(status, ignoreCase: true)
                : ProductStatus.Draft,
            TaxCategoryId = request.TaxCategoryId,
            TaxCategory = taxCategory,
            Description = NormalizeOptional(request.Description),
            CategoryId = request.CategoryId,
            Variants = variants,
        };

        dbContext.Products.Add(product);
        await dbContext.SaveChangesAsync(cancellationToken);

        if (product.CategoryId is not null)
        {
            await dbContext.Entry(product).Reference(p => p.Category).LoadAsync(cancellationToken);
        }

        return TypedResults.CreatedAtRoute(
            ProductResponse.FromDomain(product, DateTimeOffset.UtcNow),
            ProductsEndpoints.GetProductRouteName,
            new { id = product.Id });
    }

    private static string? NormalizeOptional(string? value) =>
        string.IsNullOrWhiteSpace(value) ? null : value.Trim();
}