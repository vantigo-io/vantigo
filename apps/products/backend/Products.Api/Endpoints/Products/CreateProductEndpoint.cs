using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Products.Api.Database.Products;
using Vantigo.Products.Api.Domain.Products;
using Vantigo.Products.Api.Endpoints.Products.Dtos;

namespace Vantigo.Products.Api.Endpoints.Products;

/// <summary>
/// Creates a new product, optionally with an initial set of sales prices. The SKU
/// must be unique across all products: a multi-pack or other repackaging of an
/// existing item is a separate product with its own SKU.
/// </summary>
internal static class CreateProductEndpoint
{
    internal static async Task<Results<CreatedAtRoute<ProductResponse>, ValidationProblem, ProblemHttpResult>> Handler(
        ProductRequest request,
        ProductsDbContext dbContext,
        CancellationToken cancellationToken)
    {
        var errors = request.Validate();
        if (errors.Count > 0)
        {
            return TypedResults.ValidationProblem(errors, title: "Invalid product");
        }

        var sku = request.Sku.Trim();
        if (await dbContext.Products.AnyAsync(p => p.Sku == sku, cancellationToken))
        {
            return TypedResults.Problem(
                title: "Duplicate SKU",
                detail: $"A product with SKU '{sku}' already exists.",
                statusCode: StatusCodes.Status409Conflict);
        }

        var barcode = NormalizeOptional(request.Barcode);
        if (barcode is not null &&
            await dbContext.Products.AnyAsync(p => p.Barcode == barcode, cancellationToken))
        {
            return TypedResults.Problem(
                title: "Duplicate barcode",
                detail: $"A product with barcode '{barcode}' already exists.",
                statusCode: StatusCodes.Status409Conflict);
        }

        if (request.CategoryId is { } categoryId &&
            !await dbContext.ProductCategories.AnyAsync(c => c.Id == categoryId, cancellationToken))
        {
            return TypedResults.ValidationProblem(
                new Dictionary<string, string[]> { ["categoryId"] = [$"Category {categoryId} does not exist."] },
                title: "Invalid product");
        }

        var product = new Product
        {
            Name = request.Name.Trim(),
            Sku = sku,
            Type = Enum.Parse<ProductType>(request.Type, ignoreCase: true),
            Status = request.Status is { } status
                ? Enum.Parse<ProductStatus>(status, ignoreCase: true)
                : ProductStatus.Draft,
            Unit = request.Unit?.Trim() ?? Product.DefaultUnit,
            StandardCost = request.StandardCost,
            VatRate = request.VatRate,
            Description = NormalizeOptional(request.Description),
            CategoryId = request.CategoryId,
            Barcode = barcode,
            WeightKg = request.WeightKg,
            LengthCm = request.LengthCm,
            WidthCm = request.WidthCm,
            HeightCm = request.HeightCm,
            Prices = request.Prices?.Select(price => price.ToDomain()).ToList() ?? [],
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