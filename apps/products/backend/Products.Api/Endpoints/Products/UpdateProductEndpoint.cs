using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Products.Api.Database.Products;
using Vantigo.Products.Api.Domain.Products;
using Vantigo.Products.Api.Endpoints.Products.Dtos;

namespace Vantigo.Products.Api.Endpoints.Products;

/// <summary>
/// Updates a product. The SKU is immutable once the product has been activated,
/// because downstream services key on it. Prices are managed through the price
/// sub-resource and are not affected by this endpoint.
/// </summary>
internal static class UpdateProductEndpoint
{
    internal static async Task<Results<Ok<ProductResponse>, NotFound, ValidationProblem, ProblemHttpResult>> Handler(
        int id,
        ProductRequest request,
        ProductsDbContext dbContext,
        CancellationToken cancellationToken)
    {
        var errors = request.Validate();
        if (errors.Count > 0)
        {
            return TypedResults.ValidationProblem(errors, title: "Invalid product");
        }

        var product = await dbContext.Products
            .Include(p => p.Prices)
            .FirstOrDefaultAsync(p => p.Id == id, cancellationToken);

        if (product is null)
        {
            return TypedResults.NotFound();
        }

        var sku = request.Sku.Trim();
        if (!string.Equals(product.Sku, sku, StringComparison.Ordinal))
        {
            if (product.Status != ProductStatus.Draft)
            {
                return TypedResults.Problem(
                    title: "SKU is immutable",
                    detail: "The SKU cannot be changed after the product has been activated, because other services reference it.",
                    statusCode: StatusCodes.Status409Conflict);
            }

            if (await dbContext.Products.AnyAsync(p => p.Sku == sku && p.Id != id, cancellationToken))
            {
                return TypedResults.Problem(
                    title: "Duplicate SKU",
                    detail: $"A product with SKU '{sku}' already exists.",
                    statusCode: StatusCodes.Status409Conflict);
            }

            product.Sku = sku;
        }

        product.Name = request.Name.Trim();
        product.Type = Enum.Parse<ProductType>(request.Type, ignoreCase: true);
        if (request.Status is { } status)
        {
            product.Status = Enum.Parse<ProductStatus>(status, ignoreCase: true);
        }

        product.Unit = request.Unit?.Trim() ?? product.Unit;
        product.StandardCost = request.StandardCost;
        product.VatRate = request.VatRate;

        await dbContext.SaveChangesAsync(cancellationToken);

        return TypedResults.Ok(ProductResponse.FromDomain(product, DateTimeOffset.UtcNow));
    }
}