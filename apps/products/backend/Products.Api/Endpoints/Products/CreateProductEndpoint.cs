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
            Prices = request.Prices?.Select(price => price.ToDomain()).ToList() ?? [],
        };

        dbContext.Products.Add(product);
        await dbContext.SaveChangesAsync(cancellationToken);

        return TypedResults.CreatedAtRoute(
            ProductResponse.FromDomain(product, DateTimeOffset.UtcNow),
            ProductsEndpoints.GetProductRouteName,
            new { id = product.Id });
    }
}